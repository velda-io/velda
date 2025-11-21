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
package nebius

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nebius/gosdk"
	"github.com/nebius/gosdk/auth"
	common "github.com/nebius/gosdk/proto/nebius/common/v1"
	compute "github.com/nebius/gosdk/proto/nebius/compute/v1"
	computeservice "github.com/nebius/gosdk/services/nebius/compute/v1"
	pb "google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"velda.io/velda"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

type nebiusPoolBackend struct {
	cfg             *proto.AutoscalerBackendNebiusLaunchTemplate
	sdk             *gosdk.SDK
	instanceService computeservice.InstanceService
	diskService     computeservice.DiskService
}

func NewNebiusPoolBackend(cfg *proto.AutoscalerBackendNebiusLaunchTemplate, sdk *gosdk.SDK) broker.ResourcePoolBackend {
	instanceService := sdk.Services().Compute().V1().Instance()
	diskService := sdk.Services().Compute().V1().Disk()

	return &nebiusPoolBackend{
		cfg:             cfg,
		sdk:             sdk,
		instanceService: instanceService,
		diskService:     diskService,
	}
}

func (n *nebiusPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	// Create a new instance
	var name string
	labels := make(map[string]string)

	for k, v := range n.cfg.GetLabels() {
		labels[k] = v
	}
	name = fmt.Sprintf("%s-%s", n.cfg.InstanceNamePrefix, utils.RandString(5))

	// Prepare cloud-init user data
	var userData string
	agentConfig := n.cfg.AgentConfigContent
	version := n.cfg.AgentVersionOverride
	if version == "" {
		version = velda.Version
	}
	cloudInitConfig := map[string]interface{}{
		"bootcmd": []string{
			"mkdir -p /run/velda",
			fmt.Sprintf("cat << EOF > /run/velda/velda.yaml\n%s\nEOF", agentConfig),
			"nvidia-smi",
		},
		"runcmd": []string{
			"curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/nvidia-collect.sh -o /tmp/nvidia-collect.sh && bash /tmp/nvidia-collect.sh",
			fmt.Sprintf("curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/velda-%s-linux-amd64 -o /bin/velda && chmod +x /bin/velda", version),
			"curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/velda-agent.service -o /etc/systemd/system/velda-agent.service",
			"systemctl daemon-reload",
			"systemctl enable velda-agent.service",
			"systemctl start velda-agent.service",
		},
	}
	if n.cfg.GetAdminSshKey() != "" {
		cloudInitConfig["users"] = []map[string]interface{}{
			{
				"name":  "velda",
				"sudo":  "ALL=(ALL) NOPASSWD:ALL",
				"shell": "/bin/bash",
				"ssh_authorized_keys": []string{
					n.cfg.GetAdminSshKey(),
				},
			},
		}
	}

	cloudInitData, err := yaml.Marshal(cloudInitConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cloud-init data: %w", err)
	}
	userData = string(append([]byte("#cloud-config\n"), cloudInitData...))

	// Build instance specification
	diskSizeGb := n.cfg.GetBootDiskSizeGb()
	// Ubuntu with CUDA minimum requirement
	if diskSizeGb < 40 {
		diskSizeGb = 40
	}

	diskOp, err := n.diskService.Create(ctx, &compute.CreateDiskRequest{
		Metadata: &common.ResourceMetadata{
			ParentId: n.cfg.GetParentId(),
			Name:     name + "-boot-disk",
			Labels:   labels,
		},
		Spec: &compute.DiskSpec{
			Size:           &compute.DiskSpec_SizeGibibytes{SizeGibibytes: diskSizeGb},
			BlockSizeBytes: 4096,
			Type:           compute.DiskSpec_NETWORK_SSD,
			Source: &compute.DiskSpec_SourceImageFamily{
				SourceImageFamily: &compute.SourceImageFamily{
					ImageFamily: "ubuntu24.04-cuda12",
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create boot disk: %w", err)
	}
	disk, err := diskOp.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create boot disk: %w", err)
	}
	networkSpec := &compute.NetworkInterfaceSpec{
		Name:      "eth0",
		IpAddress: &compute.IPAddress{},
		SubnetId:  n.cfg.GetSubnetId(),
	}
	if n.cfg.GetUsePublicIp() {
		networkSpec.PublicIpAddress = &compute.PublicIPAddress{}
	}

	instanceSpec := &compute.InstanceSpec{
		Resources: &compute.ResourcesSpec{
			Platform: n.cfg.GetPlatform(),
			Size: &compute.ResourcesSpec_Preset{
				Preset: n.cfg.GetResourcePreset(),
			},
		},
		NetworkInterfaces: []*compute.NetworkInterfaceSpec{networkSpec},
		BootDisk: &compute.AttachedDiskSpec{
			AttachMode: compute.AttachedDiskSpec_READ_WRITE,
			Type: &compute.AttachedDiskSpec_ExistingDisk{
				ExistingDisk: &compute.ExistingDisk{
					Id: disk.ResourceID(),
				},
			},
		},
		CloudInitUserData: userData,
		Hostname:          name,
	}

	// Add preemptible settings if configured
	if n.cfg.GetPreemptible() {
		instanceSpec.Preemptible = &compute.PreemptibleSpec{}
	}

	operation, err := n.instanceService.Create(ctx, &compute.CreateInstanceRequest{
		Metadata: &common.ResourceMetadata{
			ParentId: n.cfg.GetParentId(),
			Name:     name,
			Labels:   labels,
		},
		Spec: instanceSpec,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create Nebius instance: %w", err)
	}

	operation, err = operation.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to wait for Nebius instance creation: %w", err)
	}

	log.Printf("Created Nebius instance %s AS %s", operation.ResourceID(), name)
	return name, nil
}

func (n *nebiusPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	// WorkerName is the instance ID (we always use instance ID as the worker name)
	instance, err := n.instanceService.GetByName(ctx, &common.GetByNameRequest{
		ParentId: n.cfg.GetParentId(),
		Name:     workerName,
	})
	if err != nil {
		return err
	}

	instanceId := instance.Metadata.Id
	// Terminate the instance
	log.Printf("Terminating Nebius instance %s (%s)", instanceId, workerName)
	operation, err := n.instanceService.Delete(ctx, &compute.DeleteInstanceRequest{
		Id: instanceId,
	})
	if err != nil {
		return err
	}

	_, err = operation.Wait(ctx)
	if err != nil {
		return err
	}

	operation, err = n.diskService.Delete(ctx, &compute.DeleteDiskRequest{
		Id: instance.Spec.BootDisk.GetExistingDisk().GetId(),
	})
	if err != nil {
		return err
	}

	_, err = operation.Wait(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (n *nebiusPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	listReq := &compute.ListInstancesRequest{
		ParentId: n.cfg.GetParentId(),
	}
	var res []broker.WorkerStatus
	// We'll actively terminate any stopped instances (no parked/reuse support)
	for instance, err := range n.instanceService.Filter(ctx, listReq) {
		if err != nil {
			log.Printf("Failed to filter instances: %v", err)
			return nil, err
		}
		metadata := instance.GetMetadata()
		status := instance.GetStatus()

		// Skip instances being deleted
		if status.GetState() == compute.InstanceStatus_DELETING {
			continue
		}
		// Client-side label/tag filtering
		if len(n.cfg.GetLabels()) > 0 {
			instLabels := metadata.GetLabels()
			matched := true
			for k, v := range n.cfg.GetLabels() {
				if lv, ok := instLabels[k]; !ok || lv != v {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}

		instanceId := metadata.GetName()

		name := instanceId

		// Active/running instance
		res = append(res, broker.WorkerStatus{Name: name})
	}

	return res, nil
}

func (n *nebiusPoolBackend) WaitForLastOperation(ctx context.Context) error {
	return nil
}

type nebiusLaunchTemplatePoolFactory struct{}

func (f *nebiusLaunchTemplatePoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_NebiusLaunchTemplate:
		return true
	}
	return false
}

func (f *nebiusLaunchTemplatePoolFactory) NewBackend(pool *proto.AgentPool, brokerInfo *agentpb.BrokerInfo) (broker.ResourcePoolBackend, error) {
	ctx := context.Background()
	nebiusTemplate := pool.GetAutoScaler().GetBackend().GetNebiusLaunchTemplate()

	if nebiusTemplate.AgentConfig != nil {
		nebiusTemplate = pb.Clone(nebiusTemplate).(*proto.AutoscalerBackendNebiusLaunchTemplate)
		nebiusTemplate.AgentConfig.Pool = pool.GetName()
		if nebiusTemplate.AgentConfig.Broker == nil {
			if brokerInfo == nil {
				return nil, fmt.Errorf("no default broker info provided for pool %s", pool.GetName())
			}
			nebiusTemplate.AgentConfig.Broker = brokerInfo
		}
		var err error
		nebiusTemplate.AgentConfigContent, err = utils.ProtoToYaml(nebiusTemplate.AgentConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal agent config: %w", err)
		}
	}
	if nebiusTemplate.GetInstanceNamePrefix() == "" {
		nebiusTemplate.InstanceNamePrefix = fmt.Sprintf("velda-%s", pool.GetName())
	}

	// Initialize Nebius SDK with credentials
	var creds gosdk.Credentials
	var err error
	// Try environment variable
	if token := os.Getenv("NEBIUS_IAM_TOKEN"); token != "" {
		creds = gosdk.IAMToken(token)
	} else if serviceAccountKey := os.Getenv("NEBIUS_SERVICE_ACCOUNT_KEY"); serviceAccountKey != "" {
		// Parse service account credentials from JSON
		creds = gosdk.ServiceAccountReader(
			auth.NewServiceAccountCredentialsFileParser(
				nil,
				serviceAccountKey,
			),
		)
	} else if _, err := os.Stat("/mnt/cloud-metadata/token"); err == nil {
		// VM service account from Nebius
		tokener, err := auth.NewFileTokener("/mnt/cloud-metadata/token", time.Hour)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize Nebius file tokener: %w", err)
		}
		creds = gosdk.CustomTokener(tokener)
	} else {
		return nil, fmt.Errorf("no Nebius credentials provided for pool %s", pool.GetName())
	}

	sdkOptions := []gosdk.Option{
		gosdk.WithCredentials(creds),
	}

	sdk, err := gosdk.New(ctx, sdkOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Nebius SDK: %w", err)
	}

	return NewNebiusPoolBackend(nebiusTemplate, sdk), nil
}

func init() {
	backends.Register(&nebiusLaunchTemplatePoolFactory{})
}
