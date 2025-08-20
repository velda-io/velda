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
	"net"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"velda.io/velda"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	configpb "velda.io/velda/pkg/proto/config"
)

type AwsAutoPoolProvisioner struct {
	schedulerSet   *broker.SchedulerSet
	cfg            *configpb.AWSAutoProvisioner
	serverEndpoint net.TCPAddr
}

func (p *AwsAutoPoolProvisioner) Run(ctx context.Context) {
	err := p.run(ctx)
	if err != nil {
		log.Panicf("Failed to run AWS auto provisioner: %v", err)
	}
}

func (p *AwsAutoPoolProvisioner) run(ctx context.Context) error {
	types, err := getAvailableInstanceTypes(ctx, p.cfg)
	if err != nil {
		return fmt.Errorf("failed to get available instance types: %w", err)
	}
	p.serverEndpoint, err = getPublicEndpoint(ctx)
	if err != nil {
		return fmt.Errorf("failed to get public endpoint: %w", err)
	}
	log.Printf("Public endpoint: %s", p.serverEndpoint.String())
	for _, instanceType := range types {
		template := proto.Clone(p.cfg.Template).(*configpb.AutoscalerBackendAWSLaunchTemplate)
		poolName := p.cfg.PoolPrefix + instanceType
		template.InstanceType = instanceType
		template.AmiId, err = getAmi(ctx, p.cfg)
		if err != nil {
			return fmt.Errorf("failed to get AMI ID: %w", err)
		}

		if template.Tags == nil {
			template.Tags = make(map[string]string)
		}
		template.Tags["velda/pool"] = p.cfg.PoolPrefix + instanceType
		template.AgentConfigContent = fmt.Sprintf(
			`broker:
  address: "%s"
sandbox_config:
daemon_config:
pool: "%s"
`, p.serverEndpoint.String(), poolName)
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
			Name:       poolName,
			AutoScaler: autoScaler,
		}

		pool, err := p.schedulerSet.GetOrCreatePool(poolName)
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

func getPublicEndpoint(ctx context.Context) (net.TCPAddr, error) {
	// Get the interface with the default route
	interfaces, err := net.Interfaces()
	if err != nil {
		return net.TCPAddr{}, fmt.Errorf("failed to get network interfaces: %w", err)
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				dialer := &net.Dialer{
					Timeout:   1 * time.Second,
					LocalAddr: &net.UDPAddr{IP: ipNet.IP, Port: 0},
				}
				// Attempt to dial out to a public DNS server to check if this interface has internet access
				// This is a simple way to check if the interface is usable for external communication
				// Check if this interface has the default route by attempting to dial out
				conn, err := dialer.DialContext(ctx, "udp", "8.8.8.8:53")
				if err == nil {
					conn.Close()
					return net.TCPAddr{IP: ipNet.IP, Port: 50051}, nil
				}
			}
		}
	}

	return net.TCPAddr{}, errors.New("no interface with default route found")
}

func getAvailableInstanceTypes(ctx context.Context, cfg *configpb.AWSAutoProvisioner) ([]string, error) {
	if cfg.Template == nil {
		return nil, errors.New("AWSAutoProvisioner template must be specified")
	}
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

func getAmi(ctx context.Context, cfg *configpb.AWSAutoProvisioner) (string, error) {
	if cfg.AmiId != "" {
		return cfg.AmiId, nil
	}
	region := cfg.Template.Region
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return "", fmt.Errorf("failed to load AWS config: %w", err)
	}
	owner := cfg.AmiOwner
	if owner == "" {
		owner = "686255976885"
	}

	amiName := cfg.AmiName
	if amiName == "" {
		version := velda.Version
		if version == "" || version == "dev" {
			return "", fmt.Errorf("failed to determine AMI name: version is empty or set to %q", version)
		}
		amiName = fmt.Sprintf("velda-agent-%s", version)
	}
	// List all AMI images in the region matching the specified name and owner
	ec2Client := ec2.NewFromConfig(awsCfg)

	result, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{owner},
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{amiName},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe AMI: %w", err)
	}
	if len(result.Images) == 0 {
		return "", fmt.Errorf("AMI not found in account %s with name %s in region %s", owner, amiName, region)
	}
	return *result.Images[0].ImageId, nil
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
