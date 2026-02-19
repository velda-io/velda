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
	"velda.io/velda/pkg/proto"
)

// authCheckCmd represents the authCheck command
var authCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Checks the authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		client := proto.NewInstanceServiceClient(conn)
		_, err = client.ListInstances(cmd.Context(), &proto.ListInstancesRequest{
			PageSize: 1,
		})
		return err
	},
}

func init() {
	authCmd.AddCommand(authCheckCmd)
}
