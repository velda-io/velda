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
	"log"
	"os"
	"path"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
)

var Debug *bool

var rootCmd = &cobra.Command{
	Use:          "velda",
	Short:        "Velda CLI",
	SilenceUsage: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		clientlib.InitConfig()
	},
}

// Exported for addition from other packages
var RootCmd = rootCmd

func DebugLog(format string, args ...interface{}) {
	if *Debug {
		log.Printf(format, args...)
	}
}

func Execute() {
	execName := path.Base(os.Args[0])
	switch execName {
	case "vrun":
		rootCmd.SetArgs(append([]string{"run"}, os.Args[1:]...))
	case "vbatch":
		rootCmd.SetArgs(append([]string{"run", "--batch"}, os.Args[1:]...))
	case "mount.host":
		rootCmd.SetArgs(append([]string{"agent", "mount-hostdir"}, os.Args[1:]...))
	}
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	clientlib.InitConfigFlags(rootCmd)
	Debug = rootCmd.PersistentFlags().Bool("debug", false, "Enable debug mode")
}
