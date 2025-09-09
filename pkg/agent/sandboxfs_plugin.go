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
	"path"
	"syscall"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
	"velda.io/velda/pkg/utils"
)

type SandboxFsPlugin struct {
	PluginBase
	WorkspaceDir  string
	SandboxConfig *agentpb.SandboxConfig
	requestPlugin interface{}
}

func NewSandboxFsPlugin(workspaceDir string, sandboxConfig *agentpb.SandboxConfig, requestPlugin interface{}) *SandboxFsPlugin {
	return &SandboxFsPlugin{
		WorkspaceDir:  workspaceDir,
		SandboxConfig: sandboxConfig,
		requestPlugin: requestPlugin,
	}
}

func (p *SandboxFsPlugin) Run(ctx context.Context) error {
	session := ctx.Value(p.requestPlugin).(*proto.SessionRequest)

	err := p.initAgentDir(session, p.WorkspaceDir)
	if err != nil {
		return fmt.Errorf("InitAgentDir: %w", err)
	}
	defer func() {
		if err := syscall.Unmount(path.Join(p.WorkspaceDir, "velda/velda"), syscall.MNT_DETACH); err != nil {
			fmt.Printf("Unmount velda: %v\n", err)
		}
		os.RemoveAll(p.WorkspaceDir)
	}()
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
