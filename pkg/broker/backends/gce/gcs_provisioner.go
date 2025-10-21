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
package gce

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	configpb "velda.io/velda/pkg/proto/config"
)

type GcsPoolProvisioner struct {
	schedulerSet    *broker.SchedulerSet
	storageClient   *storage.Client
	lastSeenVersion map[string]time.Time
	lastSeenTime    map[string]time.Time
	cfg             *configpb.GCSProvisioner
	brokerInfo      *agentpb.BrokerInfo
}

func (p *GcsPoolProvisioner) Run(ctx context.Context) {
	p.lastSeenVersion = make(map[string]time.Time)
	p.lastSeenTime = make(map[string]time.Time)

	interval := p.cfg.UpdateInterval.AsDuration()
	if interval == 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	updateLoop := func(t time.Time) {
		it := p.storageClient.Bucket(p.cfg.Bucket).Objects(ctx, &storage.Query{
			Prefix: p.cfg.ConfigPrefix + "/",
		})

		for {
			attr, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				log.Printf("Failed to query GCS objects: %v", err)
				break
			}

			lastVersion, ok := p.lastSeenVersion[attr.Name]
			if !ok || lastVersion.Before(attr.Updated) {
				err := p.update(ctx, attr, !ok)
				if err != nil {
					log.Printf("Failed to update pool %s: %v", attr.Name, err)
				}
				p.lastSeenVersion[attr.Name] = attr.Updated
			}
			p.lastSeenTime[attr.Name] = t
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

func (p *GcsPoolProvisioner) update(ctx context.Context, attr *storage.ObjectAttrs, new bool) error {
	rc, err := p.storageClient.Bucket(attr.Bucket).Object(attr.Name).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("failed to create reader: %w", err)
	}
	defer rc.Close()

	var yamlObj map[string]interface{}
	if err := yaml.NewDecoder(rc).Decode(&yamlObj); err != nil {
		return fmt.Errorf("failed to unmarshal yaml: %w", err)
	}
	jsonData, err := json.Marshal(yamlObj)
	if err != nil {
		return fmt.Errorf("failed to marshal json: %w", err)
	}
	obj := &configpb.AgentPool{}
	if err := protojson.Unmarshal(jsonData, obj); err != nil {
		return fmt.Errorf("failed to unmarshal protojson: %w", err)
	}
	if p.cfg.ConfigPrefix+"/"+obj.Name != attr.Name {
		return fmt.Errorf("Name mismatch: %s != %s", p.cfg.ConfigPrefix+"/"+obj.Name, attr.Name)
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
	log.Printf("Updating pool from GCS: %v", obj.Name)
	pool.PoolManager.UpdateConfig(autoscalerConfig)
	return nil
}

type GcsPoolProvisionerFactory struct{}

func (*GcsPoolProvisionerFactory) NewProvisioner(cfg *configpb.Provisioner, schedulers *broker.SchedulerSet, brokerInfo *agentpb.BrokerInfo) (backends.Provisioner, error) {
	cfgGcs := cfg.GetGcs()
	client, err := storage.NewClient(context.Background())
	if err != nil {
		return nil, err
	}
	return &GcsPoolProvisioner{
		schedulerSet:  schedulers,
		storageClient: client,
		cfg:           cfgGcs,
	}, nil
}

func (*GcsPoolProvisionerFactory) CanHandle(cfg *configpb.Provisioner) bool {
	return cfg.GetGcs() != nil
}

func init() {
	backends.RegisterProvisioner(&GcsPoolProvisionerFactory{})
}
