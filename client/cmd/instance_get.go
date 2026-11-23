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

var getInstanceCmd = &cobra.Command{
	Use:   "get <instance>",
	Short: "Get instance details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewInstanceServiceClient(conn)

		instanceName := args[0]
		instanceID, err := clientlib.ParseInstanceId(
			cmd.Context(), instanceName, !clientlib.FallbackToSession)
		if err != nil {
			return err
		}

		instance, err := client.GetInstance(cmd.Context(), &proto.GetInstanceRequest{
			InstanceId: instanceID,
		})
		if err != nil {
			return fmt.Errorf("Error getting instance: %v", err)
		}

		// Output the instance as JSON
		marshaler := protojson.MarshalOptions{
			Indent:          "  ",
			EmitUnpopulated: true,
		}
		output, err := marshaler.Marshal(instance)
		if err != nil {
			return fmt.Errorf("Error marshaling response: %v", err)
		}
		fmt.Println(string(output))

		return nil
	},
}

func init() {
	instanceCmd.AddCommand(getInstanceCmd)
}
