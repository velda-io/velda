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
package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

func callPersistentPreRun(cmd *cobra.Command, args []string) {
	if parent := cmd.Parent(); parent != nil {
		if parent.PersistentPreRun != nil {
			parent.PersistentPreRun(parent, args)
		}
	}
}

// AgentCmd represents the agent command
var AgentCmd = &cobra.Command{
	Use:    "agent",
	Short:  "Agent internal commands",
	Hidden: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		log.SetPrefix(fmt.Sprintf("%d\t", os.Getpid()))
		callPersistentPreRun(cmd, args)
	},
}

func init() {
	rootCmd.AddCommand(AgentCmd)
}
