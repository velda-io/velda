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
	"os"
	"testing"

	"velda.io/velda/pkg/broker/backends/backend_testing"
	agentpb "velda.io/velda/pkg/proto/agent"
	cfgpb "velda.io/velda/pkg/proto/config"
)

// To run test:
// NEBIUS_BACKEND=<folder-id>/<instance-template-name> NEBIUS_IAM_TOKEN=<token> go test --tags nebius -v ./pkg/broker/backends/nebius

func TestNebiusBackend(t *testing.T) {
	parentID := os.Getenv("NEBIUS_PARENT_ID")
	subnetID := os.Getenv("NEBIUS_SUBNET_ID")
	if parentID == "" || subnetID == "" {
		t.Skip("NEBIUS_PARENT_ID or NEBIUS_SUBNET_ID not set; skipping integration test")
	}
	configpb := &cfgpb.AutoscalerBackend{
		Backend: &cfgpb.AutoscalerBackend_NebiusLaunchTemplate{
			NebiusLaunchTemplate: &cfgpb.AutoscalerBackendNebiusLaunchTemplate{
				ParentId:       parentID,
				Platform:       "cpu-d3",
				ResourcePreset: "4vcpu-16gb",
				//Platform:           "gpu-h200-sxm",
				//ResourcePreset:     "1gpu-16vcpu-200gb",
				SubnetId:           subnetID,
				InstanceNamePrefix: "velda-agent",
				AgentConfig:        &agentpb.AgentConfig{},
				MaxDiskPoolSize:    1,
				Labels: map[string]string{
					"velda-test": "velda",
				},
			},
		},
	}

	poolPb := &cfgpb.AgentPool{
		Name:       "cpu-4",
		AutoScaler: &cfgpb.AgentPool_AutoScaler{Backend: configpb},
	}

	factory := &nebiusLaunchTemplatePoolFactory{}
	brokerInfo := &agentpb.BrokerInfo{
		Address: "172.31.30.15:50051",
	}
	backend, err := factory.NewBackend(poolPb, brokerInfo)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	backend.ListWorkers(context.Background())

	backend_testing.TestSimpleScaleUpDown(t, backend)
}
