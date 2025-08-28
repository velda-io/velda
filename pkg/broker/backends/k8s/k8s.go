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
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	corev1c "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

type k8sPoolBackend struct {
	cfg         *rest.Config
	podTemplate *corev1.Pod
	podSelector string
	client      *corev1c.CoreV1Client
}

func NewK8sPoolBackend(cfg *rest.Config, podTemplate *corev1.Pod, podSelector string) (broker.ResourcePoolBackend, error) {
	client, err := corev1c.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}
	return &k8sPoolBackend{
		cfg:         cfg,
		podTemplate: podTemplate,
		podSelector: podSelector,
		client:      client,
	}, nil
}

func (k *k8sPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	pod := k.podTemplate.DeepCopy()
	pod.Name = fmt.Sprintf("%s-%s", k.podTemplate.Name, utils.RandString(5))

	_, err := k.client.Pods(k.podTemplate.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return pod.Name, nil
}

func (k *k8sPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	err := k.client.Pods(k.podTemplate.Namespace).Delete(ctx, workerName, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}

func (k *k8sPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	pods, err := k.client.Pods(k.podTemplate.Namespace).List(ctx, metav1.ListOptions{LabelSelector: k.podSelector})
	if err != nil {
		return nil, err
	}
	workers := make([]broker.WorkerStatus, 0)
	for _, p := range pods.Items {
		workers = append(workers, broker.WorkerStatus{
			Name: p.Name,
			// IP:   p.Status.PodIP,
		})
	}
	return workers, nil
}

type k8sPoolFactory struct{}

func (f *k8sPoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_Kubernetes:
		return true
	}
	return false
}

func (f *k8sPoolFactory) NewBackend(pb *proto.AutoscalerBackend) (broker.ResourcePoolBackend, error) {
	cfg := pb.GetKubernetes()
	kubeconfig := cfg.GetKubeconfig()
	if kubeconfig == "" {
		// TODO: Get in-cluster config
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}

		// Load Kubernetes configuration (e.g., from ~/.kube/config)
		kubeconfig = filepath.Join(homeDir, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	podTemplate, err := os.Open(cfg.GetPodTemplatePath())
	if err != nil {
		return nil, fmt.Errorf("failed to open pod template: %w", err)
	}

	decoder := yaml.NewYAMLOrJSONDecoder(podTemplate, 100)
	pod := &corev1.Pod{}
	err = decoder.Decode(pod)
	if err != nil {
		return nil, fmt.Errorf("failed to decode pod template: %w", err)
	}

	return NewK8sPoolBackend(config, pod, cfg.PodSelector)
}

func init() {
	backends.Register(&k8sPoolFactory{})
}
