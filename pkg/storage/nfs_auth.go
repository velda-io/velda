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
package storage

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"velda.io/velda/pkg/proto"
)

// LocalNfsAuth is a common NfsAuth implementation for local filesystem based
// storage backends (btrfs, zfs, mini).  It uses the Linux exportfs(8) tool to
// manage NFS exports under /etc/exports.d/.
type LocalNfsAuth struct {
	disk LocalDiskProvider
}

// NewLocalNfsAuth creates a LocalNfsAuth backed by the given LocalDiskProvider.
// It ensures that /etc/exports.d exists, creating it if necessary.
func NewLocalNfsAuth(disk LocalDiskProvider) (*LocalNfsAuth, error) {
	if _, err := os.Stat("/etc/exports.d"); os.IsNotExist(err) {
		cmd := runAsRootCommand("mkdir", "-p", "/etc/exports.d")
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to create exports directory: %w", err)
		}
	}
	return &LocalNfsAuth{disk: disk}, nil
}

// GrantNfsAccess exports the instance (and optional snapshot) path via NFS for
// agentHost, then patches req.AgentSessionInfo with the resulting mount details.
func (n *LocalNfsAuth) GrantNfsAccess(ctx context.Context, req *proto.SessionRequest, agentHost, nfsServer string) error {
	instanceID := req.InstanceId
	exportPath := n.disk.GetRoot(instanceID)
	paths := []string{exportPath}

	var snapshotPath string
	if req.SnapshotName != "" {
		snapshotPath = n.disk.GetSnapshotRoot(instanceID, req.SnapshotName)
		paths = append(paths, snapshotPath)
	}

	if err := exportNFS(paths, agentHost, req.InstanceId, req.SessionId); err != nil {
		return fmt.Errorf("failed to export NFS: %w", err)
	}

	req.AgentSessionInfo = &proto.AgentSessionInfo{
		FileMount: &proto.AgentSessionInfo_NfsMount_{
			NfsMount: &proto.AgentSessionInfo_NfsMount{
				NfsServer:       nfsServer,
				NfsPath:         exportPath,
				NfsSnapshotPath: snapshotPath,
			},
		},
	}
	return nil
}

// RevokeNfsAccess removes the NFS export that was created for the given instance
// and session.
func (n *LocalNfsAuth) RevokeNfsAccess(ctx context.Context, instanceId int64, sessionId string) error {
	// TODO: Add reference counting for same peer IP.
	exportPath := n.disk.GetRoot(instanceId)
	if err := unexportNFS(exportPath, instanceId, sessionId); err != nil {
		return err
	}
	return nil
}

// exportNFS writes an exports(5) file for the given paths/host and reloads exportfs.
func exportNFS(paths []string, host string, instanceId int64, sessionId string) error {
	exportData := ""
	for _, p := range paths {
		exportData += fmt.Sprintf("%s %s(rw,async,no_root_squash,no_subtree_check)\n", p, host)
	}
	file := nfsExportFile(instanceId, sessionId)
	cmd := runAsRootCommand("sh", "-c", fmt.Sprintf("cat > %s", file))
	cmd.Stdin = strings.NewReader(exportData)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to write NFS export file for %s: %s", host, out)
		return fmt.Errorf("failed to write NFS export file: %w", err)
	}
	cmd = runAsRootCommand("exportfs", "-ar")
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to export NFS %s for host %s: %s", paths, host, out)
		return fmt.Errorf("failed to export NFS: %w", err)
	}
	return nil
}

// unexportNFS removes the exports file for the given instance/session and reloads exportfs.
func unexportNFS(exportPath string, instanceId int64, sessionId string) error {
	file := nfsExportFile(instanceId, sessionId)
	cmd := runAsRootCommand("rm", file)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to remove NFS export file for instance %d session %s: %s", instanceId, sessionId, out)
		// Ignore error; the file may have already been removed.
	}
	cmd = runAsRootCommand("exportfs", "-ar")
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to unexport NFS %s: %s", exportPath, out)
		return fmt.Errorf("failed to unexport NFS: %w", err)
	}
	return nil
}

func nfsExportFile(instanceId int64, sessionId string) string {
	return fmt.Sprintf("/etc/exports.d/velda-%d-%s.exports", instanceId, sessionId)
}

// runAsRootCommand returns an *exec.Cmd that runs the given command directly
// when already running as root, or prepends "sudo" otherwise.
func runAsRootCommand(name string, args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		return exec.Command(name, args...)
	}
	newArgs := append([]string{name}, args...)
	return exec.Command("sudo", newArgs...)
}
