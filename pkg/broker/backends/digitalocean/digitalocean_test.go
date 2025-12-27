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
	"os"
	"testing"

	"velda.io/velda/pkg/broker/backends/backend_testing"
	agentpb "velda.io/velda/pkg/proto/agent"
	cfgpb "velda.io/velda/pkg/proto/config"
)

// To run test:
// DIGITALOCEAN_API_TOKEN=<token> DIGITALOCEAN_SSH_KEY_ID=<ssh-key-id> go test --tags digitalocean -v ./pkg/broker/backends/digitalocean

func TestDigitalOceanBackend(t *testing.T) {
	apiToken := os.Getenv("DIGITALOCEAN_API_TOKEN")
	if apiToken == "" {
		t.Skip("DIGITALOCEAN_API_TOKEN not set, skipping test")
	}

	sshKeyIDStr := os.Getenv("DIGITALOCEAN_SSH_KEY_ID")
	if sshKeyIDStr == "" {
		t.Skip("DIGITALOCEAN_SSH_KEY_ID not set, skipping test")
	}

	// Convert SSH key ID from string to int32
	var sshKeyID int32
	_, err := fmt.Sscanf(sshKeyIDStr, "%d", &sshKeyID)
	if err != nil {
		t.Fatalf("Invalid DIGITALOCEAN_SSH_KEY_ID: %v", err)
	}

	configpb := &cfgpb.AutoscalerBackend{
		Backend: &cfgpb.AutoscalerBackend_DigitaloceanDroplet{
			DigitaloceanDroplet: &cfgpb.AutoscalerBackendDigitalOceanDroplet{
				Size:        "gpu-mi300x1-192gb-devcloud",
				Region:      "atl1",
				Image:       "amd-rocm71software",
				ApiToken:    apiToken,
				SshKeyIds:   []int32{sshKeyID},
				AgentConfig: &agentpb.AgentConfig{},
				Labels: map[string]string{
					"velda-test": "velda",
				},
			},
		},
	}

	poolPb := &cfgpb.AgentPool{
		Name:       "digitalocean-gpu",
		AutoScaler: &cfgpb.AgentPool_AutoScaler{Backend: configpb},
	}

	factory := &digitaloceanDropletPoolFactory{}
	brokerInfo := &agentpb.BrokerInfo{
		Address: "localhost:50051",
	}
	backend, err := factory.NewBackend(poolPb, brokerInfo)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}

	backend.ListWorkers(context.Background())

	backend_testing.TestSimpleScaleUpDown(t, backend)
}
