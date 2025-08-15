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
package backends

import (
	"context"
	"fmt"

	"velda.io/velda/pkg/broker"
	proto "velda.io/velda/pkg/proto/config"
)

type factory interface {
	CanHandle(pb *proto.AutoscalerBackend) bool
	NewBackend(pb *proto.AutoscalerBackend) (broker.ResourcePoolBackend, error)
}

type Provisioner interface {
	Run(ctx context.Context)
}

type provisionerFactory interface {
	CanHandle(pb *proto.Provisioner) bool
	NewProvisioner(pb *proto.Provisioner, schedulers *broker.SchedulerSet) (Provisioner, error)
}

var handlers []factory

var provisioners []provisionerFactory

func Register(f factory) {
	handlers = append(handlers, f)
}

func RegisterProvisioner(f provisionerFactory) {
	provisioners = append(provisioners, f)
}

func NewBackend(pb *proto.AutoscalerBackend) (broker.ResourcePoolBackend, error) {
	for _, h := range handlers {
		if h.CanHandle(pb) {
			return h.NewBackend(pb)
		}
	}
	return nil, fmt.Errorf("no backend found for %v", pb)
}

func NewProvisioner(pb *proto.Provisioner, schedulerSet *broker.SchedulerSet) (Provisioner, error) {
	for _, p := range provisioners {
		if p.CanHandle(pb) {
			return p.NewProvisioner(pb, schedulerSet)
		}
	}
	return nil, fmt.Errorf("no provisioner found for %v", pb)
}

func AutoScaledConfigFromConfig(ctx context.Context, cfg *proto.AgentPool) (*broker.AutoScaledPoolConfig, error) {
	backend, err := NewBackend(cfg.AutoScaler.Backend)
	if err != nil {
		return nil, err
	}
	return AutoScaledConfigFromBackend(ctx, backend, cfg.AutoScaler), nil
}

func AutoScaledConfigFromBackend(ctx context.Context, backend broker.ResourcePoolBackend, autoScalerCfg *proto.AgentPool_AutoScaler) *broker.AutoScaledPoolConfig {
	return &broker.AutoScaledPoolConfig{
		Context:              ctx,
		Backend:              backend,
		MinIdle:              int(autoScalerCfg.MinIdleAgents),
		MaxIdle:              int(autoScalerCfg.MaxIdleAgents),
		IdleDecay:            autoScalerCfg.IdleDecay.AsDuration(),
		MaxSize:              int(autoScalerCfg.MaxAgents),
		SyncLoopInterval:     autoScalerCfg.SyncLoopInterval.AsDuration(),
		KillUnknownAfter:     autoScalerCfg.KillUnknownAfter.AsDuration(),
		DefaultSlotsPerAgent: int(autoScalerCfg.DefaultSlotsPerAgent),
	}
}
