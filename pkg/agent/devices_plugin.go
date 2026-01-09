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
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type DevicesPlugin struct {
	PluginBase
	WorkspaceDir string
	config       *agentpb.SandboxConfig
}

func NewDevicesPlugin(workspaceDir string, config *agentpb.SandboxConfig) *DevicesPlugin {
	return &DevicesPlugin{
		WorkspaceDir: workspaceDir,
		config:       config,
	}
}

func copyNod(src, dst string) error {
	stat, err := os.Stat(src)
	if err != nil {
		return err
	}

	sysStat, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return err
	}
	if stat.Mode()&os.ModeDevice == 0 {
		return nil
	}
	if err := unix.Mknod(dst, unix.S_IFCHR|uint32(stat.Mode()&fs.ModePerm), int(sysStat.Rdev)); err != nil {
		return err
	}

	if err := os.Chmod(dst, stat.Mode()&fs.ModePerm); err != nil {
		return err
	}
	if err := os.Chown(dst, int(sysStat.Uid), int(sysStat.Gid)); err != nil {
		return err
	}

	return nil
}

func (p *DevicesPlugin) Run(ctx context.Context) error {
	// detect available GPU devices (NVIDIA or AMD)
	nvidiaExists := func() bool { _, err := os.Stat("/dev/nvidiactl"); return err == nil }()
	amdKfdExists := func() bool { _, err := os.Stat("/dev/kfd"); return err == nil }()
	amdDriExists := func() bool {
		fi, err := os.Stat("/dev/dri")
		if err != nil {
			return false
		}
		return fi.IsDir()
	}()

	// nothing to do if no GPU devices present
	if !nvidiaExists && !amdKfdExists && !amdDriExists {
		return p.RunNext(ctx)
	}

	workspaceDir := path.Join(p.WorkspaceDir, "workspace")

	// handle NVIDIA devices/libs if present
	if nvidiaExists {
		nvidia_libs := os.Getenv("VELDA_NVIDIA_DIR")
		if nvidia_libs == "" {
			nvidia_libs = p.config.GetNvidiaDriverInstallDir()
		}
		if nvidia_libs == "" {
			if _, err := os.Stat("/var/nvidia"); err == nil {
				nvidia_libs = "/var/nvidia"
			}
		}

		// copy device nodes
		files, err := filepath.Glob("/dev/nvidia*")
		if err != nil {
			return fmt.Errorf("glob nvidia nodes: %w", err)
		}
		capfiles, err := filepath.Glob("/dev/nvidia-caps/*")
		if err != nil {
			return fmt.Errorf("glob nvidia caps: %w", err)
		}
		if err := os.MkdirAll(filepath.Join(workspaceDir, "dev/nvidia-caps"), 0755); err != nil {
			return fmt.Errorf("mkdir /dev/nvidia-caps: %w", err)
		}
		files = append(files, capfiles...)
		for _, file := range files {
			if err := copyNod(file, filepath.Join(workspaceDir, file)); err != nil {
				return fmt.Errorf("copy nvidia node: %v %w", file, err)
			}
		}

		// bind-mount driver libs
		if nvidia_libs != "" {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "var/nvidia"), 0755); err != nil {
				return fmt.Errorf("mkdir nvidia bins: %w", err)
			}

			if err := syscall.Mount(nvidia_libs, filepath.Join(workspaceDir, "var/nvidia"), "", syscall.MS_BIND, ""); err != nil {
				return fmt.Errorf("mount nvidia libs: %w", err)
			}

			if err := syscall.Mount(nvidia_libs, filepath.Join(workspaceDir, "var/nvidia"), "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
				return fmt.Errorf("remount nvidia libs as RO: %w", err)
			}
		}
	}

	// handle AMD devices: /dev/kfd and /dev/dri
	if amdKfdExists || amdDriExists {
		if err := os.MkdirAll(filepath.Join(workspaceDir, "dev"), 0755); err != nil {
			return fmt.Errorf("mkdir dev: %w", err)
		}
	}
	if amdKfdExists {
		if err := copyNod("/dev/kfd", filepath.Join(workspaceDir, "/dev/kfd")); err != nil {
			return fmt.Errorf("copy kfd node: %w", err)
		}
	}
	if amdDriExists {
		files, err := filepath.Glob("/dev/dri/*")
		if err != nil {
			return fmt.Errorf("glob dri nodes: %w", err)
		}
		if len(files) > 0 {
			if err := os.MkdirAll(filepath.Join(workspaceDir, "dev/dri"), 0755); err != nil {
				return fmt.Errorf("mkdir dev/dri: %w", err)
			}
			for _, file := range files {
				if err := copyNod(file, filepath.Join(workspaceDir, file)); err != nil {
					return fmt.Errorf("copy dri node: %v %w", file, err)
				}
			}
		}
	}

	return p.RunNext(ctx)
}

func (p *DevicesPlugin) HasNvidiaGpu() bool {
	_, ctlErr := os.Stat("/dev/nvidiactl")
	_, devErr := os.Stat("/var/nvidia")
	return ctlErr == nil && devErr == nil
}
