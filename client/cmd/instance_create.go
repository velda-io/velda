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
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/sftp"
	"github.com/spf13/cobra"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

var createInstanceCmd = &cobra.Command{
	Use:   "create [-i <image> | -f instance | -d docker-image] <instance>",
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
		dockerImage, _ := cmd.Flags().GetString("docker-image")
		tarFile, _ := cmd.Flags().GetString("tar-file")

		// Validate that only one source is specified
		sourceCount := 0
		if image != "" {
			sourceCount++
		}
		if fromInstance != "" {
			sourceCount++
		}
		if dockerImage != "" {
			sourceCount++
		}
		if tarFile != "" {
			sourceCount++
		}
		if sourceCount > 1 {
			return fmt.Errorf("Only one of --image, --from-instance, or --docker-image can be specified")
		}

		// Handle docker-image creation
		if dockerImage != "" {
			return createInstanceFromDocker(cmd, client, name, dockerImage)
		}

		// Handle tar-file creation
		if tarFile != "" {
			return createInstanceFromTar(cmd, client, name, tarFile)
		}

		request := &proto.CreateInstanceRequest{
			Instance: &proto.Instance{
				InstanceName: name,
			},
		}
		if image != "" {
			request.Source = &proto.CreateInstanceRequest_ImageName{
				ImageName: image,
			}
			cmd.Printf("Using image %s\n", image)
		} else if fromInstance != "" {
			instanceId, err := clientlib.ParseInstanceId(
				cmd.Context(), fromInstance, clientlib.FallbackToSession)
			if err != nil {
				return fmt.Errorf("Error parsing instance ID: %v", err)
			}
			cmd.Printf("Cloning from instance %d@%s\n", instanceId, snapshotName)
			request.Source = &proto.CreateInstanceRequest_Snapshot{
				Snapshot: &proto.SnapshotReference{
					InstanceId:   instanceId,
					SnapshotName: snapshotName,
				},
			}
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
	flags.StringP("docker-image", "d", "", "Docker image to initialize the instance from (e.g., ubuntu:24.04)")
	flags.String("tar-file", "", "Path to local tar file to initialize the instance from")
	flags.BoolP("verbose", "v", false, "Enable verbose output during instance creation")
	flags.BoolP("quiet", "q", false, "Suppress status output (still prints docker stderr on error)")
	flags.Bool("no-init", false, "Skip running the initialization script when creating from a Docker image")
}

// createInstanceFromDocker creates an instance and initializes it from a Docker image
func createInstanceFromDocker(cmd *cobra.Command, client proto.InstanceServiceClient, name, dockerImage string) error {
	quiet, _ := cmd.Flags().GetBool("quiet")
	verbose, _ := cmd.Flags().GetBool("verbose")
	pr := func(format string, a ...interface{}) {
		if !quiet {
			cmd.Printf(format, a...)
		}
	}
	pr("Creating instance %s from Docker image %s\n", name, dockerImage)

	// Create an empty instance first
	request := &proto.CreateInstanceRequest{
		Instance: &proto.Instance{
			InstanceName: name,
		},
	}

	instance, err := client.CreateInstance(cmd.Context(), request)
	if err != nil {
		return fmt.Errorf("Error creating instance: %v", err)
	}
	pr("Instance %s created with ID %d\n", instance.InstanceName, instance.Id)

	// Connect to instance and copy files (stream docker export directly)
	conn, err := clientlib.GetApiConnection()
	if err != nil {
		return fmt.Errorf("failed to get API connection: %v", err)
	}
	defer conn.Close()

	instanceId, err := clientlib.ParseInstanceId(cmd.Context(), name, clientlib.FallbackToSession)
	if err != nil {
		return fmt.Errorf("failed to parse instance ID: %v", err)
	}

	// Create the Docker container before requesting a velda session so the
	// container exists when the session starts.
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is not installed or not in the PATH")
	}
	createCmd := exec.Command("docker", "create", "--platform", "linux/amd64", dockerImage)
	var createStderr bytes.Buffer
	if quiet {
		createCmd.Stderr = &createStderr
	} else {
		// stream docker stderr live when not quiet
		createCmd.Stderr = cmd.ErrOrStderr()
	}
	containerOutput, err := createCmd.Output()
	if err != nil {
		// Print docker stderr even in quiet mode
		if createStderr.Len() > 0 {
			cmd.ErrOrStderr().Write(createStderr.Bytes())
		}
		return fmt.Errorf("failed to create Docker container: %v", err)
	}
	containerID := strings.TrimSpace(string(containerOutput))
	defer func() {
		// cleanup container if session request fails
		exec.Command("docker", "rm", containerID).Run()
	}()

	brokerClient, err := clientlib.GetBrokerClient()
	if err != nil {
		return fmt.Errorf("failed to get broker client: %v", err)
	}

	// Request a session for file copying
	resp, err := brokerClient.RequestSession(cmd.Context(), &proto.SessionRequest{
		ServiceName: "docker-init",
		InstanceId:  instanceId,
		Pool:        "shell",
		User:        "root",
	})
	if err != nil {
		return fmt.Errorf("failed to request session: %v", err)
	}

	// Connect via SSH
	sshClient, err := clientlib.SshConnect(cmd, resp.GetSshConnection(), "root")
	if err != nil {
		return fmt.Errorf("failed to connect to SSH: %v", err)
	}
	defer sshClient.Close()

	// Create SFTP client
	sftpClient, err := sftp.NewClient(sshClient.Client)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %v", err)
	}
	defer sftpClient.Close()

	// Copy files to instance (stream from docker export)
	if err := streamDockerImageToSftp(cmd, containerID, sftpClient, verbose, quiet); err != nil {
		return fmt.Errorf("failed to stream docker image to instance: %v", err)
	}

	// Run initialization script unless --no-init is specified
	noInit, _ := cmd.Flags().GetBool("no-init")
	if !noInit {
		scriptContent := getInitSandboxScript()
		if err := runInitScript(cmd, sshClient, scriptContent, quiet); err != nil {
			return fmt.Errorf("failed to run init script: %v", err)
		}
	} else {
		pr("Skipping initialization script (--no-init specified)\n")
	}

	pr("✓ Instance %s successfully initialized from Docker image %s\n", name, dockerImage)
	pr("Use `velda run --instance %s` to connect to the instance.\n", name)

	return nil
}

