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

var deleteImageCmd = &cobra.Command{
	Use:   "delete <image-name>",
	Short: "Delete an image",
	Long: `Delete an image by name.

Examples:
  # Delete an image
  velda image delete my-image`,
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

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			cmd.Printf("Are you sure you want to delete image '%s'? This action cannot be undone. (y/N): ", imageName)
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" {
				cmd.Println("Operation cancelled")
				return nil
			}
		}

		request := &proto.DeleteImageRequest{
			ImageName: imageName,
		}

		_, err = client.DeleteImage(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error deleting image: %v", err)
		}

		cmd.Printf("Image '%s' deleted\n", imageName)
		return nil
	},
}

func init() {
	imageCmd.AddCommand(deleteImageCmd)
	flags := deleteImageCmd.Flags()
	flags.BoolP("force", "f", false, "Skip confirmation prompt")
}
