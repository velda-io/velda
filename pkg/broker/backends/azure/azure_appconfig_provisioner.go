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
package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azappconfig"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	configpb "velda.io/velda/pkg/proto/config"
)

type AzureAppConfigPoolProvisioner struct {
	schedulerSet    *broker.SchedulerSet
	lastSeenVersion map[string]time.Time
	lastSeenTime    map[string]time.Time
	cfg             *configpb.AzureProvisioner
	client          *azappconfig.Client
	brokerInfo      *agentpb.BrokerInfo
}

func (p *AzureAppConfigPoolProvisioner) Run(ctx context.Context) {
	p.lastSeenVersion = make(map[string]time.Time)
	p.lastSeenTime = make(map[string]time.Time)

	interval := p.cfg.UpdateInterval.AsDuration()
	if interval == 0 {
		interval = 60 * time.Second
	}

	ticker := time.NewTicker(interval)
	updateLoop := func(t time.Time) {
		// Ensure prefix ends with a slash and list all settings under it (e.g. "pool/*").
		prefix := p.cfg.ConfigPrefix
		if !strings.HasSuffix(prefix, "/") {
			prefix = prefix + "/"
		}
		keyFilter := prefix + "*"
		selector := azappconfig.SettingSelector{
			KeyFilter: &keyFilter,
		}

		pager := p.client.NewListSettingsPager(selector, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				log.Printf("Failed to query Azure App Configuration: %v", err)
				break
			}

			for _, setting := range page.Settings {
				if setting.Key == nil || setting.Value == nil {
					continue
				}

				key := *setting.Key
				lastModified := time.Time{}
				if setting.LastModified != nil {
					lastModified = *setting.LastModified
				}

				lastVersion, ok := p.lastSeenVersion[key]
				if !ok || lastVersion.Before(lastModified) {
					err := p.update(ctx, key, *setting.Value, !ok)
					if err != nil {
						log.Printf("Failed to update pool %s: %v", key, err)
					}
					p.lastSeenVersion[key] = lastModified
				}
				p.lastSeenTime[key] = t
			}
		}

		for k, v := range p.lastSeenTime {
			if v != t {
				// TODO: Remove pool k
				log.Printf("Pool %s not seen in last update", k)
				delete(p.lastSeenVersion, k)
				delete(p.lastSeenTime, k)
			}
		}
	}
	updateLoop(time.Now())

	go func() {
		defer ticker.Stop()
		for {
			select {
			case t := <-ticker.C:
				updateLoop(t)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (p *AzureAppConfigPoolProvisioner) update(ctx context.Context, key string, value string, new bool) error {
	obj := &configpb.AgentPool{}
	var yamlObj map[string]interface{}
	if err := yaml.Unmarshal([]byte(value), &yamlObj); err != nil {
		return fmt.Errorf("failed to unmarshal yaml: %w", err)
	}
	jsonData, err := json.Marshal(yamlObj)
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	if err := protojson.Unmarshal(jsonData, obj); err != nil {
		return fmt.Errorf("failed to unmarshal protojson: %w", err)
	}
	expectedKey := p.cfg.ConfigPrefix
	if !strings.HasSuffix(expectedKey, "/") {
		expectedKey = expectedKey + "/"
	}
	expectedKey = expectedKey + obj.Name
	if expectedKey != key {
		return fmt.Errorf("Name mismatch: %s != %s", expectedKey, key)
	}
	pool, err := p.schedulerSet.GetOrCreatePool(obj.Name)
	if err != nil {
		return err
	}
	autoscalerConfig, err := backends.AutoScaledConfigFromConfig(ctx, obj, p.brokerInfo)
	if err != nil {
		return err
	}
	if new && obj.AutoScaler.GetInitialDelay() != nil {
		time.AfterFunc(obj.AutoScaler.GetInitialDelay().AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
	}
	log.Printf("Updating pool from Azure App Configuration: %v", obj.Name)
	pool.PoolManager.UpdateConfig(autoscalerConfig)
	return nil
}

type AzurePoolProvisionerFactory struct{}

func (*AzurePoolProvisionerFactory) NewProvisioner(cfg *configpb.Provisioner, schedulers *broker.SchedulerSet, brokerInfo *agentpb.BrokerInfo) (backends.Provisioner, error) {
	cfgAzure := cfg.GetAzure()
	if cfgAzure == nil {
		return nil, fmt.Errorf("azure provisioner config is nil")
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	client, err := azappconfig.NewClient(cfgAzure.Endpoint, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure App Configuration client: %w", err)
	}

	return &AzureAppConfigPoolProvisioner{
		schedulerSet: schedulers,
		client:       client,
		cfg:          cfgAzure,
		brokerInfo:   brokerInfo,
	}, nil
}

func (*AzurePoolProvisionerFactory) CanHandle(cfg *configpb.Provisioner) bool {
	return cfg.GetAzure() != nil
}

func init() {
	backends.RegisterProvisioner(&AzurePoolProvisionerFactory{})
}
