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
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"

	agentpb "velda.io/velda/pkg/proto/agent"
)

type LinuxNamespacePlugin struct {
	PluginBase
	WorkspaceDir  string
	SandboxConfig *agentpb.SandboxConfig
}

func printMountInfo() {
	f, err := os.Open("/proc/self/mountinfo")
	if err == nil {
		defer f.Close()
		data, _ := os.ReadFile("/proc/self/mountinfo")
		log.Printf("=== /proc/self/mountinfo ===\n%s\n============================", string(data))
	}
}

func die(descr string, err error) {
	log.Fatal(descr + ": " + err.Error())
}

func mkNod(name string, mode uint32, dev int) {
	if err := unix.Mknod(name, mode, dev); err != nil {
		die("Mknod "+name, err)
	}
	if err := unix.Chmod(name, mode&0777); err != nil {
		die("Chmod "+name, err)
	}
}

func setupDev(devDir string) {
	mkNod(path.Join(devDir, "null"), unix.S_IFCHR|0666, int(unix.Mkdev(1, 3)))
	mkNod(path.Join(devDir, "zero"), unix.S_IFCHR|0666, int(unix.Mkdev(1, 5)))
	mkNod(path.Join(devDir, "random"), unix.S_IFCHR|0666, int(unix.Mkdev(1, 8)))
	mkNod(path.Join(devDir, "urandom"), unix.S_IFCHR|0666, int(unix.Mkdev(1, 9)))
	mkNod(path.Join(devDir, "ptmx"), unix.S_IFCHR|0666, int(unix.Mkdev(5, 2)))
	mkNod(path.Join(devDir, "tty"), unix.S_IFCHR|0666, int(unix.Mkdev(5, 0)))
	mkNod(path.Join(devDir, "full"), unix.S_IFCHR|0666, int(unix.Mkdev(1, 7)))
	if err := unix.Mkdir(path.Join(devDir, "pts"), 0755); err != nil {
		die("Mkdir pts", err)
	}
	if err := unix.Mkdir(path.Join(devDir, "shm"), 0755); err != nil {
		die("Mkdir shm", err)
	}
	if err := unix.Symlink("/proc/self/fd", path.Join(devDir, "fd")); err != nil {
		die("Symlink fd", err)
	}

}

func (p *LinuxNamespacePlugin) Run(ctx context.Context) error {
	if sandboxRuntimeEnabled(p.SandboxConfig) {
		return p.RunNext(ctx)
	}
	p.setupMounts(p.WorkspaceDir)
	return p.RunNext(ctx)
}

