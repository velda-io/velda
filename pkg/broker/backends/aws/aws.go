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

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

type awsPoolBackend struct {
	region             string
	launchTemplateId   string
	instanceNamePrefix string
	svc                *ec2.EC2
	lastOp             *ec2.DescribeInstancesOutput
	instanceIdMap      map[string]string
}

func NewAWSPoolBackend(region, launchTemplateId, instanceNamePrefix string,
	svc *ec2.EC2) broker.ResourcePoolBackend {
	// Fetch template ID from name
	return &awsPoolBackend{
		region:             region,
		launchTemplateId:   launchTemplateId,
		instanceNamePrefix: instanceNamePrefix,
		svc:                svc,
		instanceIdMap:      make(map[string]string),
	}
}

func (a *awsPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	nameSuffix := utils.RandString(5)
	name := fmt.Sprintf("%s%s", a.instanceNamePrefix, nameSuffix)
	input := &ec2.RunInstancesInput{
		LaunchTemplate: &ec2.LaunchTemplateSpecification{
			LaunchTemplateId: aws.String(a.launchTemplateId),
			Version:          aws.String("$Default"),
		},
		MinCount: aws.Int64(1),
		MaxCount: aws.Int64(1),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("instance"),
				Tags: []*ec2.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String(name),
					},
				},
			},
		},
	}
	result, err := a.svc.RunInstancesWithContext(ctx, input)
	if err != nil {
		return "", err
	}
	log.Printf("Created instance %s", *result.Instances[0].InstanceId)
	a.instanceIdMap[name] = *result.Instances[0].InstanceId
	//a.lastOp = result
	return name, nil
}

func (a *awsPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	instanceId, ok := a.instanceIdMap[workerName]
	if !ok {
		// Query from EC2
		input := &ec2.DescribeInstancesInput{
			Filters: []*ec2.Filter{
				{
					Name:   aws.String("tag:Name"),
					Values: []*string{aws.String(workerName)},
				},
			},
		}
		result, err := a.svc.DescribeInstancesWithContext(ctx, input)
		if err != nil {
			return fmt.Errorf("Failed to find instance %s: %v", workerName, err)
		}
		if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
			return fmt.Errorf("Failed to find instance %s", workerName)
		}
		instanceId = *result.Reservations[0].Instances[0].InstanceId
	}
	input := &ec2.TerminateInstancesInput{
		InstanceIds: []*string{aws.String(instanceId)},
	}
	_, err := a.svc.TerminateInstancesWithContext(ctx, input)
	if err != nil {
		return err
	}
	delete(a.instanceIdMap, workerName)
	return nil
}

func (a *awsPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	// Filter by template ID
	input := &ec2.DescribeInstancesInput{}
	input.Filters = []*ec2.Filter{
		{
			Name:   aws.String("tag:aws:ec2launchtemplate:id"),
			Values: []*string{aws.String(a.launchTemplateId)},
		},
	}
	result, err := a.svc.DescribeInstancesWithContext(ctx, input)
	if err != nil {
		return nil, err
	}
	var res []broker.WorkerStatus
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			if *instance.State.Name == "terminated" || *instance.State.Name == "shutting-down" {
				continue
			}
			name := ""
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
	awsTemplate := pb.GetAwsLaunchTemplate()
	prefix := awsTemplate.InstanceNamePrefix
	if prefix == "" {
		prefix = awsTemplate.LaunchTemplateName + "-"
	}
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(awsTemplate.Region),
	})
	if err != nil {
		return nil, err
	}
	svc := ec2.New(sess)
	// Find template ID
	var input ec2.DescribeLaunchTemplatesInput
	input.LaunchTemplateNames = []*string{aws.String(awsTemplate.LaunchTemplateName)}
	result, err := svc.DescribeLaunchTemplates(&input)
	if err != nil {
		return nil, err
	}
	if len(result.LaunchTemplates) == 0 {
		return nil, fmt.Errorf("Launch template %s not found", awsTemplate.LaunchTemplateName)
	}
	launchTemplateId := *result.LaunchTemplates[0].LaunchTemplateId
	return NewAWSPoolBackend(awsTemplate.Region, launchTemplateId, prefix, svc), nil
}

func init() {
	backends.Register(&awsPoolFactory{})
}
