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
package cmd

import (
	"github.com/spf13/cobra"
)

// AgentCmd represents the agent command
var MiniCmd = &cobra.Command{
	Use:   "mini",
	Short: "Manage mini-velda cluster",
	Long: `This command allows you to manage a mini-velda cluster, including starting, stopping, and configuring the mini-velda environment.

Mini-velda is a Velda cluster that runs directly on individual's compute, with capability to scale to the cloud environment.
`,
}

func init() {
	rootCmd.AddCommand(MiniCmd)
	MiniCmd.PersistentFlags().String("agent-launcher", "docker", "The agent launcher to use (docker)")
}
