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

// printAccessTokenCmd represents the printAccessToken command
var printAccessTokenCmd = &cobra.Command{
	Use:   "print-access-token",
	Short: "Prints the access token",
	RunE: func(cmd *cobra.Command, args []string) error {
		authenticator, err := clientlib.GetAuthenticator()
		if err != nil {
			return fmt.Errorf("Error getting authenticator: %w", err)
		}
		token, err := authenticator.GetAccessToken(cmd.Context())
		if err != nil {
			return fmt.Errorf("Error getting access token: %w", err)
		}
		fmt.Println(token)
		return err
	},
}

func init() {
	authCmd.AddCommand(printAccessTokenCmd)
}
