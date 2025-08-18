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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	"github.com/spf13/cobra"

	"velda.io/velda/pkg/broker/backends"
	configpb "velda.io/velda/pkg/proto/config"
)

// ANSI color codes
const (
	ColorReset     = "\033[0m"
	ColorRed       = "\033[31m"
	ColorGreen     = "\033[32m"
	ColorYellow    = "\033[33m"
	ColorBlue      = "\033[34m"
	ColorPurple    = "\033[35m"
	ColorCyan      = "\033[36m"
	ColorWhite     = "\033[37m"
	ColorBold      = "\033[1m"
	ColorLightGray = "\033[37;2m" // Light gray for verbose output
)

type AwsConfigure struct{}

// AWSEnvironmentInfo holds detected AWS environment information
type AWSEnvironmentInfo struct {
	Region           string
	AvailabilityZone string
	SubnetID         string
	SecurityGroups   []string
	VpcID            string
}

// detectAWSEnvironment attempts to detect AWS environment settings from EC2 metadata
func (c *AwsConfigure) detectAWSEnvironment(ctx context.Context) (*AWSEnvironmentInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := imds.NewFromConfig(cfg)
	info := &AWSEnvironmentInfo{}

	// Try to get region from metadata
	if region, err := client.GetMetadata(ctx, &imds.GetMetadataInput{
		Path: "placement/region",
	}); err == nil {
		defer region.Content.Close()
		if regionBytes, err := io.ReadAll(region.Content); err == nil {
			info.Region = string(regionBytes)
		}
	} else {
		return nil, err
	}

	// Try to get availability zone
	if az, err := client.GetMetadata(ctx, &imds.GetMetadataInput{
		Path: "placement/availability-zone",
	}); err == nil {
		defer az.Content.Close()
		if azBytes, err := io.ReadAll(az.Content); err == nil {
			info.AvailabilityZone = string(azBytes)
		}
	}

	// Try to get instance metadata for network info
	if mac, err := client.GetMetadata(ctx, &imds.GetMetadataInput{
		Path: "network/interfaces/macs/",
	}); err == nil {
		defer mac.Content.Close()
		if macBytes, err := io.ReadAll(mac.Content); err == nil {
			macAddr := strings.TrimSpace(string(macBytes))
			if macAddr != "" {
				// Get subnet ID
				if subnet, err := client.GetMetadata(ctx, &imds.GetMetadataInput{
					Path: fmt.Sprintf("network/interfaces/macs/%s/subnet-id", macAddr),
				}); err == nil {
					defer subnet.Content.Close()
					if subnetBytes, err := io.ReadAll(subnet.Content); err == nil {
						info.SubnetID = string(subnetBytes)
					}
				}

				// Get VPC ID
				if vpc, err := client.GetMetadata(ctx, &imds.GetMetadataInput{
					Path: fmt.Sprintf("network/interfaces/macs/%s/vpc-id", macAddr),
				}); err == nil {
					defer vpc.Content.Close()
					if vpcBytes, err := io.ReadAll(vpc.Content); err == nil {
						info.VpcID = string(vpcBytes)
					}
				}

				// Get security groups
				if sgs, err := client.GetMetadata(ctx, &imds.GetMetadataInput{
					Path: fmt.Sprintf("network/interfaces/macs/%s/security-group-ids", macAddr),
				}); err == nil {
					defer sgs.Content.Close()
					if sgBytes, err := io.ReadAll(sgs.Content); err == nil {
						sgList := strings.Fields(string(sgBytes))
						info.SecurityGroups = sgList
					}
				}
			}
		}
	}

	return info, nil
}

// promptUser prompts the user for input with a default value
func (c *AwsConfigure) promptUser(cmd *cobra.Command, reader *bufio.Reader, prompt, defaultValue string) (string, error) {
	if defaultValue != "" {
		cmd.Printf("%s [%s]: ", prompt, defaultValue)
	} else {
		cmd.Printf("%s: ", prompt)
	}

	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	if input == "" && defaultValue != "" {
		return defaultValue, nil
	}

	return input, nil
}

