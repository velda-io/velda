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
	"sync"

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

type nebiusPoolBackend struct {
	cfg             *proto.AutoscalerBackendNebiusLaunchTemplate
	sdk             *gosdk.SDK
	instanceService computeservice.InstanceService
	diskService     computeservice.DiskService

	// Cache for available disks
	diskPoolMu       sync.RWMutex
	diskPool         map[string]diskInfo
	lastScannedDisks map[string]diskInfo
}

type diskInfo struct {
	diskId   string
	diskName string
}

func NewNebiusPoolBackend(cfg *proto.AutoscalerBackendNebiusLaunchTemplate, sdk *gosdk.SDK) broker.ResourcePoolBackend {
	instanceService := sdk.Services().Compute().V1().Instance()
	diskService := sdk.Services().Compute().V1().Disk()

	backend := &nebiusPoolBackend{
		cfg:              cfg,
		sdk:              sdk,
		instanceService:  instanceService,
		diskService:      diskService,
		diskPool:         make(map[string]diskInfo),
		lastScannedDisks: make(map[string]diskInfo),
	}

	return backends.MakeAsync(backend)
}

// GenerateWorkerName implements SyncBackend interface
func (n *nebiusPoolBackend) GenerateWorkerName() string {
	return fmt.Sprintf("%s-%s", n.cfg.InstanceNamePrefix, utils.RandString(5))
}

// getAvailableDisk retrieves and removes a disk from the pool
func (n *nebiusPoolBackend) getAvailableDisk() *diskInfo {
	n.diskPoolMu.Lock()
	defer n.diskPoolMu.Unlock()

	if len(n.diskPool) == 0 {
		return nil
	}

	// Get the first available disk
	for _, disk := range n.diskPool {
		delete(n.diskPool, disk.diskId)
		return &disk
	}
	return nil
}

