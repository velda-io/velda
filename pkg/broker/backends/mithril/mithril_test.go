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
package mithril

import (
	"context"
	"os"
	"testing"

	"velda.io/velda/pkg/broker/backends/backend_testing"
	agentpb "velda.io/velda/pkg/proto/agent"
	cfgpb "velda.io/velda/pkg/proto/config"
)

// To run test:
// MITHRIL_API_TOKEN=<token> MITHRIL_PROJECT_ID=<project-id> go test --tags mithril -v ./pkg/broker/backends/mithril

func TestMithrilBackend(t *testing.T) {
	apiToken := os.Getenv("MITHRIL_API_TOKEN")
	if apiToken == "" {
		t.Skip("MITHRIL_API_TOKEN not set, skipping test")
	}

	projectID := os.Getenv("MITHRIL_PROJECT_ID")
	if projectID == "" {
		t.Skip("MITHRIL_PROJECT_ID not set, skipping test")
	}

	configpb := &cfgpb.AutoscalerBackend{
		Backend: &cfgpb.AutoscalerBackend_MithrilSpotBid{
			MithrilSpotBid: &cfgpb.AutoscalerBackendMithrilSpotBid{
				InstanceType: "it_RrgkIZz6c9BZu5gi",
				Region:       "us-central3-a",
				LimitPrice:   0.14,
				ProjectId:    projectID,
				ApiToken:     apiToken,
				AgentConfig:  &agentpb.AgentConfig{},
				Labels: map[string]string{
					"velda-test": "velda",
				},
			},
		},
	}

	poolPb := &cfgpb.AgentPool{
		Name:       "mithril-a100",
		AutoScaler: &cfgpb.AgentPool_AutoScaler{Backend: configpb},
	}

	factory := &mithrilSpotBidPoolFactory{}
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
