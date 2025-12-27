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
package digitalocean

import (
	"context"
	"fmt"
	"log"
	"time"

	pb "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	proto "velda.io/velda/pkg/proto/config"
)

type DigitalOceanAutoPoolProvisioner struct {
	schedulerSet *broker.SchedulerSet
	cfg          *proto.DigitalOceanAutoProvisioner
	brokerInfo   *agentpb.BrokerInfo
}

func (p *DigitalOceanAutoPoolProvisioner) Run(ctx context.Context) {
	err := p.run(ctx)
	if err != nil {
		log.Panicf("Failed to run DigitalOcean auto provisioner: %v", err)
	}
}

func (p *DigitalOceanAutoPoolProvisioner) run(ctx context.Context) error {
	if p.brokerInfo == nil {
		return fmt.Errorf("no default broker info public endpoint")
	}

	// Iterate through each pool detail and create a pool
	for _, detail := range p.cfg.PoolDetails {
		if detail.PoolName == "" {
			log.Printf("Skipping pool detail with empty pool name")
			continue
		}

		// Create the droplet template from common config and detail
		template := &proto.AutoscalerBackendDigitalOceanDroplet{
			Size:                 detail.Size,
			Image:                detail.Image,
			Region:               p.cfg.Region,
			ApiToken:             p.cfg.ApiToken,
			ApiEndpoint:          p.cfg.ApiEndpoint,
			SshKeyIds:            p.cfg.SshKeyIds,
			AgentVersionOverride: p.cfg.AgentVersionOverride,
			TailscaleConfig:      p.cfg.GetTailscaleConfig(),
			Backups:              detail.GetBackups(),
			Ipv6:                 detail.GetIpv6(),
			Monitoring:           detail.GetMonitoring(),
			VpcUuid:              detail.GetVpcUuid(),
			AgentConfig:          &agentpb.AgentConfig{},
		}

		// Copy labels from common config
		if p.cfg.Labels != nil {
			template.Labels = make(map[string]string)
			for k, v := range p.cfg.Labels {
				template.Labels[k] = v
			}
		}

		// Add pool-specific label
		if template.Labels == nil {
			template.Labels = make(map[string]string)
		}
		template.Labels["velda/pool"] = detail.PoolName

		// Determine which autoscaler config to use
		autoScaler := detail.AutoscalerConfig
		if autoScaler == nil {
			autoScaler = p.cfg.AutoscalerConfig
		}
		if autoScaler == nil {
			// Use default
			autoScaler = &proto.AgentPool_AutoScaler{
				MaxAgents:     10,
				MinAgents:     0,
				MaxIdleAgents: 1,
				IdleDecay:     durationpb.New(1 * time.Minute),
			}
		}
		// Clone the autoscaler config and set the backend
		autoScaler = pb.Clone(autoScaler).(*proto.AgentPool_AutoScaler)
		if detail.Description != "" {
			if autoScaler.Metadata == nil {
				autoScaler.Metadata = &proto.PoolMetadata{}
			}
			autoScaler.Metadata.Description = detail.Description
		}

		autoScaler.Backend = &proto.AutoscalerBackend{
			Backend: &proto.AutoscalerBackend_DigitaloceanDroplet{
				DigitaloceanDroplet: template,
			},
		}

		newPoolConfig := &proto.AgentPool{
			Name:       detail.PoolName,
			AutoScaler: autoScaler,
		}

		pool, err := p.schedulerSet.GetOrCreatePool(detail.PoolName)
		if err != nil {
			return err
		}

		autoscalerConfig, err := backends.AutoScaledConfigFromConfig(ctx, newPoolConfig, p.brokerInfo)
		if err != nil {
			return err
		}

		log.Printf("Created DigitalOcean auto-provisioned pool: %v (size: %s, region: %s, image: %s)",
			newPoolConfig.Name, detail.Size, p.cfg.Region, detail.Image)
		pool.PoolManager.UpdateConfig(autoscalerConfig)

		if newPoolConfig.AutoScaler.GetInitialDelay() != nil {
			time.AfterFunc(newPoolConfig.AutoScaler.GetInitialDelay().AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
		} else {
			pool.PoolManager.ReadyForIdleMaintenance()
		}
	}

	return nil
}

type DigitalOceanAutoProvisionerFactory struct{}

func (*DigitalOceanAutoProvisionerFactory) NewProvisioner(cfg *proto.Provisioner, schedulers *broker.SchedulerSet, brokerInfo *agentpb.BrokerInfo) (backends.Provisioner, error) {
	cfgDO := cfg.GetDigitaloceanAuto()
	return &DigitalOceanAutoPoolProvisioner{
		schedulerSet: schedulers,
		cfg:          cfgDO,
		brokerInfo:   brokerInfo,
	}, nil
}

func (*DigitalOceanAutoProvisionerFactory) CanHandle(cfg *proto.Provisioner) bool {
	return cfg.GetDigitaloceanAuto() != nil
}

func init() {
	backends.RegisterProvisioner(&DigitalOceanAutoProvisionerFactory{})
}
