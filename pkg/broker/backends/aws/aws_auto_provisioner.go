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
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	configpb "velda.io/velda/pkg/proto/config"
)

type AwsAutoPoolProvisioner struct {
	schedulerSet *broker.SchedulerSet
	cfg          *configpb.AWSAutoProvisioner
}

func (p *AwsAutoPoolProvisioner) Run(ctx context.Context) {
	p.run(ctx)
}

func (p *AwsAutoPoolProvisioner) run(ctx context.Context) error {
	types, err := getAvailableInstanceTypes(ctx, p.cfg)
	if err != nil {
		return fmt.Errorf("failed to get available instance types: %w", err)
	}
	for _, instanceType := range types {
		template := proto.Clone(p.cfg.Template).(*configpb.AutoscalerBackendAWSLaunchTemplate)
		template.InstanceType = instanceType
		template.AmiId = "ami-056f848b337c14f24"
		autoScaler := proto.Clone(p.cfg.GetAutoscalerConfig()).(*configpb.AgentPool_AutoScaler)
		if autoScaler == nil {
			autoScaler = &configpb.AgentPool_AutoScaler{
				MaxAgents:     10,
				MaxIdleAgents: 1,
				IdleDecay:     durationpb.New(1 * time.Minute),
			}
		}
		autoScaler.Backend = &configpb.AutoscalerBackend{
			Backend: &configpb.AutoscalerBackend_AwsLaunchTemplate{
				AwsLaunchTemplate: template,
			},
		}
		newPoolConfig := &configpb.AgentPool{
			Name:       p.cfg.PoolPrefix + instanceType,
			AutoScaler: autoScaler,
		}

		pool, err := p.schedulerSet.GetOrCreatePool(newPoolConfig.Name)
		if err != nil {
			return err
		}
		autoscalerConfig, err := backends.AutoScaledConfigFromConfig(ctx, newPoolConfig)
		if err != nil {
			return err
		}
		log.Printf("Created pool: %v", newPoolConfig.Name)
		pool.PoolManager.UpdateConfig(autoscalerConfig)
		if newPoolConfig.AutoScaler.GetInitialDelay() != nil {
			time.AfterFunc(newPoolConfig.AutoScaler.GetInitialDelay().AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
		} else {
			pool.PoolManager.ReadyForIdleMaintenance()
		}
	}
	return nil
}

func getAvailableInstanceTypes(ctx context.Context, cfg *configpb.AWSAutoProvisioner) ([]string, error) {
	region := cfg.Template.Region
	if cfg.Template.Zone != "" {
		region = cfg.Template.Zone[:len(cfg.Template.Zone)-1]
	}
	if region == "" {
		return nil, errors.New("region or zone must be specified")
	}
	zone := cfg.Template.Zone
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	// List all instance types in the region
	ec2Client := ec2.NewFromConfig(awsCfg)

	req := &ec2.DescribeInstanceTypeOfferingsInput{}
	if zone != "" {
		req.LocationType = ec2types.LocationTypeAvailabilityZone
		req.Filters = []ec2types.Filter{
			{
				Name:   aws.String("location"),
				Values: []string{zone},
			},
		}
	} else if region != "" {
		req.LocationType = ec2types.LocationTypeRegion
		req.Filters = []ec2types.Filter{
			{
				Name:   aws.String("location"),
				Values: []string{region},
			},
		}
	} else {
		return nil, errors.New("region or zone must be specified")
	}
	offerings, err := ec2Client.DescribeInstanceTypeOfferings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to describe instance type offerings: %w", err)
	}
	result := make([]string, 0, len(offerings.InstanceTypeOfferings))
	for _, offering := range offerings.InstanceTypeOfferings {
		instType := string(offering.InstanceType)
		matched := false
		if len(cfg.InstanceTypePrefixes) > 0 {
			for _, prefix := range cfg.InstanceTypePrefixes {
				if strings.HasPrefix(instType, prefix) {
					matched = true
					break
				}
			}
		} else {
			matched = true
		}
		if matched {
			result = append(result, instType)
		}
	}
	return result, nil
}

type AwsAutoProvisionerFactory struct{}

func (*AwsAutoProvisionerFactory) NewProvisioner(cfg *configpb.Provisioner, schedulers *broker.SchedulerSet) (backends.Provisioner, error) {
	cfgAws := cfg.GetAwsAuto()
	return &AwsAutoPoolProvisioner{
		schedulerSet: schedulers,
		cfg:          cfgAws,
	}, nil
}

func (*AwsAutoProvisionerFactory) CanHandle(cfg *configpb.Provisioner) bool {
	return cfg.GetAwsAuto() != nil
}

func init() {
	backends.RegisterProvisioner(&AwsAutoProvisionerFactory{})
}
