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
	"strconv"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var deleteVolumeCmd = &cobra.Command{
	Use:   "delete <volume-id>",
	Short: "Delete a volume",
	Long: `Delete a volume by ID.

Examples:
  # Delete a volume (with confirmation)
  velda volume delete 12345

  # Delete a volume without confirmation
  velda volume delete 12345 --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewVolumeServiceClient(conn)

		volumeIDStr := args[0]
		volumeID, err := strconv.ParseInt(volumeIDStr, 10, 64)
		if err != nil {
			return fmt.Errorf("Invalid volume ID: %v", err)
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			cmd.Printf("Are you sure you want to delete volume %d? This action cannot be undone. (y/N): ", volumeID)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				cmd.Println("Operation cancelled")
				return nil
			}
		}

		request := &proto.DeleteVolumeRequest{
			VolumeId: volumeID,
		}

		_, err = client.DeleteVolume(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error deleting volume: %v", err)
		}

		cmd.Printf("Volume %d deleted\n", volumeID)
		return nil
	},
}

func init() {
	volumeCmd.AddCommand(deleteVolumeCmd)
	flags := deleteVolumeCmd.Flags()
	flags.BoolP("force", "f", false, "Skip confirmation prompt")
}
