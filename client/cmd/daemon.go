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
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/agentd"
	"velda.io/velda/pkg/clientlib"
	agentpb "velda.io/velda/pkg/proto/agent"
)

var agentPool string

// daemonCmd represents the daemon command
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "The daemon process for the agent.",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.PrintErrln("Starting Velda agent daemon")
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		brokerClient, err := clientlib.GetBrokerClient()
		if err != nil {
			return err
		}
		daemonConfig := clientlib.GetAgentDaemonConfig()
		if daemonConfig == nil {
			daemonConfig = &agentpb.DaemonConfig{}
		}
		if daemonConfig.MaxSessions <= 0 {
			daemonConfig.MaxSessions = 1
		}
		workDir := daemonConfig.GetWorkDirPath()
		if workDir == "" {
			workDir = "/tmp/agent"
		}
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return err
		}
		networkDaemon, err := agentd.GetNetworkDaemon(int(daemonConfig.MaxSessions), daemonConfig.Network)
		if err != nil {
			return err
		}
		runner := agentd.NewRunner(workDir, networkDaemon)
		if agentPool == "" {
			agentPool = clientlib.GetAgentConfig().GetPool()
		}
		if agentPool == "" {
			return errors.New("agent pool is not specified")
		}
		ag := agentd.NewAgent(brokerClient, runner, agentPool, daemonConfig)

		// Wait until SIGTERM
		go func() {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
			<-sig
			cancel()
		}()
		if os.Getenv("VELDA_SANDBOX_PPROF") != "" {
			go func() {
				cmd.Printf("PProf finished with %s", http.ListenAndServe(":6060", nil))
			}()
		}
		err = ag.Run(ctx)
		if err != nil && err != context.Canceled {
			return err
		}
		return nil
	},
}

func init() {
	AgentCmd.AddCommand(daemonCmd)
	daemonCmd.Flags().StringVarP(&agentPool, "pool", "p", "", "Agent pool")
}
