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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/nebius/gosdk"
	"github.com/nebius/gosdk/auth"
	"github.com/nebius/gosdk/config/reader"
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

const configHashLabel = "velda/config-hash"

type nebiusPoolBackend struct {
	cfg             *proto.AutoscalerBackendNebiusLaunchTemplate
	sdk             *gosdk.SDK
	instanceService computeservice.InstanceService
	diskService     computeservice.DiskService
}

func NewNebiusPoolBackend(cfg *proto.AutoscalerBackendNebiusLaunchTemplate, sdk *gosdk.SDK) broker.ResourcePoolBackend {
	instanceService := sdk.Services().Compute().V1().Instance()
	diskService := sdk.Services().Compute().V1().Disk()

	backend := &nebiusPoolBackend{
		cfg:             cfg,
		sdk:             sdk,
		instanceService: instanceService,
		diskService:     diskService,
	}

	return backends.MakeAsyncResumable(backend, int(cfg.GetMaxDiskPoolSize()))
}

// diskTypeFromProto converts the proto NebiusBootDiskType to the Nebius SDK DiskSpec_DiskType enum.
func diskTypeFromProto(bt proto.NebiusBootDiskType) compute.DiskSpec_DiskType {
	switch bt {
	case proto.NebiusBootDiskType_SSD_NRD:
		return compute.DiskSpec_NETWORK_SSD_NON_REPLICATED
	case proto.NebiusBootDiskType_SSD_IO:
		return compute.DiskSpec_NETWORK_SSD_IO_M3
	default:
		return compute.DiskSpec_NETWORK_SSD
	}
}

// GenerateWorkerName implements SyncBackend interface
func (n *nebiusPoolBackend) GenerateWorkerName() string {
	return fmt.Sprintf("%s-%s", n.cfg.InstanceNamePrefix, utils.RandString(5))
}

func (n *nebiusPoolBackend) desiredVersion() string {
	version := n.cfg.GetAgentVersionOverride()
	if version == "" {
		version = velda.Version
	}
	return version
}

func (n *nebiusPoolBackend) currentConfigHash() string {
	// Create MarshalOptions with Deterministic set to true.
	options := pb.MarshalOptions{
		Deterministic: true,
	}

	// Marshal the message into a byte slice.
	data, err := options.Marshal(n.cfg)
	if err != nil {
		panic(err)
	}
	hasher := sha256.New()
	hasher.Write(data)
	hasher.Write([]byte(n.desiredVersion()))
	sum := hasher.Sum(nil)

	return hex.EncodeToString(sum[:16])
}

func (n *nebiusPoolBackend) baseLabels() map[string]string {
	labels := make(map[string]string)
	for k, v := range n.cfg.GetLabels() {
		labels[k] = v
	}
	labels["managed-by"] = "velda"
	labels[configHashLabel] = n.currentConfigHash()
	return labels
}

// CreateWorker implements SyncBackend interface
func (n *nebiusPoolBackend) CreateWorker(ctx context.Context, name string) (backends.WorkerInfo, error) {
	instanceID, err := n.createInstance(ctx, name)
	if err != nil {
		return backends.WorkerInfo{}, err
	}
	return backends.WorkerInfo{State: backends.WorkerStateActive, Data: instanceID}, nil
}

