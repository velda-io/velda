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
	"google.golang.org/protobuf/encoding/protojson"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var getVolumeCmd = &cobra.Command{
	Use:   "get <volume-id>",
	Short: "Get volume details",
	Long: `Get detailed information about a specific volume.

Examples:
  # Get volume details
  velda volume get 12345`,
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

		request := &proto.GetVolumeRequest{
			VolumeId: volumeID,
		}

		volume, err := client.GetVolume(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error getting volume: %v", err)
		}

		marshaler := protojson.MarshalOptions{
			Indent:          "  ",
			EmitUnpopulated: true,
		}
		output, err := marshaler.Marshal(volume)
		if err != nil {
			return fmt.Errorf("Error marshaling response: %v", err)
		}
		fmt.Println(string(output))

		return nil
	},
}

func init() {
	volumeCmd.AddCommand(getVolumeCmd)
}
