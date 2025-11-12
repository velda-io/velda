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
	"strings"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var setTagSessionCmd = &cobra.Command{
	Use:   "set-tag --session-id=<session-id> --tags=key=value,key2=value2",
	Short: "Set or update tags for a session",
	Args:  cobra.NoArgs,
	Long: `Set or update tags for a session.

Tags are key-value pairs that can be used to organize and categorize sessions.
To remove a tag, set its value to an empty string.

Examples:
  # Set tags for a session
  velda set-tag --session-id=abc123 --tags=env=prod,team=ml

  # Remove a tag by setting empty value
  velda set-tag --session-id=abc123 --tags=env=,team=ml
`,

	RunE: func(cmd *cobra.Command, args []string) error {
		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(),
			cmd.Flag("instance").Value.String(),
			clientlib.FallbackToSession)
		if err != nil {
			return err
		}
		sessionId, _ := cmd.Flags().GetString("session-id")
		tagsStr, _ := cmd.Flags().GetString("tags")

		if sessionId == "" {
			return fmt.Errorf("You must specify --session-id")
		}

		if tagsStr == "" {
			return fmt.Errorf("You must specify --tags in format key=value,key2=value2")
		}

		// Parse tags from the format "key=value,key2=value2"
		tags := make(map[string]string)
		tagPairs := strings.Split(tagsStr, ",")
		for _, pair := range tagPairs {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("Invalid tag format: %s. Expected key=value", pair)
			}
			tags[parts[0]] = parts[1]
		}

		brokerClient, err := clientlib.GetBrokerClient()
		if err != nil {
			return err
		}
		_, err = brokerClient.SetTag(cmd.Context(), &proto.SetTagRequest{
			InstanceId: instanceId,
			SessionId:  sessionId,
			Tags:       tags,
		})
		if err != nil {
			return err
		}
		quiet, _ := cmd.Flags().GetBool("quiet")
		if !quiet {
			cmd.Println("Tags updated successfully")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(setTagSessionCmd)
	setTagSessionCmd.Flags().StringP("instance", "i", "", "Instance name or ID. Default to the current instance if running in Velda, or default-instance clientlib.")
	setTagSessionCmd.Flags().String("session-id", "", "Specify the session ID to update.")
	setTagSessionCmd.Flags().String("tags", "", "Tags to set in format key=value,key2=value2. Set value to empty string to remove a tag.")
	setTagSessionCmd.Flags().BoolP("quiet", "q", false, "Suppress output messages.")
}
