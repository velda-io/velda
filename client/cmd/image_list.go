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

var listImagesCmd = &cobra.Command{
	Use:   "list [prefix]",
	Short: "List available images",
	Long: `List all available images, optionally filtered by prefix.

Examples:
  # List all images
  velda image list

  # List images with the "ubuntu" prefix
  velda image list ubuntu`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewInstanceServiceClient(conn)

		prefix := ""
		if len(args) == 1 {
			prefix = args[0]
		}

		request := &proto.ListImagesRequest{
			Prefix: prefix,
		}

		response, err := client.ListImages(cmd.Context(), request)
		cmd.SetOut(cmd.OutOrStdout())
		if err != nil {
			return fmt.Errorf("Error listing images: %v", err)
		}

		if len(response.Images) == 0 {
			cmd.Println("No images found")
			return nil
		}

		cmd.Println("Available images:")
		for _, image := range response.Images {
			cmd.Printf("  %s\n", image)
		}

		return nil
	},
}

func init() {
	imageCmd.AddCommand(listImagesCmd)
}
