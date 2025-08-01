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
	"time"

	"github.com/spf13/cobra"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type WorkDir string
type AgentName string
type MaxSessionTime time.Duration
type AuthPluginType Plugin

func ProvideWorkdir(cmd *cobra.Command) WorkDir {
	workDir, _ := cmd.Flags().GetString("workdir")
	return WorkDir(workDir)
}
func ProvideMaxSessionTime(cmd *cobra.Command) MaxSessionTime {
	maxTime, _ := cmd.Flags().GetDuration("max-time")
	return MaxSessionTime(maxTime)
}

func ProvideRequestPlugin() *SessionRequestPlugin {
	return NewSessionRequestPlugin()
}

func ProvideAgentDaemonPlugin(workDir WorkDir, sandboxConfig *agentpb.SandboxConfig) *AgentDaemonPlugin {
	return NewAgentDaemonPlugin(string(workDir), sandboxConfig)
}

func ProvideSandboxFsPlugin(workDir WorkDir, sandboxConfig *agentpb.SandboxConfig, requestPlugin *SessionRequestPlugin) *SandboxFsPlugin {
	return NewSandboxFsPlugin(string(workDir), sandboxConfig, requestPlugin)
}

func ProvideAuthPlugin(cmd *cobra.Command, requestPlugin *SessionRequestPlugin) AuthPluginType {
	return NewAuthPlugin(requestPlugin)
}
func ProvideMounter(sandboxConfig *agentpb.SandboxConfig) Mounter {
	return NewSimpleMounter(sandboxConfig)
}

func ProvideRootfsPlugin(workDir WorkDir, mounter Mounter, sandboxConfig *agentpb.SandboxConfig, requestPlugin *SessionRequestPlugin) *RootfsPlugin {
	return NewRootfsPlugin(string(workDir), mounter, sandboxConfig, requestPlugin)
}

func ProvideAutoFsDaemonPlugin() *AutoFsDaemonPlugin {
	return NewAutoFsDaemonPlugin()
}

func ProvideLinuxNamespacePlugin(workDir WorkDir, sandboxConfig *agentpb.SandboxConfig, requestPlugin *SessionRequestPlugin) *LinuxNamespacePlugin {
	return NewLinuxNamespacePlugin(string(workDir), sandboxConfig, requestPlugin)
}

func ProvideNvidiaPlugin(workDir WorkDir) *NvidiaPlugin {
	return NewNvidiaPlugin(string(workDir))
}

func ProvideRunPid1Plugin(workDir WorkDir, sandboxConfig *agentpb.SandboxConfig, agentDaemonPlugin *AgentDaemonPlugin, requestPlugin *SessionRequestPlugin) *RunPid1Plugin {
	return NewRunPid1Plugin(string(workDir), sandboxConfig, agentDaemonPlugin, requestPlugin)
}

func ProvideAgentName(cmd *cobra.Command) AgentName {
	agentName, _ := cmd.Flags().GetString("agent-name")
	return AgentName(agentName)
}

func ProvidePivotRootPlugin(workDir WorkDir) *PivotRootPlugin {
	return NewPivotRootPlugin(string(workDir))
}

func ProvideWaiterPlugin() *WaiterPlugin {
	return NewWaiterPlugin()
}

func ProvideCompletionSignalPlugin() *CompletionSignalPlugin {
	return NewCompletionSignalPlugin()
}

func ProvideBatchPlugin(waiterPlugin *WaiterPlugin, requestPlugin *SessionRequestPlugin, completionSignalPlugin *CompletionSignalPlugin) *BatchPlugin {
	return NewBatchPlugin(waiterPlugin, requestPlugin, completionSignalPlugin)
}

func ProvideSshdPlugin(agentName AgentName, auth AuthPluginType, waiterPlugin *WaiterPlugin, requestPlugin *SessionRequestPlugin, completionSignalPlugin *CompletionSignalPlugin, nvidiaPlugin *NvidiaPlugin) *SshdPlugin {
	return NewSshdPlugin(string(agentName), auth, waiterPlugin, requestPlugin, completionSignalPlugin, nvidiaPlugin.HasGpu())
}
func ProvideReportStatusPlugin(sshdPlugin *SshdPlugin) *ReportStatusPlugin {
	return NewReportStatusPlugin(sshdPlugin)
}

func ProvideCompletionWaiterPlugin(completionSignalPlugin *CompletionSignalPlugin, maxSessionTime MaxSessionTime) *CompletionWaitPlugin {
	completionWaiter := completionSignalPlugin.GetWaiterPlugin()
	completionWaiter.MaxTime = time.Duration(maxSessionTime)
	return completionWaiter
}
