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
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"velda.io/velda/pkg/proto"
)

var mountHostCmd = &cobra.Command{
	Use:    "mount-hostdir",
	Short:  "Mount a directory from the host.",
	Hidden: true,
	Args:   cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		mountreq := &proto.MountRequest{
			Source:  args[0],
			Target:  args[1],
			Options: cmd.Flag("options").Value.String(),
			Fstype:  "host",
		}
		conn, err := grpc.NewClient("unix:///run/velda/agent.sock", grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return err
		}
		defer conn.Close()

		client := proto.NewAgentDaemonClient(conn)
		_, err = client.Mount(cmd.Context(), mountreq)
		if err != nil {
			return err
		}
		return nil
	},
}

func init() {
	AgentCmd.AddCommand(mountHostCmd)
	mountHostCmd.Flags().StringP("options", "o", "", "Mount options")
}
