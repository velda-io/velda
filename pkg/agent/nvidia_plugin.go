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

type NvidiaPlugin struct {
	PluginBase
	WorkspaceDir string
	config       *agentpb.SandboxConfig
}

func NewNvidiaPlugin(workspaceDir string, config *agentpb.SandboxConfig) *NvidiaPlugin {
	return &NvidiaPlugin{
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

	return nil
}

func (p *NvidiaPlugin) Run(ctx context.Context) error {
	_, err := os.Stat("/dev/nvidiactl")
	if err != nil {
		// No nvidia device found or driver installation failure.
		return p.RunNext(ctx)
	}
	nvidia_libs := os.Getenv("VELDA_NVIDIA_DIR")
	if nvidia_libs == "" {
		nvidia_libs = p.config.GetNvidiaDriverInstallDir()
	}
	if nvidia_libs == "" {
		// Default installation of images.
		if _, err := os.Stat("/var/nvidia"); err == nil {
			nvidia_libs = "/var/nvidia"
		}
	}
	if nvidia_libs == "" {
		return p.RunNext(ctx)
	}

	workspaceDir := path.Join(p.WorkspaceDir, "workspace")
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

	if err := os.MkdirAll(filepath.Join(workspaceDir, "var/nvidia"), 0755); err != nil {
		return fmt.Errorf("mkdir nvidia bins: %w", err)
	}

	if err := syscall.Mount(nvidia_libs, filepath.Join(workspaceDir, "var/nvidia"), "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("mount nvidia libs: %w", err)
	}

	if err := syscall.Mount(nvidia_libs, filepath.Join(workspaceDir, "var/nvidia"), "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("remount nvidia libs as RO: %w", err)
	}

	return p.RunNext(ctx)
}

func (p *NvidiaPlugin) HasGpu() bool {
	_, ctlErr := os.Stat("/dev/nvidiactl")
	_, devErr := os.Stat("/var/nvidia")
	return ctlErr == nil && devErr == nil
}
