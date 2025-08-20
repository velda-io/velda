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
// AWS_BACKEND=us-east-1/velda-oss-ue1-agent-shell go test --tags aws -v ./pkg/broker/backends/aws

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

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
				Region:              aws_backend[0],
				LaunchTemplateName:  aws_backend[1],
				UseInstanceIdAsName: true,
			},
		},
	}

	factory := &awsLaunchTemplatePoolFactory{}
	backend, err := factory.NewBackend(configpb)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}

	backend_testing.TestSimpleScaleUpDown(t, backend)
}

type awsWaitUntilRunning struct {
	*awsPoolBackend
}

func (r *awsWaitUntilRunning) WaitForLastOperation(ctx context.Context) error {
	svc := r.svc()
	instanceId := r.lastStartedInstanceId
	log.Printf("Waiting for instance %s to be running", instanceId)
	for {
		desc, err := svc.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceId},
		})
		if err != nil {
			return err
		}
		if len(desc.Reservations) == 0 || len(desc.Reservations[0].Instances) == 0 {
			return errors.New("instance not found")
		}
		if desc.Reservations[0].Instances[0].State.Name == ec2types.InstanceStateNameRunning {
			time.Sleep(60 * time.Second) // Give it some time to be fully ready
			return nil
		}
		time.Sleep(1000 * time.Millisecond)
	}
}

func TestAWSBackendWithDeletionBuffer(t *testing.T) {
	if os.Getenv("AWS_BACKEND") == "" {
		t.Skip("AWS_BACKEND not set")
	}
	aws_backend := strings.Split(os.Getenv("AWS_BACKEND"), "/")

	configpb := &cfgpb.AutoscalerBackend{
		Backend: &cfgpb.AutoscalerBackend_AwsLaunchTemplate{
			AwsLaunchTemplate: &cfgpb.AutoscalerBackendAWSLaunchTemplate{
				Region:              aws_backend[0],
				LaunchTemplateName:  aws_backend[1],
				UseInstanceIdAsName: true,
				MaxStoppedInstances: 1,
			},
		},
	}

	factory := &awsLaunchTemplatePoolFactory{}
	backend, err := factory.NewBackend(configpb)
	if err != nil {
		t.Fatalf("Failed to create backend: %v", err)
	}
	backendAws := backend.(*awsPoolBackend)
	deletionReady := func() {
		t.Log("Waiting for instance to be stopped")
		for {
			backendAws.ListWorkers(context.Background())
			if len(backendAws.stoppedInstances) > 0 {
				// Next deletion will terminate the instance instead.
				configpb.Backend.(*cfgpb.AutoscalerBackend_AwsLaunchTemplate).AwsLaunchTemplate.MaxStoppedInstances = 0
				return
			}
			time.Sleep(1000 * time.Millisecond)
		}
	}

	backend_testing.TestScaleUpDownWithBuffer(t, &awsWaitUntilRunning{backendAws}, deletionReady)
}