func (n *nebiusPoolBackend) createInstance(ctx context.Context, name string) (string, error) {
	labels := n.baseLabels()
	// Prepare cloud-init user data
	var userData string
	agentConfig := n.cfg.AgentConfigContent
	version := n.desiredVersion()

	// Build runcmd list
	runcmds := []string{}

	// Add Tailscale installation and configuration if tailscale_config is set
	if tsConfig := n.cfg.GetTailscaleConfig(); tsConfig != nil && tsConfig.GetServer() != "" && tsConfig.GetPreAuthKey() != "" {
		tailscaleCmd := "curl -fsSL https://tailscale.com/install.sh | sh"
		runcmds = append(runcmds, tailscaleCmd)

		// Build tailscale up command with server and auth key
		tailscaleUpCmd := fmt.Sprintf("tailscale up --login-server=%s --authkey=%s --accept-routes",
			tsConfig.GetServer(),
			tsConfig.GetPreAuthKey())
		runcmds = append(runcmds, tailscaleUpCmd)
	}

	// Add shared filesystem mount commands
	bootcmds := []string{
		"mkdir -p /run/velda",
		fmt.Sprintf("cat << EOF > /run/velda/velda.yaml\n%s\nEOF", agentConfig),
		"nvidia-smi",
	}
	for i, fs := range n.cfg.GetFilesystems() {
		if fs.GetFilesystemId() == "" || fs.GetMountPath() == "" {
			continue
		}
		bootcmds = append(bootcmds, fmt.Sprintf("mkdir -p %s", fs.GetMountPath()))
		bootcmds = append(bootcmds, fmt.Sprintf("mount -t virtiofs fs-%d %s", i, fs.GetMountPath()))
	}

	runcmds = append(runcmds,
		"curl -fsSL https://releases.velda.io/nvidia-collect.sh -o /tmp/nvidia-collect.sh && bash /tmp/nvidia-collect.sh",
		fmt.Sprintf("[ \"$(/bin/velda version || true)\" != \"%s\" ] && curl -fsSL https://releases.velda.io/velda-%s-linux-amd64 -o /tmp/velda && chmod +x /tmp/velda && mv /tmp/velda /bin/velda || true", version, version),
		"[ ! -e /usr/lib/systemd/system/velda-agent.service ] && curl -fsSL https://releases.velda.io/velda-agent.service -o /usr/lib/systemd/system/velda-agent.service && systemctl daemon-reload && systemctl enable velda-agent.service && systemctl start velda-agent.service",
	)
	cloudInitConfig := map[string]interface{}{
		"bootcmd": bootcmds,
		"runcmd":  runcmds,
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

	diskName := name + "-boot-disk"
	log.Printf("Creating new boot disk %s", diskName)
	diskSpec := &compute.DiskSpec{
		Size:           &compute.DiskSpec_SizeGibibytes{SizeGibibytes: diskSizeGb},
		BlockSizeBytes: 4096,
		Type:           diskTypeFromProto(n.cfg.GetBootDiskType()),
		Source: &compute.DiskSpec_SourceImageFamily{
			SourceImageFamily: &compute.SourceImageFamily{
				ImageFamily: "ubuntu24.04-cuda12",
			},
		},
	}
	if diskSpec.GetType() != compute.DiskSpec_NETWORK_SSD {
		// Enable encryption
		diskSpec.DiskEncryption = &compute.DiskEncryption{
			Type: compute.DiskEncryption_DISK_ENCRYPTION_MANAGED,
		}
	}

	diskOp, err := n.diskService.Create(ctx, &compute.CreateDiskRequest{
		Metadata: &common.ResourceMetadata{
			ParentId: n.cfg.GetParentId(),
			Name:     diskName,
			Labels:   labels,
		},
		Spec: diskSpec,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create boot disk: %w", err)
	}
	_, err = diskOp.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create boot disk: %w", err)
	}
	diskId := diskOp.ResourceID()
	networkSpec := &compute.NetworkInterfaceSpec{
		Name:      "eth0",
		IpAddress: &compute.IPAddress{},
		SubnetId:  n.cfg.GetSubnetId(),
	}
	if n.cfg.GetUsePublicIp() {
		networkSpec.PublicIpAddress = &compute.PublicIPAddress{}
	}

	// Build attached filesystem specs
	var attachedFilesystems []*compute.AttachedFilesystemSpec
	for id, fs := range n.cfg.GetFilesystems() {
		if fs.GetFilesystemId() == "" {
			continue
		}
		attachedFilesystems = append(attachedFilesystems, &compute.AttachedFilesystemSpec{
			AttachMode: compute.AttachedFilesystemSpec_READ_WRITE,
			Type: &compute.AttachedFilesystemSpec_ExistingFilesystem{
				ExistingFilesystem: &compute.ExistingFilesystem{
					Id: fs.GetFilesystemId(),
				},
			},
			MountTag: fmt.Sprintf("fs-%d", id),
		})
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
					Id: diskId,
				},
			},
		},
		Filesystems:       attachedFilesystems,
		CloudInitUserData: userData,
		Hostname:          name,
	}

	// Add preemptible settings if configured
	if n.cfg.GetPreemptible() {
		instanceSpec.Preemptible = &compute.PreemptibleSpec{}
	}

	log.Printf("Creating Nebius instance %s", name)
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
	return operation.ResourceID(), nil
}

