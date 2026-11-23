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

var listVolumesCmd = &cobra.Command{
	Use:   "list [--page-size <size>] [--page-token <token>]",
	Short: "List all volumes",
	Long: `List all volumes for the current account.

Examples:
  # List all volumes
  velda volume list

  # List volumes with custom page size
  velda volume list --page-size 50

  # Output as JSON
  velda volume list --output json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")
		if !slices.Contains(allowedOutputFormats, output) {
			return fmt.Errorf("Invalid output format: %s", output)
		}

		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewVolumeServiceClient(conn)

		pageSize, _ := cmd.Flags().GetInt32("page-size")
		pageToken, _ := cmd.Flags().GetString("page-token")

		if pageSize <= 0 {
			pageSize = 20
		}

		request := &proto.ListVolumesRequest{
			PageSize:  pageSize,
			PageToken: pageToken,
		}

		response, err := client.ListVolumes(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error listing volumes: %v", err)
		}

		if output == "json" {
			data, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(response)
			if err != nil {
				return err
			}
			cmd.OutOrStdout().Write(data)
			return nil
		} else if output == "table" {
			if len(response.Volumes) == 0 {
				cmd.Println("No volumes found")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSIZE (GB)\tSTATUS\tREGION")

			for _, volume := range response.Volumes {
				status := proto.VolumeStatus_name[int32(volume.Status)]
				fmt.Fprintf(w, "%d\t%s\t%d\t%s\t%s\n",
					volume.Id, volume.Name, volume.SizeGb, status, volume.Region)
			}
			w.Flush()
		}

		return nil
	},
}

func init() {
	volumeCmd.AddCommand(listVolumesCmd)
	flags := listVolumesCmd.Flags()
	flags.Int32P("page-size", "p", 20, "Number of volumes to list per page")
	flags.String("page-token", "", "Token for pagination")
	flags.StringP("output", "o", "table", "Output format. Options: table, json")
}
