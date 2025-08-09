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

var createImageCmd = &cobra.Command{
	Use:   "create <image-name> -i <instance> [-s <snapshot>]",
	Short: "Create a new image",
	Long: `Create a new image from an instance and optionally a specific snapshot.

Examples:
  # Create an image from the current state of an instance
  velda image create my-image -i my-instance

  # Create an image from a specific snapshot of an instance
  velda image create my-image -i my-instance -s my-snapshot`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewInstanceServiceClient(conn)

		imageName := args[0]
		if imageName == "" {
			return fmt.Errorf("Image name is required")
		}

		instanceFlag, _ := cmd.Flags().GetString("instance")

		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(), instanceFlag, clientlib.FallbackToSession)
		if err != nil {
			return fmt.Errorf("Error parsing instance ID: %v", err)
		}

		snapshotName, _ := cmd.Flags().GetString("snapshot")

		request := &proto.CreateImageRequest{
			ImageName:    imageName,
			InstanceId:   instanceId,
			SnapshotName: snapshotName,
		}

		cmd.Printf("Creating image '%s' from instance %d", imageName, instanceId)
		if snapshotName != "" {
			cmd.Printf(" (snapshot: %s)", snapshotName)
		}
		cmd.Println()

		_, err = client.CreateImage(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error creating image: %v", err)
		}

		cmd.Printf("Image '%s' created successfully\n", imageName)
		return nil
	},
}

func init() {
	imageCmd.AddCommand(createImageCmd)
	flags := createImageCmd.Flags()
	flags.StringP("instance", "i", "", "Instance to create the image from")
	flags.StringP("snapshot", "s", "", "Snapshot to create the image from")
}