// promptUserList prompts the user for a comma-separated list with default values
func (c *AwsConfigure) promptUserList(cmd *cobra.Command, reader *bufio.Reader, prompt string, defaultValues []string) ([]string, error) {
	defaultStr := strings.Join(defaultValues, ", ")
	if defaultStr != "" {
		cmd.Printf("%s [%s]: ", prompt, defaultStr)
	} else {
		cmd.Printf("%s: ", prompt)
	}

	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	if input == "" && len(defaultValues) > 0 {
		return defaultValues, nil
	}

	if input == "" {
		return []string{}, nil
	}

	// Split by comma and trim spaces
	values := strings.Split(input, ",")
	for i, v := range values {
		values[i] = strings.TrimSpace(v)
	}

	return values, nil
}

// validateAndResolveSecurityGroups validates security group names/IDs and resolves names to IDs
func (c *AwsConfigure) validateAndResolveSecurityGroups(ctx context.Context, ec2Client *ec2.Client, securityGroups []string, vpcID string) ([]string, error) {
	if len(securityGroups) == 0 {
		return []string{}, nil
	}

	var sgIds []string
	var sgNames []string

	// Separate IDs and names
	for _, sg := range securityGroups {
		if strings.HasPrefix(sg, "sg-") {
			sgIds = append(sgIds, sg)
		} else {
			sgNames = append(sgNames, sg)
		}
	}

	// Describe security groups to validate and resolve names
	input := &ec2.DescribeSecurityGroupsInput{}

	if len(sgIds) > 0 {
		input.GroupIds = sgIds
	}

	if len(sgNames) > 0 {
		input.GroupNames = sgNames
	}

	if vpcID != "" {
		input.Filters = []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcID},
			},
		}
	}

	result, err := ec2Client.DescribeSecurityGroups(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe security groups: %w", err)
	}

	var resolvedIds []string
	for _, sg := range result.SecurityGroups {
		if sg.GroupId != nil {
			resolvedIds = append(resolvedIds, *sg.GroupId)
		}
	}

	return resolvedIds, nil
}

// checkAWSPermissions validates that the user has necessary AWS permissions using dry-run mode where possible
func (c *AwsConfigure) checkAWSPermissions(cmd *cobra.Command, awsCfg aws.Config, ec2Client *ec2.Client, region string) error {
	// Check AWS permissions
	cmd.PrintErrf("%s🔐 Checking AWS permissions...%s\n", ColorCyan, ColorReset)

	ctx := cmd.Context()
	var permissionErrors []string

	stsClient := sts.NewFromConfig(awsCfg)

	// Call STS GetCallerIdentity
	out, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("failed to get AWS caller identity: %w", err)
	}

	cmd.PrintErrln("Current AWS role ARN:", *out.Arn)

	handleError := func(action string, err error) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "DryRunOperation":
				return
			}
		}
		if err != nil {
			permissionErrors = append(permissionErrors, fmt.Sprintf("%s: %v", action, err))
		}
	}

	// Test permission to describe instances (read-only, no dry-run needed)
	_, err = ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		DryRun:     aws.Bool(true), // Use dry-run to test permissions
		MaxResults: aws.Int32(5),   // Limit results to minimize impact
	})
	handleError("ec2:DescribeInstances", err)

	// Test permission to describe instance types (read-only, no dry-run needed)
	_, err = ec2Client.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		DryRun:     aws.Bool(true), // Use dry-run to test permissions
		MaxResults: aws.Int32(5),
	})
	handleError("ec2:DescribeInstanceTypes", err)

	// Test permission to describe security groups (read-only, no dry-run needed)
	_, err = ec2Client.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		DryRun:     aws.Bool(true), // Use dry-run to test permissions
		MaxResults: aws.Int32(5),
	})
	handleError("ec2:DescribeSecurityGroups", err)

	// Test permission to describe subnets (read-only, no dry-run needed)
	_, err = ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		DryRun:     aws.Bool(true), // Use dry-run to test permissions
		MaxResults: aws.Int32(5),
	})
	handleError("ec2:DescribeSubnets", err)

	// Test RunInstances permission using dry-run mode
	// This is the most critical permission for the AWS provisioner
	_, err = ec2Client.RunInstances(ctx, &ec2.RunInstancesInput{
		DryRun:       aws.Bool(true), // Use dry-run to test permissions without creating instances
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		InstanceType: ec2types.InstanceTypeT2Micro,
		ImageId:      aws.String("ami-056f848b337c14f24"),
	})
	handleError("ec2:RunInstances", err)

	if len(permissionErrors) > 0 {
		return fmt.Errorf("%sPermission check failed:%s\n %s", ColorRed, ColorReset, strings.Join(permissionErrors, "\n  "))
	}

	return nil
}

