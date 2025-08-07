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
	"log"
	"os"
	"os/exec"
	"path"
	"syscall"

	"golang.org/x/sys/unix"

	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type LinuxNamespacePlugin struct {
	PluginBase
	requestPlugin interface{}
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
	p.setupMounts(ctx, p.WorkspaceDir)
	return p.RunNext(ctx)
}

func (p *LinuxNamespacePlugin) setupMounts(ctx context.Context, workDir string) {
	request := ctx.Value(p.requestPlugin).(*proto.SessionRequest)
	// Copy /etc/hosts to workDir/hosts
	bytes, err := os.ReadFile("/etc/hosts")
	if err != nil {
		die("Read /etc/hosts", err)
	}
	bytes = append(bytes, []byte("\n127.0.0.1 "+request.SessionId+"\n")...)
	if err := os.WriteFile(path.Join(workDir, "hosts"), bytes, 0644); err != nil {
		die("Write /etc/hosts", err)
	}

	// Disable propagation
	workspaceDir := path.Join(workDir, "workspace")

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
		die("Mount hosts", err)
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
	WorkspaceDir string
}

func (p *PivotRootPlugin) Run(ctx context.Context) error {
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
		// Set up mounts from /etc/fstab by invoking "mount -a -O nolazy"
		cmd := exec.Command("mount", "-a", "-O", "nox-lazy")
		cmd.Stderr = os.Stderr
		// Failures are non-fatal.
		if err := cmd.Run(); err != nil {
			log.Printf("Failed start mount -a -O nolazy: %v", err)
		}
	}
	return p.RunNext(ctx)
}

func NewLinuxNamespacePlugin(workspaceDir string, sandboxConfig *agentpb.SandboxConfig, requestPlugin interface{}) *LinuxNamespacePlugin {
	return &LinuxNamespacePlugin{
		WorkspaceDir:  workspaceDir,
		SandboxConfig: sandboxConfig,
		requestPlugin: requestPlugin,
	}
}

func NewPivotRootPlugin(workspaceDir string) *PivotRootPlugin {
	return &PivotRootPlugin{
		WorkspaceDir: workspaceDir,
	}
}
