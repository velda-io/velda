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
	defer func() {
		if err == nil {
			if remountErr := syscall.Mount("", workspaceDir, "", syscall.MS_REC|syscall.MS_SHARED, ""); remountErr != nil {
				err = fmt.Errorf("Remount workspace: %w", remountErr)
				if cleanup != nil {
					cleanup()
				}
			}
		}
	}()
	// If snapshot is specified, use overlay approach
	if session.SnapshotName != "" {
		return m.mountWithSnapshot(ctx, session, workspaceDir)
	}

	// Otherwise, mount the current version
	return m.mountInternal(ctx, session, workspaceDir, mountTypeCurrent, "", "")
}

// mountType specifies the type of mount operation to perform
type mountType int

const (
	mountTypeCurrent  mountType = iota // Mount current version
	mountTypeSnapshot                  // Mount snapshot
)

// mountInternal is a unified function for mounting with optional veldafs wrapper
// Parameters:
//   - ctx: context for the operation
//   - session: session request containing mount information
//   - targetDir: directory where the filesystem should be mounted
//   - mType: type of mount (current or snapshot)
//   - snapshotName: name of snapshot (only used when mType is mountTypeSnapshot)
//   - instanceSuffix: suffix for instance name when using veldafs (e.g., "current", "snapshot")
func (m *SimpleMounter) mountInternal(ctx context.Context, session *proto.SessionRequest, targetDir string, mType mountType, snapshotName, instanceSuffix string) (cleanup func(), err error) {
	useCAS := m.sandboxConfig.GetDiskSource().CasConfig != nil

	if useCAS {
		useDirectFS := m.sandboxConfig.GetDiskSource().GetCasConfig().GetUseDirectProtocol()

		// Mount with veldafs wrapper
		dataDir := path.Join(path.Dir(targetDir), path.Base(targetDir)+"_data")

		var baseCleanup func()
		var mountErr error

		// Skip base mount if using DirectFS for snapshots
		if useDirectFS && mType == mountTypeSnapshot {
			// DirectFS will directly mount from the server, no need for base mount
			baseCleanup = nil
			mountErr = nil
			dataDir = fmt.Sprintf("%s:7655@%s/.zfs/snapshot/%s", session.AgentSessionInfo.GetNfsMount().NfsServer, session.AgentSessionInfo.GetNfsMount().NfsPath, snapshotName)
		} else {
			if err := os.MkdirAll(dataDir, 0755); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", dataDir, err)
			}
			if mType == mountTypeSnapshot {
				baseCleanup, mountErr = m.mountDirect(ctx, session, dataDir, true, snapshotName)
			} else {
				baseCleanup, mountErr = m.mountDirect(ctx, session, dataDir, false, "")
			}

			if mountErr != nil {
				return nil, mountErr
			}
		}

		// Mount CAS driver on top
		instanceName := fmt.Sprintf("instance-%d", session.InstanceId)
		if instanceSuffix != "" {
			instanceName = fmt.Sprintf("instance-%d-%s", session.InstanceId, instanceSuffix)
		}

		casCleanup, err := m.runVeldafsWrapper(ctx, dataDir, instanceName, targetDir, mType)
		if err != nil {
			if baseCleanup != nil {
				baseCleanup()
			}
			return nil, fmt.Errorf("mount veldafs: %w", err)
		}

		return func() {
			if casCleanup != nil {
				casCleanup()
			}
			if baseCleanup != nil {
				baseCleanup()
			}
		}, nil
	} else {
		// Direct mount without CAS
		if mType == mountTypeSnapshot {
			return m.mountDirect(ctx, session, targetDir, true, snapshotName)
		}
		return m.mountDirect(ctx, session, targetDir, false, "")
	}
}

