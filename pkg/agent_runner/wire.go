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
//go:build wireinject

package agent_runner

import (
	"context"

	"github.com/google/wire"
	"github.com/spf13/cobra"
	"velda.io/velda/pkg/agent"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type ShimRunner agent.AbstractPlugin
type Pid1Runner agent.AbstractPlugin

func provideShimRunner(requestPlugin *agent.SessionRequestPlugin, agentDaemonPlugin *agent.AgentDaemonPlugin, sandboxFsPlugin *agent.SandboxFsPlugin, rootfsPlugin *agent.RootfsPlugin, autofsDaemon *agent.AutoFsDaemonPlugin, sandboxPlugin *agent.LinuxNamespacePlugin, nvidiaPlugin *agent.NvidiaPlugin, pid1Plugin *agent.RunPid1Plugin) ShimRunner {
	return agent.NewPluginRunner(
		requestPlugin,
		agentDaemonPlugin,
		sandboxFsPlugin,
		rootfsPlugin,
		autofsDaemon,
		sandboxPlugin,
		nvidiaPlugin,
		autofsDaemon.GetMountPlugin(),
		pid1Plugin,
	)
}

func NewShimRunner(ctx context.Context, cmd *cobra.Command, sandboxConfig *agentpb.SandboxConfig) ShimRunner {
	wire.Build(
		agent.ProvideWorkdir,
		agent.ProvideRequestPlugin,
		agent.ProvideAgentDaemonPlugin,
		agent.ProvideSandboxFsPlugin,
		agent.ProvideMounter,
		agent.ProvideRootfsPlugin,
		agent.ProvideAutoFsDaemonPlugin,
		agent.ProvideLinuxNamespacePlugin,
		agent.ProvideNvidiaPlugin,
		agent.ProvideRunPid1Plugin,
		provideShimRunner,
	)
	return ShimRunner(nil) // This will never be reached, but is required for the wire build.
}

func providePid1Runner(requestPlugin *agent.SessionRequestPlugin, authPlugin agent.AuthPluginType, pivotRootPlugin *agent.PivotRootPlugin, waiterPlugin *agent.WaiterPlugin, completionSignalPlugin *agent.CompletionSignalPlugin, sshdPlugin *agent.SshdPlugin, statusPlugin *agent.ReportStatusPlugin, batchPlugin *agent.BatchPlugin, completionWaiter *agent.CompletionWaitPlugin) Pid1Runner {
	return agent.NewPluginRunner(
		requestPlugin,
		pivotRootPlugin,
		authPlugin,
		waiterPlugin,
		completionSignalPlugin,
		sshdPlugin,
		batchPlugin,
		statusPlugin,
		completionWaiter,
	)
}

// NewPid1Runner creates a new Pid1Runner using dependency injection.
func NewPid1Runner(ctx context.Context, cmd *cobra.Command, sandboxConfig *agentpb.SandboxConfig) Pid1Runner {
	wire.Build(
		agent.ProvideNvidiaPlugin,
		agent.ProvideCommandModifier,
		agent.ProvideMaxSessionTime,
		agent.ProvideWorkdir,
		agent.ProvideAgentName,
		agent.ProvideRequestPlugin,
		agent.ProvideAuthPlugin,
		agent.ProvidePivotRootPlugin,
		agent.ProvideWaiterPlugin,
		agent.ProvideCompletionSignalPlugin,
		agent.ProvideSshdPlugin,
		agent.ProvideReportStatusPlugin,
		agent.ProvideBatchPlugin,
		agent.ProvideCompletionWaiterPlugin,
		providePid1Runner,
	)
	return Pid1Runner(nil) // This will never be reached, but is required for the wire build.
}
