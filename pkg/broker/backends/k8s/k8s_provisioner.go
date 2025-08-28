// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"

	configpb "velda.io/velda/pkg/proto/config"
)

func (m *AgentPool) DeepCopyObject() runtime.Object {
	copy := *m
	return &copy
}
func (al *AgentPoolList) DeepCopyObject() runtime.Object {
	copy := *al
	return &copy
}

func toAgentPool(obj interface{}) (*AgentPool, error) {
	jsonData, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	result := &AgentPool{}
	err = json.Unmarshal(jsonData, result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type K8sProvisioner struct {
	schedulerSet *broker.SchedulerSet
	client       dynamic.ResourceInterface
	cfg          *rest.Config
}

func (p *K8sProvisioner) Run(ctx context.Context) {

	// Set up informer to watch for CRD events
	informer := cache.NewSharedInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return p.client.List(ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return p.client.Watch(ctx, options)
			},
		},
		&unstructured.Unstructured{},
		time.Minute,
	)

	// Add event handlers
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if err := p.onAdd(obj); err != nil {
				log.Printf("Failed to handle agent pool add: %v", err)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if err := p.onUpdate(oldObj, newObj); err != nil {
				log.Printf("Failed to handle agent pool update: %v", err)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if err := p.onDelete(obj); err != nil {
				log.Printf("Failed to handle agent pool delete: %v", err)
			}
		},
	})

	log.Printf("Starting CRD watcher")
	// Start informer
	go informer.Run(ctx.Done())
}

func (p *K8sProvisioner) update(obj interface{}, new bool) error {
	poolCrd, err := toAgentPool(obj)
	if err != nil {
		return err
	}
	pool, err := p.schedulerSet.GetOrCreatePool(poolCrd.Name)
	if err != nil {
		return err
	}

	template := poolCrd.Spec.Template
	template.APIVersion = "core/v1"
	template.Kind = "Pod"
	if len(template.Labels) == 0 {
		return fmt.Errorf("Require at least one label")
	}
	filters := make([]string, 0, len(template.Labels))
	for k, v := range template.Labels {
		filters = append(filters, fmt.Sprintf("%v=%v", k, v))
	}
	filter := strings.Join(filters, ",")

	backend, err := NewK8sPoolBackend(p.cfg, &template, filter)
	if err != nil {
		log.Printf("Failed to create backend: %v", err)
		return err
	}
	// TODO: Should reset current backend?
	pool.PoolManager.UpdateConfig(&broker.AutoScaledPoolConfig{
		Backend:              backend,
		MinIdle:              poolCrd.Spec.AutoScaler.MinIdle,
		MaxSize:              poolCrd.Spec.AutoScaler.MaxReplicas,
		MaxIdle:              poolCrd.Spec.AutoScaler.MaxIdle,
		IdleDecay:            time.Duration(poolCrd.Spec.AutoScaler.IdleDecay) * time.Second,
		KillUnknownAfter:     time.Duration(poolCrd.Spec.AutoScaler.KillUnknownAfter) * time.Second,
		DefaultSlotsPerAgent: poolCrd.Spec.AutoScaler.DefaultSlotsPerAgent,
	})
	log.Printf("Updated pool from CRD update: %v", poolCrd.Name)
	return nil
}

func (p *K8sProvisioner) onAdd(obj interface{}) error {
	return p.update(obj, true)
}

func (p *K8sProvisioner) onUpdate(old, new interface{}) error {
	oldGeneration := old.(*unstructured.Unstructured).GetGeneration()
	newGeneration := new.(*unstructured.Unstructured).GetGeneration()
	if oldGeneration == newGeneration {
		return nil
	}
	return p.update(new, false)
}

func (p *K8sProvisioner) onDelete(obj interface{}) error {
	// TODO: Implement this
	return nil
}

type K8sProvisionerFactory struct{}

func getClusterConfig(cfg *configpb.KubernetesProvisioner) (*rest.Config, error) {
	ctx := context.Background()
	switch cfg.Cluster.(type) {
	case *configpb.KubernetesProvisioner_Gke:
		gke := cfg.GetGke()
		// 1) Get Google creds (ADC). Works with:
		//    - GOOGLE_APPLICATION_CREDENTIALS (service account JSON)
		//    - GCE/GKE metadata server
		//    - gcloud auth application-default login (optional; still no gcloud invocation)
		creds, err := google.FindDefaultCredentials(ctx, container.CloudPlatformScope)
		if err != nil {
			return nil, fmt.Errorf("find default creds: %w", err)
		}

		// 2) Look up the cluster to get endpoint + CA bundle.
		gkeSvc, err := container.NewService(ctx, option.WithTokenSource(creds.TokenSource))
		if err != nil {
			return nil, fmt.Errorf("container service: %w", err)
		}
		name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", gke.GetProject(), gke.GetLocation(), gke.GetClusterName())
		cl, err := gkeSvc.Projects.Locations.Clusters.Get(name).Context(ctx).Do()

		if err != nil {
			return nil, fmt.Errorf("get cluster: %w", err)
		}

		caPEM, err := base64.StdEncoding.DecodeString(cl.MasterAuth.ClusterCaCertificate)
		if err != nil {
			return nil, fmt.Errorf("decode cluster CA: %w", err)
		}

		// 3) Build rest.Config and wrap transport with OAuth2 so tokens auto-refresh.
		cfg := &rest.Config{
			Host: fmt.Sprintf("https://%s", cl.Endpoint),
			TLSClientConfig: rest.TLSClientConfig{
				CAData: caPEM,
			},
			Timeout:   30 * time.Second,
			UserAgent: "velda-apiserver/1.0",
		}

		// Inject OAuth2 bearer tokens (auto-refreshing) into all kube API calls.
		cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
			return &oauth2.Transport{
				Base:   rt,
				Source: creds.TokenSource,
			}
		}
		return cfg, nil
	default:
		config, err := rest.InClusterConfig()
		if err != nil {
			homeDir, errh := os.UserHomeDir()
			if errh != nil {
				return nil, errh
			}

			// Load Kubernetes configuration (e.g., from ~/.kube/config)
			kubeconfig := filepath.Join(homeDir, ".kube", "config")

			config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		}
		if err != nil {
			return nil, err
		}
		return config, nil
	}
}

func (*K8sProvisionerFactory) NewProvisioner(cfg *configpb.Provisioner, schedulers *broker.SchedulerSet) (backends.Provisioner, error) {
	config, err := getClusterConfig(cfg.GetKubernetes())
	if err != nil {
		return nil, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Define GVR for the custom resource
	gvr := schema.GroupVersionResource{
		Group:    "velda.io",
		Version:  "v1",
		Resource: "agentpools",
	}

	return &K8sProvisioner{
		client:       client.Resource(gvr).Namespace(cfg.GetKubernetes().Namespace),
		cfg:          config,
		schedulerSet: schedulers,
	}, nil
}

func (*K8sProvisionerFactory) CanHandle(cfg *configpb.Provisioner) bool {
	return cfg.GetKubernetes() != nil
}

func init() {
	backends.RegisterProvisioner(&K8sProvisionerFactory{})
}
