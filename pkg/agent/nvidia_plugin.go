// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
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
)

type NvidiaPlugin struct {
	PluginBase
	WorkspaceDir string
}

func NewNvidiaPlugin(workspaceDir string) *NvidiaPlugin {
	return &NvidiaPlugin{
		WorkspaceDir: workspaceDir,
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
	nvidia_libs := os.Getenv("VELDA_NVIDIA_DIR")
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
	return os.Getenv("VELDA_NVIDIA_DIR") != ""
}
