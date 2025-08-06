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

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new profile",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		broker, _ := cmd.Flags().GetString("broker")
		profileProvided := true
		profile, _ := cmd.Flags().GetString("profile")
		if profile == "" {
			profile = "default"
			profileProvided = false
		}
		_, err := clientlib.LoadConfigs(profile)
		if err == nil {
			return fmt.Errorf("Profile '%s' already exists. Use a different profile name or remove the existing profile.", profile)
		}

		created, cfg, err := clientlib.InitCurrentConfig(broker, true)
		if err != nil {
			return fmt.Errorf("Error initializing config: %w", err)
		}
		if !created {
			return fmt.Errorf("Profile '%s' already exists. Use a different profile name or remove the existing profile.", profile)
		}
		if !profileProvided {
			// Config lib do not use 'default' as the profile name, instead it will create a temporary profile.
			if err := cfg.RenameConfig(profile); err != nil {
				return fmt.Errorf("Error renaming config: %w", err)
			}
		}
		if err := cfg.MakeCurrent(); err != nil {
			return fmt.Errorf("Error making config current: %w", err)
		}
		fmt.Printf("Profile '%s' initialized.\n", profile)
		return nil
	},
}

func init() {
	RootCmd.AddCommand(initCmd)
	initCmd.Flags().String("broker", "", "The address of broker")
	initCmd.MarkFlagRequired("broker")
}