// mountDirect mounts the filesystem directly without veldafs wrapper
// Parameters:
//   - isSnapshot: if true, mount a snapshot; if false, mount current version
//   - snapshotName: name of snapshot (only used when isSnapshot is true)
func (m *SimpleMounter) mountDirect(ctx context.Context, session *proto.SessionRequest, targetDir string, isSnapshot bool, snapshotName string) (cleanup func(), err error) {
	switch s := m.sandboxConfig.GetDiskSource().GetSource().(type) {
	case *agentpb.AgentDiskSource_MountedDiskSource_:
		var sourcePath string
		if isSnapshot {
			disk := fmt.Sprintf("%s/%d/root", s.MountedDiskSource.GetLocalPath(), session.InstanceId)
			sourcePath = path.Join(disk, ".zfs/snapshot", snapshotName)
		} else {
			sourcePath = fmt.Sprintf("%s/%d/root", s.MountedDiskSource.GetLocalPath(), session.InstanceId)
		}

		mountFlags := uintptr(syscall.MS_BIND)
		if isSnapshot {
			mountFlags |= syscall.MS_RDONLY
		}

		if err := syscall.Mount(sourcePath, targetDir, "bind", mountFlags, ""); err != nil {
			if isSnapshot {
				return nil, fmt.Errorf("mount snapshot: %w", err)
			}
			return nil, fmt.Errorf("mount bind disk: %w", err)
		}

		if isSnapshot {
			return func() {
				syscall.Unmount(targetDir, syscall.MNT_DETACH)
			}, nil
		}
		return nil, nil

	case *agentpb.AgentDiskSource_NfsMountSource_:
		nfsSource := s.NfsMountSource
		nfsMount := session.AgentSessionInfo.GetNfsMount()
		if nfsMount == nil {
			return nil, fmt.Errorf("NFS mount info is not provided in session request")
		}

		var nfsPath, option string
		if isSnapshot {
			nfsPathWithSnapshot := path.Join(nfsMount.NfsPath, ".zfs/snapshot", snapshotName)
			nfsPath = fmt.Sprintf("%s:%s", nfsMount.NfsServer, nfsPathWithSnapshot)
			option = nfsSource.SnapshotMountOptions
		} else {
			option = nfsSource.MountOptions
			nfsPath = fmt.Sprintf("%s:%s", nfsMount.NfsServer, nfsMount.NfsPath)
		}

		cmd := exec.CommandContext(ctx, "mount", "-t", "nfs", "-o", option, nfsPath, targetDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			if isSnapshot {
				return nil, fmt.Errorf("mount NFS snapshot: %v, output: %s", err, output)
			}
			return nil, fmt.Errorf("mount NFS disk: %v, output: %s", err, output)
		}

		if isSnapshot {
			return func() {
				syscall.Unmount(targetDir, syscall.MNT_DETACH)
			}, nil
		}
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported disk source: %T", s)
	}
}

// mount wraps mountInternal for backward compatibility
func (m *SimpleMounter) mount(ctx context.Context, session *proto.SessionRequest, workspaceDir string) (cleanup func(), err error) {
	return m.mountInternal(ctx, session, workspaceDir, mountTypeCurrent, "", "")
}

func (m *SimpleMounter) runVeldafsWrapper(ctx context.Context, disk, name, workspaceDir string, mode mountType) (cleanup func(), err error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("Get executable: %w", err)
	}

	args := []string{
		"agent",
		"sandboxfs",
		"--readyfd=3",
		"--name",
		name,
	}

	if m.sandboxConfig.GetDiskSource().GetCasConfig().GetCasCacheDir() != "" {
		args = append(args, "--cache-dir", m.sandboxConfig.GetDiskSource().GetCasConfig().GetCasCacheDir())
		if mode == mountTypeSnapshot {
			if m.sandboxConfig.GetDiskSource().GetCasConfig().GetUseDirectProtocol() {
				// Use DirectFS mode with NFS server address
				args = append(args, "--mode", "directfs-snapshot")
			} else {
				args = append(args, "--mode", "snapshot")
			}
		}
	} else {
		args = append(args, "--mode", "nocache")
	}
	args = append(args, disk, workspaceDir)

	fuseCmd := exec.Command(executable, args...)
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

