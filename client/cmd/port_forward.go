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
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

// portForwardCmd represents the portForward command
var portForwardCmd = &cobra.Command{
	Use:     "port-forward [-i instance] [-s session] [-l localhost:port] -p remote-port",
	Example: "velda port-forward -s ssh -l localhost:2222 -p 22",
	Short:   "Establiash a tunnel for forwarding TCP connections",
	RunE: func(cmd *cobra.Command, args []string) error {
		var lis net.Listener
		var err error
		directWrite, _ := cmd.Flags().GetBool("write-direct")
		if !directWrite {
			lis, err = net.Listen("tcp", cmd.Flag("local").Value.String())
			if err != nil {
				return fmt.Errorf("Error listening: %v", err)
			}
		}

		conn, err := clientlib.GetApiConnection()
		if err != nil {
			return fmt.Errorf("Error getting API connection: %v", err)
		}
		defer conn.Close()

		instance := cmd.Flag("instance").Value.String()
		instanceId, err := clientlib.ParseInstanceId(
			cmd.Context(), instance, clientlib.FallbackToSession)

		if err != nil {
			return err
		}

		brokerClient, err := clientlib.GetBrokerClient()
		if err != nil {
			return err
		}

		user, _ := cmd.Flags().GetString("user")
		resp, err := brokerClient.RequestSession(cmd.Context(), &proto.SessionRequest{
			ServiceName: cmd.Flag("service-name").Value.String(),
			InstanceId:  instanceId,
			Pool:        cmd.Flag("pool").Value.String(),
			User:        user,
		})
		if err != nil {
			return err
		}

		client, err := clientlib.SshConnect(cmd, resp.GetSshConnection(), user)
		if err != nil {
			return fmt.Errorf("Error connecting to SSH: %v", err)
		}
		defer client.Close()
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		port, _ := cmd.Flags().GetInt("port")
		if directWrite {
			conn, err := client.DialContext(ctx, "tcp", fmt.Sprintf("localhost:%d", port))
			if err != nil {
				return fmt.Errorf("Error dialing: %v", err)
			}
			defer conn.Close()
			go io.Copy(conn, os.Stdin)
			io.Copy(os.Stdout, conn)
			return nil
		} else {
			go func() {
				sig := make(chan os.Signal, 1)
				signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
				<-sig
				cancel()
				lis.Close()
			}()

			cmd.Printf("Accepting connections on %s\n", lis.Addr().String())
			return client.PortForward(ctx, lis, port)
		}
	},
}

func init() {
	rootCmd.AddCommand(portForwardCmd)
	portForwardCmd.Flags().StringP("instance", "i", "", "Instance name or ID")
	portForwardCmd.Flags().StringP("service-name", "s", "", "Service name, which can be used to identify session later.")
	portForwardCmd.Flags().StringP("local", "l", ":0", "Local address")
	portForwardCmd.Flags().IntP("port", "p", 0, "Remote port")
	portForwardCmd.MarkFlagRequired("port")
	portForwardCmd.Flags().String("pool", "shell", "Pool name to create session if session does not exist.")
	portForwardCmd.Flags().StringP("user", "u", "user", "User to login as")
	portForwardCmd.Flags().BoolP("write-direct", "W", false, "Directly use STDIN/STDOUT for operations.")
}