// getDiskPoolCount returns the current count of disks in the pool
func (n *nebiusPoolBackend) getDiskPoolCount() int {
	n.diskPoolMu.RLock()
	defer n.diskPoolMu.RUnlock()
	return len(n.diskPool)
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
	labels := make(map[string]string)

	for k, v := range n.cfg.GetLabels() {
		labels[k] = v
	}
	// Prepare cloud-init user data
	var userData string
	agentConfig := n.cfg.AgentConfigContent
	version := n.cfg.AgentVersionOverride
	if version == "" {
		version = velda.Version
	}

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

	runcmds = append(runcmds,
		"curl -fsSL https://releases.velda.io/nvidia-collect.sh -o /tmp/nvidia-collect.sh && bash /tmp/nvidia-collect.sh",
		fmt.Sprintf("[ \"$(/bin/velda version || true)\" != \"%s\" ] && curl -fsSL https://releases.velda.io/velda-%s-linux-amd64 -o /tmp/velda && chmod +x /tmp/velda && mv /tmp/velda /bin/velda || true", version, version),
		"[ ! -e /usr/lib/systemd/system/velda-agent.service ] && curl -fsSL https://releases.velda.io/velda-agent.service -o /usr/lib/systemd/system/velda-agent.service && systemctl daemon-reload && systemctl enable velda-agent.service && systemctl start velda-agent.service",
	)
	cloudInitConfig := map[string]interface{}{
		"bootcmd": []string{
			"mkdir -p /run/velda",
			fmt.Sprintf("cat << EOF > /run/velda/velda.yaml\n%s\nEOF", agentConfig),
			"nvidia-smi",
		},
		"runcmd": runcmds,
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

	// Try to use an available disk from the pool first
	pooledDisk := n.getAvailableDisk()
	var diskId string
	if pooledDisk != nil {
		log.Printf("Reusing pooled disk %s(%s) for new instance %s", pooledDisk.diskName, pooledDisk.diskId, name)
		diskId = pooledDisk.diskId
	}

	// If no pooled disk available or retrieval failed, create a new one
	if diskId == "" {
		diskName := utils.RandString(12) + "-boot-disk"
		log.Printf("Creating new boot disk %s", diskName)
		diskOp, err := n.diskService.Create(ctx, &compute.CreateDiskRequest{
			Metadata: &common.ResourceMetadata{
				ParentId: n.cfg.GetParentId(),
				Name:     diskName,
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
		_, err = diskOp.Wait(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to create boot disk: %w", err)
		}
		diskId = diskOp.ResourceID()
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
					Id: diskId,
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
	return n.performDelete(ctx, workerName)
}

func (n *nebiusPoolBackend) performDelete(ctx context.Context, workerName string) error {
	// WorkerName is the instance ID (we always use instance ID as the worker name)
	instance, err := n.instanceService.GetByName(ctx, &common.GetByNameRequest{
		ParentId: n.cfg.GetParentId(),
		Name:     workerName,
	})
	if err != nil {
		return err
	}

	instanceId := instance.Metadata.Id
	bootDiskId := instance.Spec.BootDisk.GetExistingDisk().GetId()

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
	// Try to pool the disk if we have room in the cache
	if n.cfg.MaxDiskPoolSize > 0 && int32(n.getDiskPoolCount()) < n.cfg.MaxDiskPoolSize {
		log.Printf("Pooling disk %s for reuse", bootDiskId)
		// Disk is automatically detached when instance is deleted
		// Add it to the pool for future use
		n.diskPoolMu.Lock()
		n.diskPool[bootDiskId] = diskInfo{
			diskId:   bootDiskId,
			diskName: instance.Metadata.Name + "-boot-disk",
		}
		n.diskPoolMu.Unlock()
		return nil
	}

	// Either pooling is disabled or cache is full, delete the disk
	log.Printf("Deleting disk %s", bootDiskId)
	operation, err = n.diskService.Delete(ctx, &compute.DeleteDiskRequest{
		Id: bootDiskId,
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

// ListRemoteWorkers implements SyncBackend interface
func (n *nebiusPoolBackend) ListRemoteWorkers(ctx context.Context) (map[string]backends.WorkerInfo, error) {
	listReq := &compute.ListInstancesRequest{
		ParentId: n.cfg.GetParentId(),
	}

	workers := make(map[string]backends.WorkerInfo)
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
		workers[instanceId] = backends.WorkerInfo{State: backends.WorkerStateActive, Data: instance.Metadata.Id}
	}

	// Scan for unattached disks to populate the disk pool
	if n.cfg.MaxDiskPoolSize > 0 {
		// TODO: This may have race conditions if a disk is being reused.
		go n.scanAndUpdateDiskPool(ctx)
	}
	return workers, nil
}

// scanAndUpdateDiskPool scans for unattached disks and updates the disk pool
func (n *nebiusPoolBackend) scanAndUpdateDiskPool(ctx context.Context) {
	disksFound := make(map[string]diskInfo)
	disksToDelete := make([]string, 0)

	// List all disks with matching labels
	listReq := &compute.ListDisksRequest{
		ParentId: n.cfg.GetParentId(),
	}

	for disk, err := range n.diskService.Filter(ctx, listReq) {
		if err != nil {
			log.Printf("Failed to filter disks: %v", err)
			return
		}

		metadata := disk.GetMetadata()
		status := disk.GetStatus()

		// Skip disks being deleted or created
		if status.GetState() == compute.DiskStatus_DELETING || status.GetState() == compute.DiskStatus_CREATING {
			continue
		}

		// Client-side label/tag filtering
		if len(n.cfg.GetLabels()) > 0 {
			diskLabels := metadata.GetLabels()
			matched := true
			for k, v := range n.cfg.GetLabels() {
				if lv, ok := diskLabels[k]; !ok || lv != v {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}

		// Only consider unattached disks (disks without instance attachment)
		if status.GetReadWriteAttachment() != "" || len(status.GetReadOnlyAttachments()) > 0 {
			continue
		}

		diskId := metadata.GetId()
		diskName := metadata.GetName()

		disksFound[diskId] = diskInfo{
			diskId:   diskId,
			diskName: diskName,
		}
	}

	// Update disk pool with what we found
	n.diskPoolMu.Lock()
	defer n.diskPoolMu.Unlock()

	// Add new found disks to the pool
	for id, disk := range disksFound {
		_, inPool := n.diskPool[id]
		if inPool {
			continue
		}
		_, existInLastScan := n.lastScannedDisks[id]
		if existInLastScan {
			// Was previously scanned, may already be used
			continue
		}
		if len(n.diskPool) >= int(n.cfg.MaxDiskPoolSize) {
			log.Printf("Max disk pool size reached (%d), deleting disk %s", n.cfg.MaxDiskPoolSize, id)
			disksToDelete = append(disksToDelete, id)
			continue
		}
		log.Printf("Adding unattached disk %s to pool", id)
		n.diskPool[id] = disk
	}

	// Remove disks from pool that are no longer unattached
	for id := range n.diskPool {
		_, found := disksFound[id]
		if !found {
			log.Printf("Removing disk %s from pool (no longer available)", id)
			delete(n.diskPool, id)
		}
	}

	n.lastScannedDisks = disksFound

	// Delete excess disks outside the lock
	if len(disksToDelete) > 0 {
		go func() {
			for _, diskId := range disksToDelete {
				operation, err := n.diskService.Delete(ctx, &compute.DeleteDiskRequest{
					Id: diskId,
				})
				if err != nil {
					log.Printf("Failed to delete excess disk %s: %v", diskId, err)
					continue
				}
				_, err = operation.Wait(ctx)
				if err != nil {
					log.Printf("Failed to wait for disk deletion %s: %v", diskId, err)
				}
			}
		}()
	}
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
