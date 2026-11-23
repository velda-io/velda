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
	"google.golang.org/protobuf/encoding/protojson"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var createVolumeCmd = &cobra.Command{
	Use:   "create <name> --size-gb <size> [--region <region>]",
	Short: "Create a new volume",
	Long: `Create a new volume with the specified size.

Examples:
  # Create a 100 GB volume
  velda volume create my-volume --size-gb 100

  # Create a volume in a specific region
  velda volume create my-volume --size-gb 50 --region us-east-1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewVolumeServiceClient(conn)

		name := args[0]
		sizeGB, _ := cmd.Flags().GetInt32("size-gb")
		region, _ := cmd.Flags().GetString("region")

		if sizeGB <= 0 {
			return fmt.Errorf("Size must be greater than 0")
		}
		if name == "" {
			return fmt.Errorf("Volume name is required")
		}

		volume := &proto.Volume{
			Name:   name,
			SizeGb: sizeGB,
		}

		request := &proto.CreateVolumeRequest{
			Volume: volume,
			Region: region,
		}

		createdVolume, err := client.CreateVolume(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error creating volume: %v", err)
		}

		marshaler := protojson.MarshalOptions{
			Indent:          "  ",
			EmitUnpopulated: true,
		}
		output, err := marshaler.Marshal(createdVolume)
		if err != nil {
			return fmt.Errorf("Error marshaling response: %v", err)
		}
		fmt.Println(string(output))

		return nil
	},
}

func init() {
	volumeCmd.AddCommand(createVolumeCmd)
	flags := createVolumeCmd.Flags()
	flags.Int32P("size-gb", "s", 0, "Volume size in GB (required)")
	flags.StringP("region", "r", "", "Region for the volume (optional)")
	createVolumeCmd.MarkFlagRequired("size-gb")
}
