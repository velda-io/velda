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
	"fmt"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
)

var configSetCmd = &cobra.Command{
	Use:   "set --option=value",
	Short: "Save a config item",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		flags := cmd.Flags()
		cfg, err := clientlib.CurrentConfig()
		cobra.CheckErr(err)
		updated := false
		if flags.Changed("default-instance") {
			value, _ := flags.GetString("default-instance")
			instanceId, err := clientlib.GetInstanceIdFromServer(value)
			if err != nil {
				cmd.Println("Failed to get instance ID: ", err)
				return
			}
			value = fmt.Sprintf("%d:%s", instanceId, value)
			cobra.CheckErr(cfg.SetConfig("default-instance", value))
			updated = true
		}
		if flags.Changed("broker") {
			cobra.CheckErr(cfg.SetConfig("broker", flags.Lookup("broker").Value.String()))
			updated = true
		}
		if flags.Changed("identity-file") {
			cobra.CheckErr(cfg.SetConfig("identity-file", flags.Lookup("identity-file").Value.String()))
			updated = true
		}
		if flags.Changed("jump-proxy") {
			cobra.CheckErr(cfg.SetConfig("jump-proxy", flags.Lookup("jump-proxy").Value.String()))
			updated = true
		}
		if flags.Changed("jump-identity-file") {
			cobra.CheckErr(cfg.SetConfig("jump-identity-file", flags.Lookup("jump-identity-file").Value.String()))
			updated = true
		}
		if !updated {
			cobra.CheckErr("No config item specified")
		}
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configSetCmd.Flags().String("default-instance", "", "Configure default instance")
	configSetCmd.Flags().String("broker", "", "The address of broker")
	configSetCmd.Flags().String("identity-file", "", "The identity file to use for authentication")
	configSetCmd.Flags().String("jump-proxy", "", "SSH jump server in user@host format")
	configSetCmd.Flags().String("jump-identity-file", "", "The identity file to use for jump server authentication")
}
