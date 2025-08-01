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
	"io"
	"log"
	"os"
	"path"
	"strings"
	"syscall"

	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type Mounter interface {
	Mount(ctx context.Context, session *proto.SessionRequest, workspaceDir string) (cleanup func(), err error)
}

type RootfsPlugin struct {
	PluginBase
	mounter       Mounter
	WorkspaceDir  string
	SandboxConfig *agentpb.SandboxConfig
	requestPlugin interface{}
}

func NewRootfsPlugin(workspaceDir string, mounter Mounter, sandboxConfig *agentpb.SandboxConfig, requestPlugin interface{}) *RootfsPlugin {
	return &RootfsPlugin{
		mounter:       mounter,
		WorkspaceDir:  workspaceDir,
		SandboxConfig: sandboxConfig,
		requestPlugin: requestPlugin,
	}
}

func (p *RootfsPlugin) Run(ctx context.Context) error {
	session := ctx.Value(p.requestPlugin).(*proto.SessionRequest)

	workspaceDir := path.Join(p.WorkspaceDir, "workspace")
	if err := os.Mkdir(workspaceDir, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("Mkdir workspace: %w", err)
	}
	cleanup, err := p.mounter.Mount(context.Background(), session, workspaceDir)
	if err != nil {
		return fmt.Errorf("Mount workspace: %w", err)
	}
	defer func() {
		umountAll(p.WorkspaceDir)
		if cleanup != nil {
			cleanup()
		}
	}()
	return p.RunNext(ctx)
}

func umountAll(dir string) {
	err := doUmountAll(dir)
	if err != nil {
		log.Printf("Failed to unmount %s: %v", dir, err)
	}
}

func doUmountAll(dir string) error {
	// Read /proc/mounts to get all mounted filesystems under dir
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return err
	}
	defer f.Close()
	var mounts []string
	for {
		var (
			fs   string
			path string
		)
		_, err := fmt.Fscanf(f, "%s %s", &fs, &path)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, dir) {
			mounts = append(mounts, path)
		}
	}
	// Unmount all mounts
	for i := len(mounts) - 1; i >= 0; i-- {
		if err := syscall.Unmount(mounts[i], 0); err != nil {
			return err
		}
	}
	return nil
}