// DeleteWorker implements SyncBackend interface
func (n *nebiusPoolBackend) DeleteWorker(ctx context.Context, workerName string, workerInfo backends.WorkerInfo) error {
	instanceID, err := n.instanceIDFromWorker(ctx, workerName, workerInfo)
	if err != nil {
		return err
	}
	instance, err := n.instanceService.Get(ctx, &compute.GetInstanceRequest{Id: instanceID})
	if err != nil {
		return err
	}
	return n.terminateInstanceAndDeleteBootDisk(ctx, instanceID, getInstanceBootDiskID(instance), workerName)
}

func (n *nebiusPoolBackend) instanceIDFromWorker(ctx context.Context, workerName string, workerInfo backends.WorkerInfo) (string, error) {
	if instanceID, ok := workerInfo.Data.(string); ok && instanceID != "" {
		return instanceID, nil
	}
	instance, err := n.instanceService.GetByName(ctx, &common.GetByNameRequest{
		ParentId: n.cfg.GetParentId(),
		Name:     workerName,
	})
	if err != nil {
		return "", err
	}
	return instance.GetMetadata().GetId(), nil
}

func getInstanceBootDiskID(instance *compute.Instance) string {
	if instance == nil || instance.GetSpec() == nil || instance.GetSpec().GetBootDisk() == nil {
		return ""
	}
	if disk := instance.GetSpec().GetBootDisk().GetExistingDisk(); disk != nil {
		return disk.GetId()
	}
	return ""
}

func (n *nebiusPoolBackend) terminateInstanceAndDeleteBootDisk(ctx context.Context, instanceID, bootDiskID, workerName string) error {
	log.Printf("Terminating Nebius instance %s (%s)", instanceID, workerName)
	operation, err := n.instanceService.Delete(ctx, &compute.DeleteInstanceRequest{Id: instanceID})
	if err != nil {
		return err
	}
	if _, err = operation.Wait(ctx); err != nil {
		return err
	}
	if bootDiskID == "" {
		return nil
	}

	log.Printf("Deleting disk %s for instance %s", bootDiskID, instanceID)
	operation, err = n.diskService.Delete(ctx, &compute.DeleteDiskRequest{Id: bootDiskID})
	if err != nil {
		return err
	}
	if _, err = operation.Wait(ctx); err != nil {
		return err
	}
	return nil
}

// SuspendWorker implements backends.Resumable interface
func (n *nebiusPoolBackend) SuspendWorker(ctx context.Context, workerName string, activeWorker backends.WorkerInfo) error {
	instanceID, err := n.instanceIDFromWorker(ctx, workerName, activeWorker)
	if err != nil {
		return err
	}

	log.Printf("Stopping Nebius instance %s (%s) for reuse", instanceID, workerName)
	op, err := n.instanceService.Stop(ctx, &compute.StopInstanceRequest{Id: instanceID})
	if err != nil {
		return err
	}
	_, err = op.Wait(ctx)
	return err
}

