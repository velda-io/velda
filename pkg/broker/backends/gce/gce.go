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
	"fmt"

	"google.golang.org/api/compute/v1"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

type gcePoolBackend struct {
	project            string
	zone               string
	instanceGroup      string
	instanceNamePrefix string
	igms               *compute.InstanceGroupManagersService
	ops                *compute.ZoneOperationsService
	lastOp             *compute.Operation
}

func NewGCEPoolBackend(project, zone, instanceGroup, instanceNamePrefix string,
	svc *compute.Service) broker.ResourcePoolBackend {
	if instanceNamePrefix == "" {
		instanceNamePrefix = instanceGroup + "-"
	}
	return &gcePoolBackend{
		project:            project,
		zone:               zone,
		instanceGroup:      instanceGroup,
		instanceNamePrefix: instanceNamePrefix,
		igms:               compute.NewInstanceGroupManagersService(svc),
		ops:                compute.NewZoneOperationsService(svc),
	}
}

func (g *gcePoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	nameSuffix := utils.RandString(5)
	name := fmt.Sprintf("%s%s", g.instanceNamePrefix, nameSuffix)
	req := &compute.InstanceGroupManagersCreateInstancesRequest{
		Instances: []*compute.PerInstanceConfig{{Name: name}},
	}
	op, err := g.igms.CreateInstances(g.project, g.zone, g.instanceGroup, req).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	g.lastOp = op
	return name, nil
}

func (g *gcePoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	fullInstancePath := fmt.Sprintf("projects/%s/zones/%s/instances/%s", g.project, g.zone, workerName)
	req := &compute.InstanceGroupManagersDeleteInstancesRequest{
		Instances: []string{fullInstancePath},
	}
	op, err := g.igms.DeleteInstances(g.project, g.zone, g.instanceGroup, req).Context(ctx).Do()
	if err != nil {
		return err
	}
	g.lastOp = op
	return nil
}

func (g *gcePoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	instances, err := g.igms.ListManagedInstances(g.project, g.zone, g.instanceGroup).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	var res []broker.WorkerStatus
	for _, inst := range instances.ManagedInstances {
		if inst.CurrentAction == "DELETING" || inst.CurrentAction == "STOPPING" {
			continue
		}
		res = append(res, broker.WorkerStatus{
			Name: inst.Name,
		})
	}
	return res, nil
}

func (g *gcePoolBackend) WaitForLastOperation(ctx context.Context) error {
	if g.lastOp == nil {
		return nil
	}
	op := g.lastOp
	g.lastOp = nil
	op, err := g.ops.Wait(g.project, g.zone, op.Name).Context(ctx).Do()
	if err != nil {
		return err
	}
	if op.Status == "DONE" {
		return nil
	}
	return fmt.Errorf("operation %s failed with status %s", op.Name, op.Status)
}

type gcePoolFactory struct{}

func (f *gcePoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_GceInstanceGroup:
		return true
	}
	return false
}

func (f *gcePoolFactory) NewBackend(pool *proto.AgentPool, brokerInfo *agentpb.BrokerInfo) (broker.ResourcePoolBackend, error) {
	gce := pool.GetAutoScaler().GetBackend().GetGceInstanceGroup()
	prefix := gce.InstanceNamePrefix
	if prefix == "" {
		prefix = gce.InstanceGroup
	}
	svc, err := compute.NewService(context.Background())
	if err != nil {
		return nil, err
	}
	return NewGCEPoolBackend(gce.Project, gce.Zone, gce.InstanceGroup, gce.InstanceNamePrefix, svc), nil
}

func init() {
	backends.Register(&gcePoolFactory{})
}
