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

var snapshotCreateCmd = &cobra.Command{
	Use:   "create [-i instance] <name>",
	Short: "Create a new snapshot of an instance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewInstanceServiceClient(conn)
		snapshotName := args[0]
		instanceName, _ := cmd.Flags().GetString("instance")
		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(), instanceName, clientlib.FallbackToSession)
		if err != nil {
			return err
		}
		_, err = client.CreateSnapshot(cmd.Context(), &proto.CreateSnapshotRequest{
			InstanceId:   instanceId,
			SnapshotName: snapshotName,
		})
		if err != nil {
			return fmt.Errorf("Error creating snapshot: %v", err)
		}
		return nil
	},
}

func init() {
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCreateCmd.Flags().StringP("instance", "i", "", "Instance to snapshot")
}
