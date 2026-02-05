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
	"fmt"
	"log"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	proto "velda.io/velda/pkg/proto/config"
)

type azurePoolBackend struct {
	cfg            *proto.AutoscalerBackendAzureVMSS
	vmssClient     *armcompute.VirtualMachineScaleSetsClient
	vmssVMClient   *armcompute.VirtualMachineScaleSetVMsClient
	subscriptionID string
	resourceGroup  string
	vmssName       string
	mu             sync.Mutex
	capacity       int64
	capacityInit   bool

	lastOp func()
}

func NewAzurePoolBackend(cfg *proto.AutoscalerBackendAzureVMSS, cred *azidentity.DefaultAzureCredential) (broker.ResourcePoolBackend, error) {
	subscriptionID := cfg.GetSubscriptionId()
	resourceGroup := cfg.GetResourceGroup()
	vmssName := cfg.GetVmssName()

	vmssClient, err := armcompute.NewVirtualMachineScaleSetsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create VMSS client: %w", err)
	}

	vmssVMClient, err := armcompute.NewVirtualMachineScaleSetVMsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create VMSS VM client: %w", err)
	}

	return &azurePoolBackend{
		cfg:            cfg,
		vmssClient:     vmssClient,
		vmssVMClient:   vmssVMClient,
		subscriptionID: subscriptionID,
		resourceGroup:  resourceGroup,
		vmssName:       vmssName,
	}, nil
}

func (a *azurePoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	if err := a.prefetchCapacity(ctx); err != nil {
		return "", fmt.Errorf("failed to prefetch capacity: %w", err)
	}

	a.mu.Lock()
	currentCapacity := a.capacity
	newCapacity := currentCapacity + 1
	a.capacity = newCapacity
	a.mu.Unlock()

	// Perform scale operation against Azure
	log.Printf("Scaling up Azure VMSS %s from %d to %d instances", a.vmssName, currentCapacity, newCapacity)

	sku := armcompute.SKU{}
	sku.Capacity = to.Ptr(newCapacity)
	pollerResp, err := a.vmssClient.BeginUpdate(ctx, a.resourceGroup, a.vmssName, armcompute.VirtualMachineScaleSetUpdate{
		SKU: &sku,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to scale up VMSS: %w", err)
	}

	wait := func() {
		_, err := pollerResp.PollUntilDone(ctx, nil)
		if err != nil {
			log.Printf("Error waiting for scale up to complete: %v", err)
			a.mu.Lock()
			a.capacityInit = false // Force re-fetch on next operation
			a.mu.Unlock()
			return
		}
	}

	a.lastOp = wait
	go wait()
	return "", nil
}

func (a *azurePoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	// Use targeted scale-in to delete specific instance
	pollerResp, err := a.vmssClient.BeginDeleteInstances(ctx, a.resourceGroup, a.vmssName, armcompute.VirtualMachineScaleSetVMInstanceRequiredIDs{
		InstanceIDs: []*string{to.Ptr(workerName)},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	wait := func() {
		_, err = pollerResp.PollUntilDone(ctx, nil)
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil {
			log.Printf("Error waiting for deletion to complete: %v", err)
			a.capacityInit = false // Force re-fetch on next operation
			return
		} else {
			a.capacity--
		}
	}

	a.lastOp = wait
	go wait()
	return nil
}

func (a *azurePoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	pager := a.vmssVMClient.NewListPager(a.resourceGroup, a.vmssName, nil)

	var res []broker.WorkerStatus
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list VMSS instances: %w", err)
		}

		for _, vm := range page.Value {
			if vm.InstanceID == nil {
				continue
			}
			res = append(res, broker.WorkerStatus{
				Name: *vm.InstanceID,
			})
		}
	}
	log.Printf("Listed %d workers in Azure VMSS %s: %v", len(res), a.vmssName, res)

	return res, nil
}

func (a *azurePoolBackend) WaitForLastOperation(ctx context.Context) error {
	if a.lastOp != nil {
		a.lastOp()
	}
	return nil
}

func (a *azurePoolBackend) prefetchCapacity(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.capacityInit {
		return nil
	}
	vmss, err := a.vmssClient.Get(ctx, a.resourceGroup, a.vmssName, nil)
	if err != nil {
		return fmt.Errorf("failed to get VMSS: %w", err)
	}

	cur := int64(0)
	if vmss.SKU != nil && vmss.SKU.Capacity != nil {
		cur = *vmss.SKU.Capacity
	}
	a.capacity = cur
	a.capacityInit = true
	return nil
}

type azureVMSSPoolFactory struct{}

func (f *azureVMSSPoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_AzureVmss:
		return true
	}
	return false
}

func (f *azureVMSSPoolFactory) NewBackend(pool *proto.AgentPool, brokerInfo *agentpb.BrokerInfo) (broker.ResourcePoolBackend, error) {
	azureConfig := pool.GetAutoScaler().GetBackend().GetAzureVmss()
	// VMSS creation and low-level config (image, size, networking, tags, etc.)
	// should be managed by Terraform or other infrastructure templates.
	// The controller only needs identifiers to reference the VMSS.

	// Create Azure credentials using DefaultAzureCredential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credentials: %w", err)
	}

	return NewAzurePoolBackend(azureConfig, cred)
}

func init() {
	backends.Register(&azureVMSSPoolFactory{})
}