// createInstanceFromTar creates an instance and initializes it from a local tar file
func createInstanceFromTar(cmd *cobra.Command, client proto.InstanceServiceClient, name, tarFile string) error {
	quiet, _ := cmd.Flags().GetBool("quiet")
	verbose, _ := cmd.Flags().GetBool("verbose")
	pr := func(format string, a ...interface{}) {
		if !quiet {
			cmd.Printf(format, a...)
		}
	}
	pr("Creating instance %s from tar file %s\n", name, tarFile)

	// Create an empty instance first
	request := &proto.CreateInstanceRequest{
		Instance: &proto.Instance{
			InstanceName: name,
		},
	}

	instance, err := client.CreateInstance(cmd.Context(), request)
	if err != nil {
		return fmt.Errorf("Error creating instance: %v", err)
	}
	pr("Instance %s created with ID %d\n", instance.InstanceName, instance.Id)

	// Parse instance ID
	instanceId, err := clientlib.ParseInstanceId(cmd.Context(), name, clientlib.FallbackToSession)
	if err != nil {
		return fmt.Errorf("failed to parse instance ID: %v", err)
	}

	// Request a session for file copying
	brokerClient, err := clientlib.GetBrokerClient()
	if err != nil {
		return fmt.Errorf("failed to get broker client: %v", err)
	}
	resp, err := brokerClient.RequestSession(cmd.Context(), &proto.SessionRequest{
		ServiceName: "docker-init",
		InstanceId:  instanceId,
		Pool:        "shell",
		User:        "root",
	})
	if err != nil {
		return fmt.Errorf("failed to request session: %v", err)
	}

	// Connect via SSH
	sshClient, err := clientlib.SshConnect(cmd, resp.GetSshConnection(), "root")
	if err != nil {
		return fmt.Errorf("failed to connect to SSH: %v", err)
	}
	defer sshClient.Close()

	// Create SFTP client
	sftpClient, err := sftp.NewClient(sshClient.Client)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %v", err)
	}
	defer sftpClient.Close()

	// Open tar file and stream to instance
	f, err := os.Open(tarFile)
	if err != nil {
		return fmt.Errorf("failed to open tar file: %v", err)
	}
	defer f.Close()

	if err := streamTarReaderToSftp(cmd, f, sftpClient, verbose, quiet); err != nil {
		return fmt.Errorf("failed to stream tar to instance: %v", err)
	}

	// Run initialization script unless --no-init is specified
	noInit, _ := cmd.Flags().GetBool("no-init")
	if !noInit {
		scriptContent := getInitSandboxScript()
		if err := runInitScript(cmd, sshClient, scriptContent, quiet); err != nil {
			return fmt.Errorf("failed to run init script: %v", err)
		}
	} else {
		pr("Skipping initialization script (--no-init specified)\n")
	}

	pr("✓ Instance %s successfully initialized from tar file %s\n", name, tarFile)
	pr("Use `velda run --instance %s` to connect to the instance.\n", name)

	return nil
}
