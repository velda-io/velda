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

var volumeCmd = &cobra.Command{
	Use:     "volume",
	Aliases: []string{"vol"},
	Short:   "Manage volumes for the current account",
	Long: `Manage volumes for the current account.

Volumes are persistent storage that can be attached to instances.
You can create, list, delete, and resize volumes.`,
}

func init() {
	rootCmd.AddCommand(volumeCmd)
}
