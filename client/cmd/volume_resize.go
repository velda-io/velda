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

var resizeVolumeCmd = &cobra.Command{
	Use:   "resize <volume-id> --size-gb <size>",
	Short: "Resize a volume",
	Long: `Resize a volume to a new size. Volumes can only be increased in size, not decreased.

Examples:
  # Resize a volume to 200 GB
  velda volume resize 12345 --size-gb 200`,
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

		newSizeGB, _ := cmd.Flags().GetInt32("size-gb")
		if newSizeGB <= 0 {
			return fmt.Errorf("New size must be greater than 0")
		}

		request := &proto.UpdateVolumeSizeRequest{
			VolumeId: volumeID,
			SizeGb:   newSizeGB,
		}

		_, err = client.UpdateVolumeSize(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error resizing volume: %v", err)
		}

		cmd.Printf("Volume %d resized to %d GB\n", volumeID, newSizeGB)
		return nil
	},
}

func init() {
	volumeCmd.AddCommand(resizeVolumeCmd)
	flags := resizeVolumeCmd.Flags()
	flags.Int32P("size-gb", "s", 0, "New volume size in GB (must be larger than current size, required)")
	resizeVolumeCmd.MarkFlagRequired("size-gb")
}
