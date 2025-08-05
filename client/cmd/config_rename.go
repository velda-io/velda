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
	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
)

var configRenameCmd = &cobra.Command{
	Use:   "rename [new name]",
	Short: "Rename a config profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return clientlib.MustCurrentConfig().RenameConfig(args[0])
	},
}

func init() {
	configCmd.AddCommand(configRenameCmd)
}
