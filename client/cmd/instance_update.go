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
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var updateInstanceCmd = &cobra.Command{
	Use:   "update <instance> [--storage-quota-gb <size>] [--volume-bindings <json>]",
	Short: "Update instance configuration",
	Long: `Update instance storage quota and/or volume bindings.

Examples:
  velda instance update my-instance --storage-quota-gb 100
  velda instance update my-instance --volume-bindings '{"/mnt/data": {"volume_id": 67890}}'
  velda instance update my-instance --volume-bindings '{}'
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewInstanceServiceClient(conn)

		instanceName := args[0]
		instanceID, err := clientlib.ParseInstanceId(
			cmd.Context(), instanceName, !clientlib.FallbackToSession)
		if err != nil {
			return err
		}

		// Fetch current instance state
		currentInstance, err := client.GetInstance(cmd.Context(), &proto.GetInstanceRequest{
			InstanceId: instanceID,
		})
		if err != nil {
			return fmt.Errorf("Error fetching instance: %v", err)
		}

		request := &proto.UpdateInstanceRequest{
			InstanceId: instanceID,
		}

		// Start with current volume bindings
		request.VolumeBindings = currentInstance.VolumeBindings

		// Update volume bindings only if explicitly provided
		if cmd.Flags().Changed("volume-bindings") {
			volumeBindingsJSON, _ := cmd.Flags().GetString("volume-bindings")
			var volumeBindings map[string]*proto.VolumeBinding
			if err := json.Unmarshal([]byte(volumeBindingsJSON), &volumeBindings); err != nil {
				return fmt.Errorf("Invalid volume bindings JSON: %v", err)
			}
			request.VolumeBindings = volumeBindings
		}

		// Update storage quota only if explicitly provided
		if cmd.Flags().Changed("storage-quota-gb") {
			storageQuotaGB, _ := cmd.Flags().GetInt32("storage-quota-gb")
			request.StorageQuotaGb = &storageQuotaGB
		}

		instance, err := client.UpdateInstance(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error updating instance: %v", err)
		}

		// Output the updated instance as JSON
		marshaler := protojson.MarshalOptions{
			Indent:          "  ",
			EmitUnpopulated: true,
		}
		output, err := marshaler.Marshal(instance)
		if err != nil {
			return fmt.Errorf("Error marshaling response: %v", err)
		}
		fmt.Println(string(output))

		return nil
	},
}

func init() {
	instanceCmd.AddCommand(updateInstanceCmd)

	updateInstanceCmd.Flags().Int32("storage-quota-gb", 0, "New storage quota in GB")
	updateInstanceCmd.Flags().String("volume-bindings", "", "Volume bindings as JSON map from mount path to volume binding (use '{}' to clear all)")
}