func (p *LinuxNamespacePlugin) appendRuntimeSpec(spec *specs.Spec) error {
	workspaceDir := path.Join(p.WorkspaceDir, "workspace")
	if err := os.WriteFile(path.Join(p.WorkspaceDir, "hosts"), []byte("127.0.0.1 localhost\n"), 0644); err != nil {
		return fmt.Errorf("create runtime hosts file: %w", err)
	}
	for _, relPath := range []string{"sys", "dev", "dev/pts", "dev/shm", "run", "run/user", "run/velda", "etc"} {
		if err := os.MkdirAll(path.Join(workspaceDir, relPath), 0755); err != nil {
			return fmt.Errorf("create runtime mountpoint %s: %w", relPath, err)
		}
	}
	if err := os.Symlink("/proc/self/fd", path.Join(workspaceDir, "dev/fd")); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create runtime /dev/fd symlink: %w", err)
	}
	/*
		fstabMounts, err := ParseRuntimeFstabMounts(path.Join(workspaceDir, "etc/fstab"))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("parse runtime fstab: %w", err)
		}
		for _, mount := range fstabMounts {
			targetPath := path.Join(workspaceDir, strings.TrimPrefix(mount.Destination, "/"))
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("create runtime fstab mountpoint %s: %w", mount.Destination, err)
			}
			spec.Mounts = append(spec.Mounts, mount)
		}
	*/
	spec.Mounts = append(spec.Mounts,
		specs.Mount{Destination: "/sys", Type: "sysfs", Source: "sysfs"},
		specs.Mount{Destination: "/sys/fs/cgroup", Type: "cgroup2", Source: "cgroup2"},
		specs.Mount{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755"}},
		specs.Mount{Destination: "/dev/pts", Type: "devpts", Source: "devpts", Options: []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=620"}},
		specs.Mount{Destination: "/dev/shm", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "nodev", "strictatime", "mode=1777"}},
		specs.Mount{Destination: "/run", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "nodev", "strictatime", "mode=755"}},
		specs.Mount{Destination: "/run/velda", Type: "bind", Source: path.Join(p.WorkspaceDir, "velda"), Options: []string{"rbind", "ro"}},
		specs.Mount{Destination: "/etc/hosts", Type: "bind", Source: path.Join(p.WorkspaceDir, "hosts"), Options: []string{"bind", "ro"}},
	)
	for _, mount := range p.SandboxConfig.GetHostMounts() {
		if mount.GetSource() == "" || mount.GetTarget() == "" {
			log.Printf("Skipping invalid host mount in runtime spec: %v", mount)
			continue
		}
		targetPath := path.Join(workspaceDir, strings.TrimPrefix(mount.GetTarget(), "/"))
		if err := os.MkdirAll(targetPath, 0755); err != nil {
			return fmt.Errorf("create runtime host mount target: %w", err)
		}
		options := []string{"rbind"}
		if !mount.GetReadWrite() {
			options = append(options, "ro")
		}
		spec.Mounts = append(spec.Mounts, specs.Mount{Destination: "/" + strings.TrimPrefix(mount.GetTarget(), "/"), Type: "bind", Source: mount.GetSource(), Options: options})
	}
	if spec.Linux == nil {
		spec.Linux = &specs.Linux{}
	}
	if spec.Linux.Resources == nil {
		spec.Linux.Resources = &specs.LinuxResources{}
	}
	devAutofsStat, err := os.Stat("/dev/autofs")
	if err != nil {
		return fmt.Errorf("stat /dev/autofs: %w", err)
	}
	devAutofsSys, ok := devAutofsStat.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get stat for /dev/autofs")
	}
	devAutofsMajor := int64(unix.Major(devAutofsSys.Rdev))
	devAutofsMinor := int64(unix.Minor(devAutofsSys.Rdev))
	devAutofsMode := os.FileMode(devAutofsStat.Mode() & os.ModePerm)
	spec.Linux.Devices = append(spec.Linux.Devices, specs.LinuxDevice{
		Path:     "/dev/autofs",
		Type:     "c",
		Major:    devAutofsMajor,
		Minor:    devAutofsMinor,
		FileMode: fileModePtr(devAutofsMode),
		UID:      uint32Ptr(devAutofsSys.Uid),
		GID:      uint32Ptr(devAutofsSys.Gid),
	})
	if p.SandboxConfig.GetAllocateTty() {
		ttyMode := os.FileMode(0666)
		ttyMajor := int64(4)
		ttyMinor := int64(1)
		spec.Linux.Devices = append(spec.Linux.Devices, specs.LinuxDevice{Path: "/dev/tty1", Type: "c", Major: ttyMajor, Minor: ttyMinor, FileMode: fileModePtr(ttyMode)})
		spec.Linux.Resources.Devices = append(spec.Linux.Resources.Devices, specs.LinuxDeviceCgroup{Type: "c", Major: int64Ptr(ttyMajor), Minor: int64Ptr(ttyMinor), Access: "rwm", Allow: true})
	}
	spec.Linux.Resources.Devices = append(spec.Linux.Resources.Devices,
		specs.LinuxDeviceCgroup{Type: "c", Major: int64Ptr(devAutofsMajor), Minor: int64Ptr(devAutofsMinor), Access: "rwm", Allow: true},
		specs.LinuxDeviceCgroup{Type: "c", Major: int64Ptr(10), Minor: int64Ptr(235), Access: "rwm", Allow: true},
	)
	spec.Linux.RootfsPropagation = "shared"
	return nil
}

