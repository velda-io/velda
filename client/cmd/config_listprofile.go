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

var configListProfileCmd = &cobra.Command{
	Use:   "list-profiles",
	Short: "List all saved profiles",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		profiles, err := clientlib.ListProfiles()
		if err != nil {
			cmd.Println(err)
			return
		}
		currentProfile, err := clientlib.CurrentConfig()
		currentProfileName := ""
		if err == nil {
			currentProfileName = currentProfile.Profile
		}
		for _, profile := range profiles {
			if profile == currentProfileName {
				fmt.Printf("* %s\n", profile)
				continue
			}
			fmt.Printf("  %s\n", profile)
		}
	},
}

func init() {
	configCmd.AddCommand(configListProfileCmd)
}
