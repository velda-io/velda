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
	"os"
	"testing"

	"velda.io/velda/pkg/broker/backends/backend_testing"
	agentpb "velda.io/velda/pkg/proto/agent"
	cfgpb "velda.io/velda/pkg/proto/config"
)

// To run test (requires Azure credentials available via DefaultAzureCredential):
// AZURE_SUBSCRIPTION_ID=<id> AZURE_RESOURCE_GROUP=<rg> AZURE_VMSS_NAME=<vmss> go test --tags azure -v ./pkg/broker/backends/azure

func TestAzureBackend(t *testing.T) {
	sub := os.Getenv("AZURE_SUBSCRIPTION_ID")
	if sub == "" {
		t.Skip("AZURE_SUBSCRIPTION_ID not set, skipping test")
	}

	rg := os.Getenv("AZURE_RESOURCE_GROUP")
	if rg == "" {
		t.Skip("AZURE_RESOURCE_GROUP not set, skipping test")
	}

	vmss := os.Getenv("AZURE_VMSS_NAME")
	if vmss == "" {
		t.Skip("AZURE_VMSS_NAME not set, skipping test")
	}

	configpb := &cfgpb.AutoscalerBackend{
		Backend: &cfgpb.AutoscalerBackend_AzureVmss{
			AzureVmss: &cfgpb.AutoscalerBackendAzureVMSS{
				SubscriptionId: sub,
				ResourceGroup:  rg,
				VmssName:       vmss,
				// Location and other fields may be optional for the test
			},
		},
	}

	poolPb := &cfgpb.AgentPool{
		Name:       "azure-test-pool",
		AutoScaler: &cfgpb.AgentPool_AutoScaler{Backend: configpb},
	}

	factory := &azureVMSSPoolFactory{}
	brokerInfo := &agentpb.BrokerInfo{Address: "localhost:50051"}
	backend, err := factory.NewBackend(poolPb, brokerInfo)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}

	// Ensure ListWorkers can be called without error
	backend.ListWorkers(context.Background())

	backend_testing.TestSimpleScaleUpDown(t, backend)
}
