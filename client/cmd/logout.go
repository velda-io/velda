// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout and remove the current profile",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if clientlib.IsInSession() {
			return errors.New("Log-out is not allowed from Velda session.")
		}
		err := doLogout(cmd)
		if err != nil {
			cmd.Printf("Error revoking token: %v\n", err)
		}
		defaultProfile, err := clientlib.GlobalConfig().GetConfig("profile")
		currentCfg, err := clientlib.CurrentConfig()
		removingDefault := false
		if err == nil {
			removingDefault = currentCfg.Profile == defaultProfile
		}

		clientlib.DeleteCurrentProfile()
		if removingDefault {
			cmd.Printf("Default profile '%s' has been removed.\n", defaultProfile)
			cmd.Printf("Use `velda config set-current <profile_name>` to set a new default profile.\n")
		}
		return nil
	},
}

func doLogout(cmd *cobra.Command) error {
	authClient, err := clientlib.GetAuthServiceClient()
	if err != nil {
		return fmt.Errorf("Error getting auth client: %w", err)
	}

	authenticator, err := clientlib.GetAuthenticator()
	if err != nil {
		return fmt.Errorf("Error getting authenticator: %w", err)
	}
	refreshToken, err := authenticator.GetRefreshToken(cmd.Context())
	if err != nil {
		return fmt.Errorf("Error getting refresh token: %w", err)
	}
	_, err = authClient.RevokeToken(cmd.Context(), &proto.RevokeTokenRequest{
		Token: refreshToken,
	})
	if err != nil {
		return fmt.Errorf("Error revoking token: %w", err)
	}
	cmd.PrintErrf("Logged out successfully.\n")
	return nil
}

func init() {
	authCmd.AddCommand(logoutCmd)
}
