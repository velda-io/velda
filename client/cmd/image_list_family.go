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
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var allowedImageOutputFormats = []string{"table", "json"}

var listFamilyCmd = &cobra.Command{
	Use:   "list-family",
	Short: "List available image catalog options",
	Long: `List all image catalogs available to the current user.

This shows the image catalog entries that you own or have access to.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")
		if !slices.Contains(allowedImageOutputFormats, output) {
			return fmt.Errorf("Invalid output format: %s", output)
		}

		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewImageManagementEntClient(conn)

		query, _ := cmd.Flags().GetString("query")
		limit, _ := cmd.Flags().GetInt32("limit")
		offset, _ := cmd.Flags().GetInt32("offset")

		request := &proto.SearchImagesRequest{
			Query:  query,
			Limit:  limit,
			Offset: offset,
		}

		response, err := client.SearchImages(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error searching image catalogs: %v", err)
		}

		if output == "json" {
			data, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(response)
			if err != nil {
				return err
			}
			cmd.OutOrStdout().Write(data)
			return nil
		} else if output == "table" {
			if len(response.Images) == 0 {
				cmd.Println("No image catalogs found")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSlug\tName\tRegion")
			for _, image := range response.Images {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", image.Id, image.Slug, image.Name, image.Region)
			}
			w.Flush()
		}
		return nil
	},
}

func init() {
	imageCmd.AddCommand(listFamilyCmd)
	listFamilyCmd.Flags().StringP("output", "o", "table", "Output format. Options: table, json")
	listFamilyCmd.Flags().StringP("query", "q", "", "Search query to filter image catalogs")
	listFamilyCmd.Flags().Int32P("limit", "l", 50, "Maximum number of results to return")
	listFamilyCmd.Flags().Int32P("offset", "", 0, "Offset for pagination")
}