func (p *LinuxNamespacePlugin) setupMounts(workDir string) {
	// Disable propagation
	workspaceDir := path.Join(workDir, "workspace")
	if err := syscall.Mount("", workspaceDir, "", syscall.MS_SHARED, ""); err != nil {
		die("remount slave", err)
	}

	// Create a dummy hosts file under workspace dir
	// To be overwritten by NetworkPlugin
	if err := os.WriteFile(path.Join(workDir, "hosts"), []byte("127.0.0.1 localhost\n"), 0644); err != nil {
		die("Create dummy hosts file", err)
	}

	// Mount misc
	if err := syscall.Mount("none", path.Join(workspaceDir, "sys"), "sysfs", 0, ""); err != nil {
		die("Mount sys", err)
	}
	if err := syscall.Mount("devfs", path.Join(workspaceDir, "dev"), "tmpfs", syscall.MS_NOSUID|syscall.MS_STRICTATIME, "mode=755"); err != nil {
		die("Mount dev", err)
	}
	setupDev(path.Join(workspaceDir, "dev"))
	if p.SandboxConfig.AllocateTty {
		// TODO: Pick one exclusive in the node.
		mkNod(path.Join(workspaceDir, "dev/tty1"), unix.S_IFCHR|0666, int(unix.Mkdev(4, 1)))
	}
	if err := syscall.Mount("none", path.Join(workspaceDir, "dev/pts"), "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=620"); err != nil {
		die("Mount dev/pts", err)
	}
	if err := syscall.Mount("none", path.Join(workspaceDir, "dev/shm"), "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_STRICTATIME, "mode=1777"); err != nil {
		die("Mount dev/shm", err)
	}
	if err := syscall.Mount("none", path.Join(workspaceDir, "run"), "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_STRICTATIME, "mode=755"); err != nil {
		die("Mount run", err)
	}

	if err := syscall.Mkdir(path.Join(workspaceDir, "run/user"), 0755); err != nil && !os.IsExist(err) {
		die("Mkdir run/user", err)
	}

	// Mount agent to /run/velda
	agentDir := path.Join(workDir, "velda")
	if err := os.Mkdir(path.Join(workspaceDir, "run/velda"), 0755); err != nil {
		die("Mkdir agent", err)
	}
	if err := syscall.Mount(agentDir, path.Join(workspaceDir, "run/velda"), "", syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY, ""); err != nil {
		die("Mount agent", err)
	}
	if err := syscall.Mount(agentDir, path.Join(workspaceDir, "run/velda"), "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_REC|syscall.MS_RDONLY, ""); err != nil {
		die("Remount agent to RO", err)
	}

	if err := syscall.Mount(path.Join(workDir, "hosts"), path.Join(workspaceDir, "etc/hosts"), "", syscall.MS_BIND, ""); err != nil {
		if !os.IsNotExist(err) {
			die("Mount hosts", err)
		} else {
			log.Printf("Warning: /etc/hosts not found, skipping mount")
		}
	}

	for _, mount := range p.SandboxConfig.GetHostMounts() {
		if mount.GetSource() == "" || mount.GetTarget() == "" {
			log.Printf("Skipping invalid host mount: %v", mount)
			continue
		}
		if err := os.MkdirAll(path.Join(workspaceDir, mount.GetTarget()), 0755); err != nil {
			die("Mkdir host mount target", err)
		}
		if err := syscall.Mount(mount.GetSource(), path.Join(workspaceDir, mount.GetTarget()), "", syscall.MS_BIND, ""); err != nil {
			die("Mount host mount", err)
		}
		if !mount.GetReadWrite() {
			if err := syscall.Mount(mount.GetSource(), path.Join(workspaceDir, mount.GetTarget()), "", syscall.MS_BIND|syscall.MS_RDONLY|syscall.MS_REMOUNT, ""); err != nil {
				die("Remount host as Read-only", err)
			}
		}
	}
}

