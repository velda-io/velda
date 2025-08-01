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
	"fmt"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var createInstanceCmd = &cobra.Command{
	Use:   "create [-i <image> | -f instance] <instance>",
	Short: "Create a new instance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()
		client := proto.NewInstanceServiceClient(conn)

		name := args[0]
		if name == "" {
			return fmt.Errorf("Name is required")
		}
		image, _ := cmd.Flags().GetString("image")
		fromInstance, _ := cmd.Flags().GetString("from-instance")
		snapshotName, _ := cmd.Flags().GetString("snapshot")
		request := &proto.CreateInstanceRequest{
			Instance: &proto.Instance{
				InstanceName: name,
			},
		}
		if image != "" {
			request.Source = &proto.CreateInstanceRequest_ImageName{
				image,
			}
			cmd.Printf("Using image %s\n", image)
		} else if fromInstance != "" {
			instanceId, err := clientlib.ParseInstanceId(
				cmd.Context(), fromInstance, clientlib.FallbackToSession)
			if err != nil {
				return fmt.Errorf("Error parsing instance ID: %v", err)
			}
			cmd.Printf("Cloning from instance %d@%s\n", instanceId, snapshotName)
			request.Source = &proto.CreateInstanceRequest_Snapshot{&proto.SnapshotReference{
				InstanceId:   instanceId,
				SnapshotName: snapshotName,
			}}
		} else {
			cmd.Println(`No image or instance specified, creating empty instance.
Use scp/SFTP to upload files to the instance.`)
		}
		cmd.Printf("Creating instance %s\n", name)
		instance, err := client.CreateInstance(cmd.Context(), request)
		if err != nil {
			return fmt.Errorf("Error creating instance: %v", err)
		}
		cmd.Printf("Instance %s created with ID %d\n",
			instance.InstanceName, instance.Id)
		return nil
	},
}

func init() {
	instanceCmd.AddCommand(createInstanceCmd)
	flags := createInstanceCmd.Flags()
	flags.StringP("image", "i", "", "Name of the image to create the instance from")
	flags.StringP("from-instance", "f", "", "Name of the instance to clone from")
	flags.String("snapshot", "", "Name of the snapshot to create the instance from. If not provided, it will create one from the current instance disk using timestamped-name.")
}
