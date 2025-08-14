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

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

type awsPoolBackend struct {
	region              string
	launchTemplateId    string
	instanceNamePrefix  string
	useInstanceIdAsName bool
	// Do not store ec2.Client here, as it will export all methods of ec2.Client and expode the binary size.
	// Seems awsPoolBackend[UsedInIface] will export all fields of awsPoolBackend.
	awsCfg        aws.Config
	lastOp        *ec2.DescribeInstancesOutput
	instanceIdMap map[string]string
}

func NewAWSPoolBackend(region, launchTemplateId, instanceNamePrefix string, useInstanceIdAsName bool,
	awsCfg aws.Config) broker.ResourcePoolBackend {
	// Fetch template ID from name
	return &awsPoolBackend{
		region:              region,
		launchTemplateId:    launchTemplateId,
		instanceNamePrefix:  instanceNamePrefix,
		useInstanceIdAsName: useInstanceIdAsName,
		awsCfg:              awsCfg,
		instanceIdMap:       make(map[string]string),
	}
}

func (a *awsPoolBackend) svc() *ec2.Client {
	return ec2.NewFromConfig(a.awsCfg, func(o *ec2.Options) {
		o.Region = a.region
	})
}

func (a *awsPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	nameSuffix := utils.RandString(5)
	input := &ec2.RunInstancesInput{
		LaunchTemplate: &ec2types.LaunchTemplateSpecification{
			LaunchTemplateId: aws.String(a.launchTemplateId),
			Version:          aws.String("$Default"),
		},
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	}
	var name string
	if !a.useInstanceIdAsName {
		name = fmt.Sprintf("%s%s", a.instanceNamePrefix, nameSuffix)
		input.TagSpecifications = []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags: []ec2types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String(name),
					},
				},
			},
		}
	}
	result, err := a.svc().RunInstances(ctx, input)
	if err != nil {
		return "", err
	}
	instanceName := *result.Instances[0].InstanceId
	if a.useInstanceIdAsName {
		name = instanceName
	} else {
		a.instanceIdMap[name] = instanceName
	}
	log.Printf("Created instance %s AS %s", *result.Instances[0].InstanceId, name)
	//a.lastOp = result
	return name, nil
}

func (a *awsPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	var instanceId string
	if a.useInstanceIdAsName {
		instanceId = workerName
	} else {
		var ok bool
		instanceId, ok = a.instanceIdMap[workerName]
		if !ok {
			// Query from EC2
			input := &ec2.DescribeInstancesInput{
				Filters: []ec2types.Filter{
					{
						Name:   aws.String("tag:Name"),
						Values: []string{workerName},
					},
				},
			}
			result, err := a.svc().DescribeInstances(ctx, input)
			if err != nil {
				return fmt.Errorf("Failed to find instance %s: %v", workerName, err)
			}
			if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
				return fmt.Errorf("Failed to find instance %s", workerName)
			}
			instanceId = *result.Reservations[0].Instances[0].InstanceId
		}
	}
	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceId},
	}
	_, err := a.svc().TerminateInstances(ctx, input)
	if err != nil {
		return err
	}
	delete(a.instanceIdMap, workerName)
	return nil
}

func (a *awsPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	// Filter by template ID
	input := &ec2.DescribeInstancesInput{}
	input.Filters = []ec2types.Filter{
		{
			Name:   aws.String("tag:aws:ec2launchtemplate:id"),
			Values: []string{a.launchTemplateId},
		},
	}
	result, err := a.svc().DescribeInstances(ctx, input)
	if err != nil {
		return nil, err
	}
	var res []broker.WorkerStatus
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			if instance.State.Name == "terminated" || instance.State.Name == "shutting-down" {
				continue
			}
			name := ""
			if !a.useInstanceIdAsName {
				for _, tag := range instance.Tags {
					if *tag.Key == "Name" {
						name = *tag.Value
						break
					}
				}
				if name == "" {
					log.Printf("Instance %s has no Name tag", *instance.InstanceId)
					name = *instance.InstanceId
				}
			} else {
				name = *instance.InstanceId
			}
			res = append(res, broker.WorkerStatus{
				Name: name,
			})
		}
	}
	return res, nil
}

func (a *awsPoolBackend) WaitForLastOperation(ctx context.Context) error {
	if a.lastOp == nil {
		return nil
	}
	return nil
}

type awsPoolFactory struct{}

func (f *awsPoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_AwsLaunchTemplate:
		return true
	}
	return false
}

func (f *awsPoolFactory) NewBackend(pb *proto.AutoscalerBackend) (broker.ResourcePoolBackend, error) {
	ctx := context.Background()
	awsTemplate := pb.GetAwsLaunchTemplate()
	prefix := awsTemplate.InstanceNamePrefix
	if prefix == "" {
		prefix = awsTemplate.LaunchTemplateName + "-"
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	svc := ec2.NewFromConfig(cfg)
	// Find template ID
	var input ec2.DescribeLaunchTemplatesInput
	input.LaunchTemplateNames = []string{awsTemplate.LaunchTemplateName}
	result, err := svc.DescribeLaunchTemplates(ctx, &input)
	if err != nil {
		return nil, err
	}
	if len(result.LaunchTemplates) == 0 {
		return nil, fmt.Errorf("Launch template %s not found", awsTemplate.LaunchTemplateName)
	}
	launchTemplateId := *result.LaunchTemplates[0].LaunchTemplateId
	return NewAWSPoolBackend(awsTemplate.Region, launchTemplateId, prefix, awsTemplate.UseInstanceIdAsName, cfg), nil
}

func init() {
	backends.Register(&awsPoolFactory{})
}
