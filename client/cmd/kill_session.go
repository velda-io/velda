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

var killSessionCmd = &cobra.Command{
	Use:     "kill-session",
	Aliases: []string{"kill"},
	Short:   "Kill a session of an instance",
	Args:    cobra.NoArgs,
	Long: `Kill an active session of an instance.
	
This will be done by terminating the compute resources attached to the session.
To soft-terminate the session, use vrun with OS kill command instead.`,

	RunE: func(cmd *cobra.Command, args []string) error {
		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(),
			cmd.Flag("instance").Value.String(),
			clientlib.FallbackToSession)
		if err != nil {
			return err
		}
		serviceName, _ := cmd.Flags().GetString("service-name")
		sessionId, _ := cmd.Flags().GetString("session-id")
		force, _ := cmd.Flags().GetBool("force")

		if sessionId == "" && serviceName == "" {
			return fmt.Errorf("You must specify either --session-id or --service-name to kill a session")
		}

		if sessionId != "" && serviceName != "" {
			return fmt.Errorf("You cannot specify both --session-id and --service-name")
		}
		brokerClient, err := clientlib.GetBrokerClient()
		if err != nil {
			return err
		}
		_, err = brokerClient.KillSession(cmd.Context(), &proto.KillSessionRequest{
			InstanceId:  instanceId,
			SessionId:   sessionId,
			ServiceName: serviceName,
			Force:       force,
		})
		if err != nil {
			return err
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(killSessionCmd)
	killSessionCmd.Flags().StringP("instance", "i", "", "Instance name or ID. Default to the current instance if running in Velda, or default-instance clientlib.")
	killSessionCmd.Flags().StringP("service-name", "s", "", "If specified, all sessions with this service name will be killed.")
	killSessionCmd.Flags().String("session-id", "", "Specify the session ID to kill.")
	killSessionCmd.Flags().BoolP("force", "f", false, "Force kill the session without waiting for cleanup.")
}
