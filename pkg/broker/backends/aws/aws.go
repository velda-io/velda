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
	"encoding/base64"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"gopkg.in/yaml.v3"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

type awsPoolBackend struct {
	cfg *proto.AutoscalerBackendAWSLaunchTemplate
	// Do not store ec2.Client here, as it will export all methods of ec2.Client and expode the binary size.
	// Seems awsPoolBackend[UsedInIface] will export all fields of awsPoolBackend.
	awsCfg           aws.Config
	launchTemplateId string
	instanceIdMap    map[string]string
}

func NewAWSPoolBackend(cfg *proto.AutoscalerBackendAWSLaunchTemplate, awsCfg aws.Config) broker.ResourcePoolBackend {
	// Fetch template ID from name
	return &awsPoolBackend{
		cfg:           cfg,
		awsCfg:        awsCfg,
		instanceIdMap: make(map[string]string),
	}
}

func (a *awsPoolBackend) svc() *ec2.Client {
	return ec2.NewFromConfig(a.awsCfg, func(o *ec2.Options) {
		o.Region = a.cfg.Region
	})
}

func (a *awsPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	input := &ec2.RunInstancesInput{
		MinCount: aws.Int32(1),
		MaxCount: aws.Int32(1),
	}
	if a.cfg.GetLaunchTemplateName() != "" {
		input.LaunchTemplate = &ec2types.LaunchTemplateSpecification{
			LaunchTemplateName: aws.String(a.cfg.GetLaunchTemplateName()),
			Version:            aws.String("$Default"),
		}
	}
	var name string
	tags := []ec2types.Tag{}
	if a.cfg.GetUseInstanceIdAsName() {
		input.PrivateDnsNameOptions = &ec2types.PrivateDnsNameOptionsRequest{
			HostnameType: ec2types.HostnameTypeResourceName,
		}
	} else {
		nameSuffix := utils.RandString(5)
		prefix := a.cfg.GetInstanceNamePrefix()
		if prefix == "" {
			prefix = a.cfg.GetLaunchTemplateName() + "-"
		}
		name = fmt.Sprintf("%s%s", prefix, nameSuffix)
		tags = append(tags, ec2types.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		})
	}
	if len(a.cfg.Tags) > 0 {
		for k, v := range a.cfg.Tags {
			tags = append(tags, ec2types.Tag{
				Key:   aws.String(k),
				Value: aws.String(v),
			})
		}
	}
	if len(tags) > 0 {
		input.TagSpecifications = []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         tags,
		}}
	}
	if a.cfg.AmiId != "" {
		input.ImageId = &a.cfg.AmiId
	}
	if a.cfg.InstanceType != "" {
		input.InstanceType = ec2types.InstanceType(a.cfg.InstanceType)
	}
	if a.cfg.AgentConfigContent != "" {
		cloudInitData, err := yaml.Marshal(map[string]interface{}{
			"bootcmd": []string{
				"mkdir -p /run/velda",
				fmt.Sprintf("cat << EOF > /run/velda/velda.yaml\n%s\nEOF", a.cfg.AgentConfigContent),
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to marshal cloud-init data: %w", err)
		}
		cloudInit := append([]byte("#cloud-config\n"), cloudInitData...)
		base64CloudInit := base64.StdEncoding.EncodeToString(cloudInit)

		input.UserData = aws.String(base64CloudInit)
	}
	if len(a.cfg.SecurityGroups) > 0 {
		input.SecurityGroups = a.cfg.SecurityGroups
	}
	result, err := a.svc().RunInstances(ctx, input)
	if err != nil {
		return "", err
	}
	instanceName := *result.Instances[0].InstanceId
	if a.cfg.GetUseInstanceIdAsName() {
		name = instanceName
	} else {
		a.instanceIdMap[name] = instanceName
	}
	log.Printf("Created instance %s AS %s", *result.Instances[0].InstanceId, name)
	return name, nil
}

func (a *awsPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	var instanceId string
	if a.cfg.UseInstanceIdAsName {
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
	input := &ec2.DescribeInstancesInput{}
	if a.cfg.Tags != nil {
		for k, v := range a.cfg.Tags {
			input.Filters = append(input.Filters, ec2types.Filter{
				Name:   aws.String(fmt.Sprintf("tag:%s", k)),
				Values: []string{v},
			})
		}
	}
	// Filter by template ID
	if a.cfg.GetLaunchTemplateName() != "" {
		if a.launchTemplateId == "" {
			// Fetch template ID from name
			out, err := a.svc().DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
				LaunchTemplateNames: []string{a.cfg.GetLaunchTemplateName()},
			})
			if err != nil {
				return nil, err
			}
			if len(out.LaunchTemplates) == 0 {
				return nil, fmt.Errorf("launch template %s not found", a.cfg.GetLaunchTemplateName())
			}
			a.launchTemplateId = *out.LaunchTemplates[0].LaunchTemplateId
		}
		input.Filters = append(input.Filters, ec2types.Filter{
			Name:   aws.String("tag:aws:ec2launchtemplate:id"),
			Values: []string{a.launchTemplateId},
		})
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
			if !a.cfg.UseInstanceIdAsName {
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
	return nil
}

type awsLaunchTemplatePoolFactory struct{}

func (f *awsLaunchTemplatePoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_AwsLaunchTemplate:
		return true
	}
	return false
}

func (f *awsLaunchTemplatePoolFactory) NewBackend(pb *proto.AutoscalerBackend) (broker.ResourcePoolBackend, error) {
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
	return NewAWSPoolBackend(awsTemplate, cfg), nil
}

func init() {
	backends.Register(&awsLaunchTemplatePoolFactory{})
}