// mountWithSnapshot mounts the filesystem with snapshot support using overlayfs
func (m *SimpleMounter) mountWithSnapshot(ctx context.Context, session *proto.SessionRequest, workspaceDir string) (cleanup func(), err error) {
	agentDir := path.Dir(workspaceDir)
	baseDir := path.Join(agentDir, "base")
	curDir := path.Join(agentDir, "cur")
	upperDir := path.Join(agentDir, "upper")
	workDir := path.Join(agentDir, "work")

	var cleanupFuncs []func()
	defer func() {
		if err != nil {
			// Cleanup on error
			for i := len(cleanupFuncs) - 1; i >= 0; i-- {
				cleanupFuncs[i]()
			}
		}
	}()

	// Create necessary directories
	for _, dir := range []string{baseDir, curDir, upperDir, workDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Step 1: Mount snapshot as base
	baseCleanup, err := m.mountInternal(ctx, session, baseDir, mountTypeSnapshot, session.SnapshotName, "snapshot")
	if err != nil {
		return nil, fmt.Errorf("mount base snapshot: %w", err)
	}
	if baseCleanup != nil {
		cleanupFuncs = append(cleanupFuncs, baseCleanup)
	}

	// Step 2: Mount current version as cur
	curCleanup, err := m.mountInternal(ctx, session, curDir, mountTypeCurrent, "", "current")
	if err != nil {
		return nil, fmt.Errorf("mount current version: %w", err)
	}
	if curCleanup != nil {
		cleanupFuncs = append(cleanupFuncs, curCleanup)
	}

	// Step 3: Mount overlay of base to workspace
	overlayOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", baseDir, upperDir, workDir)
	if err := syscall.Mount("overlay", workspaceDir, "overlay", 0, overlayOpts); err != nil {
		return nil, fmt.Errorf("mount overlay: %w", err)
	}
	cleanupFuncs = append(cleanupFuncs, func() {
		syscall.Unmount(workspaceDir, syscall.MNT_DETACH)
	})

	if err := syscall.Mount("", workspaceDir, "", syscall.MS_REC|syscall.MS_SHARED, ""); err != nil {
		return nil, fmt.Errorf("remount workspace: %w", err)
	}

	// Step 4: Bind mount each writable subdir from cur to workspace
	if len(session.WritableDirs) > 0 {
		bindCleanup, err := m.bindWritableDirs(session, workspaceDir, curDir)
		if err != nil {
			return nil, fmt.Errorf("bind writable dirs: %w", err)
		}
		if bindCleanup != nil {
			cleanupFuncs = append(cleanupFuncs, bindCleanup)
		}
	}

	return func() {
		for i := len(cleanupFuncs) - 1; i >= 0; i-- {
			cleanupFuncs[i]()
		}
		// Clean up temporary directories
		os.RemoveAll(upperDir)
		os.RemoveAll(workDir)
	}, nil
}

// bindWritableDirs bind mounts writable directories from cur to workspace
func (m *SimpleMounter) bindWritableDirs(session *proto.SessionRequest, workspaceDir, curDir string) (cleanup func(), err error) {
	var cleanupFuncs []func()
	defer func() {
		if err != nil {
			// Cleanup on error
			for i := len(cleanupFuncs) - 1; i >= 0; i-- {
				cleanupFuncs[i]()
			}
		}
	}()

	for _, writableDir := range session.WritableDirs {
		// Skip root directory
		if writableDir == "/" {
			continue
		}

		// Source path in cur
		source := path.Join(curDir, writableDir)

		// Target path in workspace
		target := path.Join(workspaceDir, writableDir)

		// Ensure source directory exists
		if err := os.MkdirAll(source, 0755); err != nil {
			return nil, fmt.Errorf("mkdir source %s: %w", source, err)
		}

		// Ensure target directory exists
		if err := os.MkdirAll(target, 0755); err != nil {
			return nil, fmt.Errorf("mkdir target %s: %w", target, err)
		}

		// Bind mount the writable directory from cur
		if err := syscall.Mount(source, target, "bind", syscall.MS_BIND, ""); err != nil {
			return nil, fmt.Errorf("mount bind %s to %s: %w", source, target, err)
		}

		cleanupFuncs = append(cleanupFuncs, func() {
			syscall.Unmount(target, syscall.MNT_DETACH)
		})
	}

	return func() {
		for i := len(cleanupFuncs) - 1; i >= 0; i-- {
			cleanupFuncs[i]()
		}
	}, nil
}