func (c *AwsConfigure) Configure(cmd *cobra.Command, config *configpb.Config) error {
	ctx := context.Background()
	cmd.PrintErrf("%s🔧 Configuring AWS backend...%s\n", ColorBlue+ColorBold, ColorReset)
	reader := bufio.NewReader(cmd.InOrStdin())

	// Load AWS config for validation
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Detect AWS environment from EC2 metadata
	cmd.PrintErrf("%s🔍 Detecting AWS environment...%s\n", ColorCyan, ColorReset)
	envInfo, err := c.detectAWSEnvironment(ctx)
	if err != nil {
		cmd.PrintErrf("%s⚠️  Could not detect AWS environment from EC2 metadata.%s\n", ColorYellow, ColorReset)
		cmd.PrintErrf(" %sThis is expected if running outside of AWS or without EC2 metadata access.\n", ColorLightGray)
		cmd.PrintErrf(" Error: %v%s\n", err, ColorReset)
		cmd.PrintErrf("%sThe current host must have direct connection to AWS EC2 network (e.g. VPN), "+
			"and remain connected when any workload is running.%s\n", ColorYellow, ColorReset)
		envInfo = &AWSEnvironmentInfo{}
		envInfo.Region = awsCfg.Region
	} else {
		cmd.PrintErrf("\n%s✅ Detected environment information:%s\n", ColorGreen, ColorReset)
		cmd.PrintErrf("  Region: %s%s%s\n", ColorWhite+ColorBold, envInfo.Region, ColorReset)
		cmd.PrintErrf("  Availability Zone: %s%s%s\n", ColorWhite+ColorBold, envInfo.AvailabilityZone, ColorReset)
		cmd.PrintErrf("  VPC ID: %s%s%s\n", ColorWhite+ColorBold, envInfo.VpcID, ColorReset)
		cmd.PrintErrf("  Subnet ID: %s%s%s\n", ColorWhite+ColorBold, envInfo.SubnetID, ColorReset)
		cmd.PrintErrf("  Security Groups: %s%v%s\n", ColorWhite+ColorBold, envInfo.SecurityGroups, ColorReset)
	}

	cmd.PrintErrf("\n%s📝 Please verify and/or provide the following information:%s\n\n", ColorPurple+ColorBold, ColorReset)

	// Prompt for region
	if envInfo.Region == "" {
		// Default region
		envInfo.Region = "us-east-1"
	}
	region, err := c.promptUser(cmd, reader, "AWS Region", envInfo.Region)
	if err != nil {
		return fmt.Errorf("failed to get region: %w", err)
	}
	if region == "" {
		return fmt.Errorf("AWS region is required")
	}
	ec2Client := ec2.NewFromConfig(awsCfg)

	// Prompt for security groups
	securityGroups, err := c.promptUserList(cmd, reader, "Security Groups (comma-separated names or IDs)", envInfo.SecurityGroups)
	if err != nil {
		return fmt.Errorf("failed to get security groups: %w", err)
	}

	// Validate and resolve security groups
	if len(securityGroups) > 0 {
		cmd.PrintErrf("%s🔍 Validating security groups...%s\n", ColorCyan, ColorReset)
		resolvedSGs, err := c.validateAndResolveSecurityGroups(ctx, ec2Client, securityGroups, envInfo.VpcID)
		if err != nil {
			cmd.PrintErrf("%s⚠️  Warning: Failed to validate security groups: %v%s\n", ColorYellow, err, ColorReset)
			cmd.PrintErrf("Proceeding with provided security groups as-is.\n")
		} else {
			securityGroups = resolvedSGs
			cmd.PrintErrf("%s✅ Validated security groups: %v%s\n", ColorGreen, securityGroups, ColorReset)
		}
	}

	// Prompt for subnet ID
	subnetID, err := c.promptUser(cmd, reader, "Subnet ID (leave empty for default)", envInfo.SubnetID)
	if err != nil {
		return fmt.Errorf("failed to get subnet ID: %w", err)
	}

	// Prompt for instance type prefixes
	defaultInstanceTypes := []string{"t2", "g4", "g6", "m6g"}
	instanceTypePrefixes, err := c.promptUserList(cmd, reader, "Instance type prefixes (comma-separated)", defaultInstanceTypes)
	if err != nil {
		return fmt.Errorf("failed to get instance type prefixes: %w", err)
	}
	if len(instanceTypePrefixes) == 0 {
		instanceTypePrefixes = defaultInstanceTypes
	}

	cmd.PrintErrln("For additional customization, you can create an AWS Launch Template in the AWS console.")
	cmd.PrintErrf("  %shttps://%s.console.aws.amazon.com/ec2/home?region=%s#CreateTemplate:%s\n", ColorBlue, region, region, ColorReset)
	templateId, err := c.promptUser(cmd, reader, "Launch Template name (leave empty for default)", "")
	if err != nil {
		return fmt.Errorf("failed to get launch template name: %w", err)
	}

	// Prompt for pool prefix
	defaultPoolPrefix := "aws:"
	poolPrefix, err := c.promptUser(cmd, reader, "Pool prefix", defaultPoolPrefix)
	if err != nil {
		return fmt.Errorf("failed to get pool prefix: %w", err)
	}
	if poolPrefix == "" {
		poolPrefix = defaultPoolPrefix
	}

	if err := c.checkAWSPermissions(cmd, awsCfg, ec2Client, region); err != nil {
		cmd.PrintErrf("%s❌ AWS permissions check failed: %v%s\n", ColorRed+ColorBold, err, ColorReset)
		// Ask user if they want to continue
		continueChoice, err := c.promptUser(cmd, reader, "Continue with configuration anyway? (y/n)", "n")
		if err != nil {
			return fmt.Errorf("failed to get user choice: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(continueChoice)) != "y" {
			return fmt.Errorf("configuration cancelled by user due to permission issues")
		}
	} else {
		cmd.PrintErrf("%s✅ AWS permissions check passed!%s\n", ColorGreen+ColorBold, ColorReset)
	}

	// Build the launch template configuration
	template := &configpb.AutoscalerBackendAWSLaunchTemplate{
		UseInstanceIdAsName: true,
		Region:              region,
		SecurityGroups:      securityGroups,
		LaunchTemplateName:  templateId,
	}

	// Set subnet ID if provided
	if subnetID != "" {
		template.SubnetId = subnetID
	}

	// Generate provisioner config based on user input
	provisioner := &configpb.Provisioner{
		Provisioner: &configpb.Provisioner_AwsAuto{
			AwsAuto: &configpb.AWSAutoProvisioner{
				PoolPrefix:           poolPrefix,
				Template:             template,
				InstanceTypePrefixes: instanceTypePrefixes,
			},
		},
	}

	config.Provisioners = append(config.Provisioners, provisioner)

	cmd.PrintErrf("\n%s🎉 AWS provisioner configuration completed successfully!%s\n", ColorGreen+ColorBold, ColorReset)
	cmd.PrintErrf("%sConfiguration summary:%s\n", ColorPurple+ColorBold, ColorReset)
	cmd.PrintErrf("  Pool prefix: %s%s%s\n", ColorWhite+ColorBold, poolPrefix, ColorReset)
	cmd.PrintErrf("  Region: %s%s%s\n", ColorWhite+ColorBold, region, ColorReset)
	cmd.PrintErrf("  Security groups: %s%v%s\n", ColorWhite+ColorBold, securityGroups, ColorReset)
	cmd.PrintErrf("  Subnet ID: %s%s%s\n", ColorWhite+ColorBold, subnetID, ColorReset)
	cmd.PrintErrf("  Instance type prefixes: %s%v%s\n", ColorWhite+ColorBold, instanceTypePrefixes, ColorReset)

	return nil
}

func (c *AwsConfigure) BackendName() string {
	return "aws"
}

func init() {
	backends.RegisterMiniAutoConfigurer(&AwsConfigure{})
}
