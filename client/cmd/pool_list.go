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
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var listPoolCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available pools",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %w", err)
		}
		client := proto.NewPoolManagerServiceClient(conn)

		pools, err := client.ListPools(cmd.Context(), &proto.ListPoolsRequest{})
		if err != nil {
			return fmt.Errorf("Error listing pools: %w", err)
		}
		// print as a table: NAME and DESCRIPTION
		w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION")
		for _, pool := range pools.Pools {
			desc := pool.Description
			if desc == "" {
				desc = "-"
			}
			fmt.Fprintf(w, "%s\t%s\n", pool.Name, desc)
		}
		w.Flush()
		return nil
	},
}

func init() {
	poolCmd.AddCommand(listPoolCmd)
}
