// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var allowedOutputFormats = []string{"table", "json"}

var listSessionCmd = &cobra.Command{
	Use:     "list-session",
	Aliases: []string{"ls", "list-sessions"},
	Short:   "List sessions of an instance",
	Args:    cobra.NoArgs,
	Long: `List active sessions of an instance.

Session is an instance with active compute resource attached.

The pool (configured by system administrator) determines the type
and amount of compute resources attached to the session.
`,

	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")
		if !slices.Contains(allowedOutputFormats, output) {
			return fmt.Errorf("Invalid output format: %s", output)
		}
		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(),
			cmd.Flag("instance").Value.String(),
			clientlib.FallbackToSession)
		if err != nil {
			return err
		}
		brokerClient, err := clientlib.GetBrokerClient()
		if err != nil {
			return err
		}
		sessions, err := brokerClient.ListSessions(cmd.Context(), &proto.ListSessionsRequest{
			InstanceId:  instanceId,
			ServiceName: cmd.Flag("service_name").Value.String(),
		})
		if err != nil {
			return err
		}
		if len(sessions.Sessions) == 0 {
			cmd.Println("No sessions found")
			return nil
		}
		if output == "json" {
			data, err := json.MarshalIndent(sessions, "", "  ")
			if err != nil {
				return err
			}
			cmd.Println(string(data))
			return nil
		} else if output == "table" {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "Session ID\tPool\tStatus\tService")
			for _, session := range sessions.Sessions {
				statusStr := session.Status.String()[len("STATUS_"):]
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", session.SessionId, session.Pool, statusStr, session.ServiceName)
			}
			w.Flush()
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listSessionCmd)

	listSessionCmd.Flags().StringP("instance", "i", "", "Instance name or ID. Default to the current instance if running in Velda, or default-instance clientlib.")
	listSessionCmd.Flags().StringP("service_name", "s", "", "If specified, only list sessions that are bound to this service.")
	listSessionCmd.Flags().StringP("output", "o", "table", "Output format. Options: table, json")
}
