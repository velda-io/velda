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

var configSetCurrentCmd = &cobra.Command{
	Use:   "set-current [default-profile]",
	Short: "Set the default profile",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		profile := args[0]
		_, err := clientlib.LoadConfigs(profile)
		cobra.CheckErr(err)
		err = clientlib.GlobalConfig().SetConfig("profile", profile)
		cobra.CheckErr(err)
		fmt.Println("Default profile set to", profile)
	},
}

func init() {
	configCmd.AddCommand(configSetCurrentCmd)
}
