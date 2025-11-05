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
package nebius

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

type NebiusAutoPoolProvisioner struct {
	schedulerSet *broker.SchedulerSet
	cfg          *proto.NebiusAutoProvisioner
	brokerInfo   *agentpb.BrokerInfo
}

func (p *NebiusAutoPoolProvisioner) Run(ctx context.Context) {
	err := p.run(ctx)
	if err != nil {
		log.Panicf("Failed to run Nebius auto provisioner: %v", err)
	}
}

func (p *NebiusAutoPoolProvisioner) run(ctx context.Context) error {
	if p.brokerInfo == nil {
		return fmt.Errorf("no default broker info public endpoint")
	}

	// Iterate through each pool detail and create a pool
	for _, detail := range p.cfg.PoolDetails {
		if detail.PoolName == "" {
			log.Printf("Skipping pool detail with empty pool name")
			continue
		}

		// Create the launch template from common config and detail
		template := &proto.AutoscalerBackendNebiusLaunchTemplate{
			ParentId:             p.cfg.ParentId,
			InstanceNamePrefix:   fmt.Sprintf("velda-%s", detail.PoolName),
			Platform:             detail.Platform,
			ResourcePreset:       detail.ResourcePreset,
			SubnetId:             p.cfg.SubnetId,
			BootDiskSizeGb:       p.cfg.BootDiskSizeGb,
			Preemptible:          detail.Preemptible,
			UsePublicIp:          p.cfg.UsePublicIp,
			AdminSshKey:          p.cfg.AdminSshKey,
			AgentVersionOverride: p.cfg.AgentVersionOverride,
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
		autoScaler.Backend = &proto.AutoscalerBackend{
			Backend: &proto.AutoscalerBackend_NebiusLaunchTemplate{
				NebiusLaunchTemplate: template,
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

		log.Printf("Created Nebius auto-provisioned pool: %v", newPoolConfig.Name)
		pool.PoolManager.UpdateConfig(autoscalerConfig)

		if newPoolConfig.AutoScaler.GetInitialDelay() != nil {
			time.AfterFunc(newPoolConfig.AutoScaler.GetInitialDelay().AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
		} else {
			pool.PoolManager.ReadyForIdleMaintenance()
		}
	}

	return nil
}

type NebiusAutoProvisionerFactory struct{}

func (*NebiusAutoProvisionerFactory) NewProvisioner(cfg *proto.Provisioner, schedulers *broker.SchedulerSet, brokerInfo *agentpb.BrokerInfo) (backends.Provisioner, error) {
	cfgNebius := cfg.GetNebiusAuto()
	return &NebiusAutoPoolProvisioner{
		schedulerSet: schedulers,
		cfg:          cfgNebius,
		brokerInfo:   brokerInfo,
	}, nil
}

func (*NebiusAutoProvisionerFactory) CanHandle(cfg *proto.Provisioner) bool {
	return cfg.GetNebiusAuto() != nil
}

func init() {
	backends.RegisterProvisioner(&NebiusAutoProvisionerFactory{})
}