// ResumeWorker implements backends.Resumable interface
func (n *nebiusPoolBackend) ResumeWorker(ctx context.Context, workerName string, suspendedWorker backends.WorkerInfo) error {
	instanceID, err := n.instanceIDFromWorker(ctx, workerName, suspendedWorker)
	if err != nil {
		return err
	}

	log.Printf("Starting suspended Nebius instance %s (%s)", instanceID, workerName)
	op, err := n.instanceService.Start(ctx, &compute.StartInstanceRequest{Id: instanceID})
	if err != nil {
		return err
	}
	_, err = op.Wait(ctx)
	return err
}

type stoppedInstanceCleanup struct {
	name       string
	instanceID string
	bootDiskID string
}

// ListRemoteWorkers implements SyncBackend interface
func (n *nebiusPoolBackend) ListRemoteWorkers(ctx context.Context) (map[string]backends.WorkerInfo, error) {
	listReq := &compute.ListInstancesRequest{ParentId: n.cfg.GetParentId()}
	workers := make(map[string]backends.WorkerInfo)
	stoppedToCleanup := make([]stoppedInstanceCleanup, 0)
	expectedConfigHash := n.currentConfigHash()

	for instance, err := range n.instanceService.Filter(ctx, listReq) {
		if err != nil {
			log.Printf("Failed to filter instances: %v", err)
			return nil, err
		}
		metadata := instance.GetMetadata()
		status := instance.GetStatus()
		instanceName := metadata.GetName()
		instanceID := metadata.GetId()

		if status.GetState() == compute.InstanceStatus_DELETING {
			continue
		}

		// Client-side label/tag filtering by configured labels only.
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

		if status.GetState() == compute.InstanceStatus_STOPPED {
			instConfigHash := metadata.GetLabels()[configHashLabel]
			if instConfigHash != expectedConfigHash {
				stoppedToCleanup = append(stoppedToCleanup, stoppedInstanceCleanup{
					name:       instanceName,
					instanceID: instanceID,
					bootDiskID: getInstanceBootDiskID(instance),
				})
				continue
			}
			workers[instanceName] = backends.WorkerInfo{State: backends.WorkerStateSuspended, Data: instanceID}
			continue
		}

		workers[instanceName] = backends.WorkerInfo{State: backends.WorkerStateActive, Data: instanceID}
	}

	if len(stoppedToCleanup) > 0 {
		go func(cleanupItems []stoppedInstanceCleanup) {
			cleanupCtx := context.WithoutCancel(ctx)
			for _, item := range cleanupItems {
				log.Printf("Deleting stopped Nebius instance %s (%s) due to config hash mismatch (%s)", item.instanceID, item.name, configHashLabel)
				if err := n.terminateInstanceAndDeleteBootDisk(cleanupCtx, item.instanceID, item.bootDiskID, item.name); err != nil {
					log.Printf("Failed to delete mismatched stopped instance %s (%s): %v", item.instanceID, item.name, err)
				}
			}
		}(stoppedToCleanup)
	}

	return workers, nil
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

	var cfgOption gosdk.Option
	if key := os.Getenv("NEBIUS_SERVICE_ACCOUNT_KEY"); key != "" {
		cfgOption = gosdk.WithCredentials(
			gosdk.ServiceAccountReader(auth.NewServiceAccountCredentialsFileParser(nil, key)))
	} else {
		cfgReader := reader.NewConfigReader(reader.WithoutBrowserOpen())
		if err := cfgReader.Load(ctx); err != nil {
			return nil, fmt.Errorf("failed to load Nebius config: %w", err)
		}
		cfgOption = gosdk.WithConfigReader(cfgReader)
	}
	sdk, err := gosdk.New(ctx, cfgOption)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Nebius SDK: %w", err)
	}

	return NewNebiusPoolBackend(nebiusTemplate, sdk), nil
}

func init() {
	backends.Register(&nebiusLaunchTemplatePoolFactory{})
}
