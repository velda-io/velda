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
	"runtime/debug"

	"github.com/spf13/cobra"
	"velda.io/velda"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		version := velda.Version
		if version == "" {
			version = "dev"
		}
		verbose, _ := cmd.Flags().GetBool("verbose")
		cmd.SetOut(cmd.OutOrStdout())
		if verbose {
			cmd.Println("version:", version)
			buildInfo, ok := debug.ReadBuildInfo()
			if !ok {
				cmd.PrintErrln("No build info available")
				return nil
			}
			for _, setting := range buildInfo.Settings {
				cmd.Printf("%s: %s\n", setting.Key, setting.Value)
			}
		} else {
			cmd.Println(version)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolP("verbose", "v", false, "Show detailed version information")
}
