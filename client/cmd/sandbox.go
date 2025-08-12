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
//go:build linux
// +build linux

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"velda.io/velda/pkg/agent"
	"velda.io/velda/pkg/agent_runner"
	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/utils"

	agentpb "velda.io/velda/pkg/proto/agent"
)

// sandboxCmd represents the sandbox command
var sandboxCmd = &cobra.Command{
	Use:    "sandbox",
	Short:  "Start a velda sandbox",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Getenv("VELDA_SANDBOX_PPROF") != "" {
			go func() {
				pid1, _ := cmd.Flags().GetBool("pid1")
				var port string
				if pid1 {
					port = ":6062"
				} else {
					port = ":6061"
				}
				cmd.Printf("Pprof finished with %s\n", http.ListenAndServe(port, nil))
			}()
		}
		netns, _ := cmd.Flags().GetInt("netfd")
		pid1, _ := cmd.Flags().GetBool("pid1")
		sandboxConfig := clientlib.GetAgentSandboxConfig()
		patchConfig(sandboxConfig)

		if netns > 0 && !pid1 {
			if err := unix.Setns(netns, unix.CLONE_NEWNET); err != nil {
				return fmt.Errorf("Setns failed: %w", err)
			}
			unix.Close(netns)
			cmd.Printf("Set network namespace to fd %d", netns)
		}
		err := getRunner(cmd.Context(), cmd, sandboxConfig).Run(cmd.Context())
		var exitCodeErr utils.ErrorWithExitCode
		if err != nil && errors.As(err, &exitCodeErr) {
			os.Exit(exitCodeErr.ExitCode())
		}
		return err
	},
}

func init() {
	AgentCmd.AddCommand(sandboxCmd)
	sandboxCmd.Flags().Bool("pid1", false, "Run the init components.")
	sandboxCmd.Flags().String("workdir", "", "The working directory")
	sandboxCmd.MarkFlagRequired("workdir")
	sandboxCmd.Flags().String("auth-key", "/run/velda/auth-key.pem", "The path to the auth key")
	sandboxCmd.Flags().String("aa-profile", "", "The AppArmor profile to use")
	sandboxCmd.Flags().String("agent-name", "", "The name of the agent")
	sandboxCmd.Flags().String("temp-dir", "", "The temporary directory for empty mounts")
	sandboxCmd.Flags().Int("netfd", -1, "The file descriptor for the network namespace")
}

func patchConfig(config *agentpb.SandboxConfig) {
	if config.GetDiskSource() == nil {
		config.DiskSource = &agentpb.AgentDiskSource{
			Source: &agentpb.AgentDiskSource_NfsMountSource_{
				NfsMountSource: &agentpb.AgentDiskSource_NfsMountSource{},
			},
		}
	}
}

func getRunner(ctx context.Context, cmd *cobra.Command, sandboxConfig *agentpb.SandboxConfig) agent.AbstractPlugin {
	pid1, _ := cmd.Flags().GetBool("pid1")
	if pid1 {
		return pid1Runner(ctx, cmd, sandboxConfig)
	}

	return shimRunner(ctx, cmd, sandboxConfig)
}

func shimRunner(ctx context.Context, cmd *cobra.Command, sandboxConfig *agentpb.SandboxConfig) agent.AbstractPlugin {
	return agent_runner.NewShimRunner(ctx, cmd, sandboxConfig)
}

func pid1Runner(ctx context.Context, cmd *cobra.Command, sandboxConfig *agentpb.SandboxConfig) agent.AbstractPlugin {
	return agent_runner.NewPid1Runner(ctx, cmd, sandboxConfig)
}