type PivotRootPlugin struct {
	PluginBase
	WorkspaceDir  string
	SandboxConfig *agentpb.SandboxConfig
}

func (p *PivotRootPlugin) Run(ctx context.Context) error {
	if sandboxRuntimeEnabled(p.SandboxConfig) {
		log.Printf("Runtime mode enabled, skipping pivot root")
		os.Clearenv()
		if err := setDefaultEnv(); err != nil {
			log.Printf("Failed to load default env: %v", err)
		}
		if err := os.Chdir("/"); err != nil {
			log.Printf("Failed to change to runtime root: %v", err)
		}
		if _, err := os.Stat("/etc/fstab"); err == nil {
			// Set up mounts from /etc/fstab by invoking "mount -a -O nox-lazy"
			cmd := exec.Command("mount", "-a", "-O", "nox-lazy")
			cmd.Stderr = os.Stderr
			// Failures are non-fatal.
			if err := cmd.Run(); err != nil {
				log.Printf("Failed start mount -a -O nolazy: %v", err)
			}
		}
		return p.RunNext(ctx)
	}
	workspaceDir := path.Join(p.WorkspaceDir, "workspace")
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_SLAVE, ""); err != nil {
		die("Mount private", err)
	}
	if err := syscall.Mount("cgroup", path.Join(workspaceDir, "sys/fs/cgroup"), "cgroup2", 0, ""); err != nil {
		die("Mount cgroup", err)
	}
	if err := os.Mkdir(path.Join(workspaceDir, "run/.oldroot"), 0755); err != nil && !os.IsExist(err) {
		die("Mkdir oldroot", err)
	}
	if err := syscall.PivotRoot(workspaceDir, path.Join(workspaceDir, "run/.oldroot")); err != nil {
		printMountInfo()
		die("PivotRoot", err)
	}
	if err := os.Chdir("/"); err != nil {
		die("Chdir", err)
	}
	if err := syscall.Unmount("/run/.oldroot", syscall.MNT_DETACH); err != nil {
		die("Unmount oldroot", err)
	}
	if err := os.Remove("/run/.oldroot"); err != nil {
		die("Remove oldroot", err)
	}
	// Mount /proc. This needs to be done in Pid1 to ensure it has the correct PID namespace.
	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		die("Mount proc", err)
	}
	if _, err := os.Stat("/etc/fstab"); err == nil {
		// Set up mounts from /etc/fstab by invoking "mount -a -O nox-lazy"
		cmd := exec.Command("mount", "-a", "-O", "nox-lazy")
		cmd.Stderr = os.Stderr
		// Failures are non-fatal.
		if err := cmd.Run(); err != nil {
			log.Printf("Failed start mount -a -O nolazy: %v", err)
		}
	}
	os.Clearenv()
	err := setDefaultEnv()
	if err != nil {
		log.Printf("Failed to load default env: %v", err)
	}
	return p.RunNext(ctx)
}

func setDefaultEnv() error {
	envfile, err := os.ReadFile("/etc/environment")
	if err != nil {
		return err
	}

	lines := strings.Split(string(envfile), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.Trim(parts[1], "\"")
		if err := os.Setenv(parts[0], value); err != nil {
			return err
		}
	}
	return nil
}

func NewLinuxNamespacePlugin(workspaceDir string, sandboxConfig *agentpb.SandboxConfig) *LinuxNamespacePlugin {
	return &LinuxNamespacePlugin{
		WorkspaceDir:  workspaceDir,
		SandboxConfig: sandboxConfig,
	}
}

func NewPivotRootPlugin(workspaceDir string, sandboxConfig *agentpb.SandboxConfig) *PivotRootPlugin {
	return &PivotRootPlugin{
		WorkspaceDir:  workspaceDir,
		SandboxConfig: sandboxConfig,
	}
}
