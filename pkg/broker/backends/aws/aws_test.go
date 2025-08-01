// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package aws

// To run test:
// AWS_BACKEND=us-west-1/velda-agent-shell go test -v ./pkg/broker/backends/aws

import (
	"os"
	"strings"
	"testing"

	"velda.io/velda/pkg/broker/backends/backend_testing"
	cfgpb "velda.io/velda/pkg/proto/config"
)

func TestAWSBackend(t *testing.T) {
	if os.Getenv("AWS_BACKEND") == "" {
		t.Skip("AWS_BACKEND not set")
	}
	aws_backend := strings.Split(os.Getenv("AWS_BACKEND"), "/")

	configpb := &cfgpb.AutoscalerBackend{
		Backend: &cfgpb.AutoscalerBackend_AwsLaunchTemplate{
			AwsLaunchTemplate: &cfgpb.AutoscalerBackendAWSLaunchTemplate{
				Region:             aws_backend[0],
				LaunchTemplateName: aws_backend[1],
			},
		},
	}

	factory := &awsPoolFactory{}
	backend, err := factory.NewBackend(configpb)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}

	backend_testing.TestSimpleScaleUpDown(t, backend)
}
