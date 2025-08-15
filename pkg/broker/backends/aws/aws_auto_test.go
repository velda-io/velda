//go:build aws

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
package aws

// To run test:
// go test --tags aws -v ./pkg/broker/backends/aws

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends/backend_testing"
	cfgpb "velda.io/velda/pkg/proto/config"
)

func TestAWSListInstances(t *testing.T) {
	cfg := &cfgpb.AWSAutoProvisioner{
		Template: &cfgpb.AutoscalerBackendAWSLaunchTemplate{
			Region: "us-west-1",
		},
		InstanceTypePrefixes: []string{"g4"},
	}
	types, err := getAvailableInstanceTypes(context.Background(), cfg)
	assert.NoError(t, err, "Failed to get available instance types")
	t.Logf("Available instance types: %v", types)
}

func TestAWSAutoBackend(t *testing.T) {
	ctx := context.Background()
	hostname, err := os.Hostname()
	require.NoError(t, err, "Failed to get hostname")
	tag := fmt.Sprintf("test-%s-%d", hostname, time.Now().Unix())
	schedulerSet := broker.NewSchedulerSet(ctx)
	cfg := &cfgpb.AWSAutoProvisioner{
		PoolPrefix: "aws:",
		Template: &cfgpb.AutoscalerBackendAWSLaunchTemplate{
			Region: "us-east-1",
			Tags: map[string]string{
				"test": tag,
			},
			UseInstanceIdAsName: true,
		},
		InstanceTypePrefixes: []string{"t2"},
	}
	provisioner := AwsAutoPoolProvisioner{
		schedulerSet: schedulerSet,
		cfg:          cfg,
	}
	require.NoError(t, provisioner.run(ctx), "Failed to provision nodes")
	pool, err := schedulerSet.GetPool("aws:t2.micro")
	require.NoError(t, err, "Failed to get pool")

	backend_testing.TestSimpleScaleUpDown(t, pool.PoolManager.Backend())
}
