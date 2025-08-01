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
	"velda.io/velda/pkg/proto"
)

var listInstanceCmd = &cobra.Command{
	Use:   "list",
	Short: "List all instances of the current account",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		client := proto.NewInstanceServiceClient(conn)
		pageToken, _ := cmd.Flags().GetString("page-token")
		maxResults, _ := cmd.Flags().GetInt32("max-results")
		fetchAll, _ := cmd.Flags().GetBool("all")

		for {
			instances, err := client.ListInstances(cmd.Context(), &proto.ListInstancesRequest{
				PageToken: pageToken,
				PageSize:  maxResults,
			})
			if err != nil {
				return fmt.Errorf("Error listing instances: %w", err)
			}
			for _, instance := range instances.Instances {
				fmt.Printf("%d\t%s\n", instance.Id, instance.InstanceName)
			}
			if instances.NextPageToken != "" && !fetchAll {
				fmt.Printf("Next page token: %s\n", instances.NextPageToken)
			}
			pageToken = instances.NextPageToken
			if pageToken == "" || !fetchAll {
				break
			}
		}
		return nil
	},
}

func init() {
	instanceCmd.AddCommand(listInstanceCmd)

	listInstanceCmd.Flags().String("page-token", "", "Page token")
	listInstanceCmd.Flags().Int32("max-results", 0, "Max results")
	listInstanceCmd.Flags().BoolP("all", "a", false, "Fetch all results")
}
