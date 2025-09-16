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
package broker

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"

	"velda.io/velda/pkg/proto"
)

type LocalDiskProvider interface {
	GetRoot(instanceId int64) string
}

type NfsExportAuth struct {
	disk LocalDiskProvider
}

func NewNfsExportAuth(disk LocalDiskProvider) (*NfsExportAuth, error) {
	if _, err := os.Stat("/etc/exports.d"); os.IsNotExist(err) {
		cmd := exec.Command("sudo", "mkdir", "-p", "/etc/exports.d")
		err := cmd.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to create exports directory: %w", err)
		}
	}
	return &NfsExportAuth{
		disk: disk,
	}, nil
}

func (n *NfsExportAuth) GrantAccessToAgent(ctx context.Context, agent *Agent, session *Session) error {
	instanceID := session.Request.InstanceId
	exportPath := n.disk.GetRoot(instanceID)
	agentHost := agent.Host

	// Use exportfs command to export NFS
	err := exportNFS(exportPath, agentHost.String(), session)
	if err != nil {
		return err
	}
	session.Request.AgentSessionInfo = &proto.AgentSessionInfo{
		FileMount: &proto.AgentSessionInfo_NfsMount_{
			NfsMount: &proto.AgentSessionInfo_NfsMount{
				NfsServer: agent.PeerInfo.LocalAddr.(*net.TCPAddr).IP.String(),
				NfsPath:   exportPath,
			},
		},
	}
	return nil
}

// exportNFS is a helper function to handle NFS export logic using exportfs command
func exportNFS(path, host string, session *Session) error {
	log.Printf("Exporting NFS path %s to host %s", path, host)
	cmd := exec.Command("sudo", "exportfs", "-o", "rw,async,no_root_squash,subtree_check", host+":"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to export NFS %s for host %s: %s", path, host, out)
		return fmt.Errorf("failed to export NFS: %w", err)
	}
	cmd = exec.Command("sudo", "sh", "-c", "echo '"+path+" "+host+"(rw,async,no_root_squash)' > "+exportFile(session))
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to write NFS export file for %s: %s", host, out)
		return fmt.Errorf("failed to write NFS export file: %w", err)
	}
	return nil
}

func (n *NfsExportAuth) RevokeAccessToAgent(ctx context.Context, agent *Agent, session *Session) error {
	// TODO: Add reference counting for same peer IP.
	instanceID := session.Request.InstanceId
	exportPath := n.disk.GetRoot(instanceID)
	agentHost := agent.Host

	// Use exportfs command to unexport NFS
	err := unexportNFS(exportPath, agentHost.String(), session)
	if err != nil {
		return err
	}
	return nil
}

func exportFile(s *Session) string {
	return fmt.Sprintf("/etc/exports.d/velda-%d-%s.exports", s.Request.InstanceId, s.Request.SessionId)
}

// unexportNFS is a helper function to handle NFS unexport logic using exportfs command
func unexportNFS(path, host string, session *Session) error {
	log.Printf("Unexporting NFS path %s for host %s", path, host)
	cmd := exec.Command("sudo", "exportfs", "-u", host+":"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to unexport NFS %s for host %s: %s", path, host, out)
		return fmt.Errorf("failed to unexport NFS: %w", err)
	}
	cmd = exec.Command("sudo", "rm", exportFile(session))
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to remove NFS export file for %s: %s", host, out)
		// Ignore error for now.
	}
	return nil
}

func (n *NfsExportAuth) GrantAccessToClient(ctx context.Context, session *Session, status *proto.ExecutionStatus) error {
	return nil
}

func (n *NfsExportAuth) UpdateServerInfo(ctx context.Context, info *proto.ServerInfo) error {
	return nil
}
