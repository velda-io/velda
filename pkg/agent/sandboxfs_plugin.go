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
	"path"
	"syscall"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
	"velda.io/velda/pkg/utils"
)

type SandboxFsPlugin struct {
	PluginBase
	mounter       Mounter
	WorkspaceDir  string
	SandboxConfig *agentpb.SandboxConfig
	requestPlugin interface{}
}

func NewSandboxFsPlugin(workspaceDir string, mounter Mounter, sandboxConfig *agentpb.SandboxConfig, requestPlugin interface{}) *SandboxFsPlugin {
	return &SandboxFsPlugin{
		mounter:       mounter,
		WorkspaceDir:  workspaceDir,
		SandboxConfig: sandboxConfig,
		requestPlugin: requestPlugin,
	}
}

func (p *SandboxFsPlugin) Run(ctx context.Context) (err error) {
	session := ctx.Value(p.requestPlugin).(*proto.SessionRequest)

	err = p.initAgentDir(session, p.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("InitAgentDir: %w", err)
	}

	// Mount workspace
	workspaceDir := path.Join(p.WorkspaceDir, "workspace")
	if err := os.Mkdir(workspaceDir, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("Mkdir workspace: %w", err)
	}
	cleanup, err := p.mounter.Mount(context.Background(), session, workspaceDir)

	// This should run regardless if previous return function returns error
	defer func() {
		// Unmount all workspace mounts first
		err := umountAll(p.WorkspaceDir)
		if err != nil {
			err = fmt.Errorf("umount workspace: %w", err)
			if cleanup != nil {
				cleanup()
			}
			// Do not remove workspace dir if unmounting fails, to avoid potential data loss. Log the error and return.
			log.Printf("Error unmounting workspace: %v. Workspace dir %s will not be removed.", err, workspaceDir)
			return
		}
		if cleanup != nil {
			cleanup()
		}

		// Finally remove the workspace directory tree
		if removeErr := os.RemoveAll(p.WorkspaceDir); removeErr != nil {
			err = fmt.Errorf("removeall workspace dir %s: %w", p.WorkspaceDir, removeErr)
			return
		}
	}()
	if err != nil {
		return fmt.Errorf("Mount workspace: %w", err)
	}
	return p.RunNext(ctx)
}

func (r *SandboxFsPlugin) initAgentDir(session *proto.SessionRequest, sessionDir string) error {
	instanceId := session.InstanceId
	sessionId := session.SessionId
	configData := clientlib.GenerateAgentConfig(instanceId, sessionId, session.TaskId)
	err := os.Mkdir(path.Join(sessionDir, "velda"), 0755)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("Mkdir velda: %w", err)
	}
	configFile, err := os.Create(path.Join(sessionDir, "velda/velda.yaml"))
	if err != nil {
		return fmt.Errorf("Create config file: %w", err)
	}
	defer configFile.Close()
	if err := utils.PrintProtoYaml(configData, configFile); err != nil {
		return fmt.Errorf("WriteFile agent.yaml: %w", err)
	}
	if err := os.WriteFile(path.Join(sessionDir, "velda/velda"), nil, 0555); err != nil {
		return fmt.Errorf("WriteFile auth token: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Get executable: %w", err)
	}
	// Copy the agent binary
	err = syscall.Mount(executable, path.Join(sessionDir, "velda/velda"), "bind", syscall.MS_BIND, "")
	if err != nil {
		return fmt.Errorf("Mount agent binary: %w", err)
	}
	err = syscall.Mount(executable, path.Join(sessionDir, "velda/velda"), "bind", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, "")
	if err != nil {
		return fmt.Errorf("Mount agent binary: %w", err)
	}
	return nil
}

// NonPrivSandboxFsPlugin is used in non-privileged containers where the container
// runtime has already set up the filesystem.  It writes the velda config directly
// to /run/velda (accessible in the container) and skips all mount operations.
type NonPrivSandboxFsPlugin struct {
	PluginBase
	SandboxConfig *agentpb.SandboxConfig
	requestPlugin interface{}
}

func NewNonPrivSandboxFsPlugin(sandboxConfig *agentpb.SandboxConfig, requestPlugin interface{}) *NonPrivSandboxFsPlugin {
	return &NonPrivSandboxFsPlugin{
		SandboxConfig: sandboxConfig,
		requestPlugin: requestPlugin,
	}
}

func (p *NonPrivSandboxFsPlugin) Run(ctx context.Context) (err error) {
	session := ctx.Value(p.requestPlugin).(*proto.SessionRequest)

	// Write velda config directly to /run/velda so the pid1 process can read it
	// without a bind mount.
	if mkErr := os.MkdirAll("/run/velda", 0755); mkErr != nil {
		return fmt.Errorf("MkdirAll /run/velda: %w", mkErr)
	}
	configData := clientlib.GenerateAgentConfig(session.InstanceId, session.SessionId, session.TaskId)
	configFile, err := os.Create("/run/velda/velda.yaml")
	if err != nil {
		return fmt.Errorf("Create /run/velda/velda.yaml: %w", err)
	}
	defer configFile.Close()
	if err := utils.PrintProtoYaml(configData, configFile); err != nil {
		return fmt.Errorf("Write /run/velda/velda.yaml: %w", err)
	}

	return p.RunNext(ctx)
}
