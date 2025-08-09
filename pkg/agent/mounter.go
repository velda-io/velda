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
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"strconv"
	"syscall"

	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
	"velda.io/velda/pkg/utils"
)

type SimpleMounter struct {
	sandboxConfig *agentpb.SandboxConfig
}

func NewSimpleMounter(sandboxConfig *agentpb.SandboxConfig) *SimpleMounter {
	return &SimpleMounter{
		sandboxConfig: sandboxConfig,
	}
}

func (m *SimpleMounter) Mount(ctx context.Context, session *proto.SessionRequest, workspaceDir string) (cleanup func(), err error) {
	shardId := strconv.FormatInt(session.InstanceId>>utils.ShardOffset, 16)
	instanceId := strconv.FormatInt(session.InstanceId&utils.ShardMask, 10)
	switch s := m.sandboxConfig.GetDiskSource().GetSource().(type) {
	case *agentpb.AgentDiskSource_MountedDiskSource_:
		// Mount disk to workspace
		disk := path.Join(s.MountedDiskSource.LocalPath, shardId, instanceId)

		if err := syscall.Mount(disk, workspaceDir, "bind", syscall.MS_BIND, ""); err != nil {
			return nil, fmt.Errorf("Mount bind disk: %w", err)
		}
		if err := syscall.Mount("", workspaceDir, "", syscall.MS_REC|syscall.MS_SHARED, ""); err != nil {
			return nil, fmt.Errorf("Remount workspace: %w", err)
		}
		return nil, nil

	case *agentpb.AgentDiskSource_NfsMountSource_:
		// Mount NFS disk to workspace
		nfsSource := s.NfsMountSource
		nfsMount := session.AgentSessionInfo.GetNfsMount()
		if nfsMount == nil {
			return nil, fmt.Errorf("NFS mount info is not provided in session request")
		}
		nfsPath := fmt.Sprintf("%s:%s", nfsMount.NfsServer, nfsMount.NfsPath)
		option := nfsSource.MountOptions
		cmd := exec.CommandContext(ctx, "mount", "-t", "nfs", "-o", option, nfsPath, workspaceDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("Mount NFS disk: %v, output: %s", err, output)
		}
		return nil, nil

	default:
		return nil, fmt.Errorf("Unsupported oss disk source: %T", s)
	}
}
