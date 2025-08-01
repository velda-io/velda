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

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
)

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a config item",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		global, _ := cmd.Flags().GetBool("global")
		var cfg *clientlib.Configs
		if global {
			cfg = clientlib.GlobalConfig()
		} else {
			var err error
			cfg, err = clientlib.CurrentConfig()
			cobra.CheckErr(err)
		}
		value, err := cfg.GetConfig(args[0])
		if err != nil {
			cmd.Println(err)
			return
		}
		fmt.Fprintln(cmd.OutOrStdout(), value)
	},
}

func init() {
	configCmd.AddCommand(configGetCmd)
	configGetCmd.Flags().Bool("global", false, "Get global config")
}
