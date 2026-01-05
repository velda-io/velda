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
	"os"
	"os/exec"
	"path"
	"syscall"

	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
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
	if m.sandboxConfig.GetDiskSource().CasConfig != nil {
		dataDir := path.Join(path.Dir(workspaceDir), "worksource_base")

		if err := os.Mkdir(dataDir, 0755); err != nil && !os.IsExist(err) {
			return nil, fmt.Errorf("Mkdir workspace: %w", err)
		}
		cleanup, err := m.mount(ctx, session, dataDir)
		if err != nil {
			return nil, err
		}

		// Mount CAS driver on top of dataDir
		newCleanup, err := m.runVeldafsWrapper(ctx, dataDir, fmt.Sprintf("instance-%d", session.InstanceId), workspaceDir)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("Mount veldafs: %w", err)
		}
		if cleanup != nil {
			return func() {
				newCleanup()
				cleanup()
			}, nil
		} else {
			return newCleanup, nil
		}
	} else {
		return m.mount(ctx, session, workspaceDir)
	}
}

func (m *SimpleMounter) mount(ctx context.Context, session *proto.SessionRequest, workspaceDir string) (cleanup func(), err error) {
	switch s := m.sandboxConfig.GetDiskSource().GetSource().(type) {
	case *agentpb.AgentDiskSource_MountedDiskSource_:
		// Mount disk to workspace
		disk := fmt.Sprintf("%s/%d/root", s.MountedDiskSource.GetLocalPath(), session.InstanceId)

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
		if err := syscall.Mount("", workspaceDir, "", syscall.MS_REC|syscall.MS_SHARED, ""); err != nil {
			return nil, fmt.Errorf("Remount workspace: %w", err)
		}
		return nil, nil

	default:
		return nil, fmt.Errorf("Unsupported oss disk source: %T", s)
	}
}

func (m *SimpleMounter) runVeldafsWrapper(ctx context.Context, disk, name, workspaceDir string) (cleanup func(), err error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("Get executable: %w", err)
	}
	fuseCmd := exec.Command(
		executable,
		"agent",
		"sandboxfs",
		"--readyfd=3",
		"--cache-dir",
		m.sandboxConfig.GetDiskSource().GetCasConfig().GetCasCacheDir(),
		"--name",
		name,
		disk,
		workspaceDir)
	fuseCmd.Stderr = os.Stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("Pipe: %w", err)
	}
	fuseCmd.ExtraFiles = []*os.File{pw}
	if err := fuseCmd.Start(); err != nil {
		return nil, fmt.Errorf("Start fuse: %w", err)
	}
	pw.Close()
	_, err = pr.Read(make([]byte, 1))
	if err != nil {
		fuseCmd.Process.Kill()
		return nil, fmt.Errorf("Read readyfd: %w", err)
	}
	if err := syscall.Mount("", workspaceDir, "", syscall.MS_REC|syscall.MS_SHARED, ""); err != nil {
		fuseCmd.Process.Kill()
		return nil, fmt.Errorf("Remount workspace: %w", err)
	}
	return func() {
		fuseCmd.Process.Signal(syscall.SIGTERM)
		fuseCmd.Wait()
	}, nil
}
