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
	"archive/tar"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/spf13/cobra"
	"velda.io/velda/pkg/apiserver"

	configpb "velda.io/velda/pkg/proto/config"
)

var miniStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a Velda-mini cluster",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sandboxDir := args[0]
		if sandboxDir == "" {
			return cmd.Help()
		}
		if err := initMini(cmd, sandboxDir); err != nil {
			return fmt.Errorf("failed to initialize mini: %w", err)
		}
		if err := startMiniAgent(cmd, sandboxDir); err != nil {
			return err
		}
		defer stopMiniAgent(cmd)
		if err := startMiniApiserver(sandboxDir); err != nil {
			return err
		}
		return nil
	},
}

func init() {
	MiniCmd.AddCommand(miniStartCmd)
	miniStartCmd.Flags().String("agent-launcher", "docker", "The agent launcher to use (docker, shell)")
	miniStartCmd.Flags().String("base-image", "ubuntu:24.04", "The docker image to initialize the sandbox")
}

func startMiniApiserver(sandboxDir string) error {
	root := path.Join(sandboxDir, "root", "0", "1")
	s := &apiserver.OssService{}
	s.Config = &configpb.Config{
		Server: &configpb.Server{
			GrpcAddress: ":50051",
			HttpAddress: ":8081",
		},
		Storage: &configpb.Storage{
			Storage: &configpb.Storage_Mini{
				Mini: &configpb.Storage_MiniVelda{
					Root: root,
				},
			},
		},
		AgentPools: []*configpb.AgentPool{
			{
				Name: "shell",
			},
		},
	}
	return apiserver.RunService(s)
}

func startMiniAgent(cmd *cobra.Command, sandboxDir string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	var localPath string
	var server string
	launcher, _ := cmd.Flags().GetString("agent-launcher")
	switch launcher {
	case "docker":
		localPath = "/run/velda/root"
		server = "host.docker.internal:50051"
	default:
		return fmt.Errorf("unknown agent launcher: %s", launcher)
	}

	agentConfig := fmt.Sprintf(`
broker:
  address: "%s"
sandbox_config:
  disk_source:
   mounted_disk_source:
     local_path: "%s"
daemon_config:
pool: shell`, server, localPath)
	agentConfigPath := path.Join(sandboxDir, "velda.yaml")
	if err := os.WriteFile(agentConfigPath, []byte(agentConfig), 0644); err != nil {
		return fmt.Errorf("failed to write agent config file: %w", err)
	}
	switch launcher {
	case "docker":
		cmd := exec.Command("docker", "run", "-d",
			"--name", "velda-mini-agent",
			"--privileged",
			"-h", "velda-mini-main",
			"--add-host=host.docker.internal:host-gateway",
			"-v", fmt.Sprintf("%s:/run/velda", sandboxDir),
			"-v", fmt.Sprintf("%s:/bin/velda", executable), "ubuntu:24.04",
			"/bin/velda", "agent", "daemon")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to start docker container: %w", err)
		}
	default:
		return fmt.Errorf("unknown agent launcher: %s", launcher)
	}
	return nil
}

func stopMiniAgent(cmd *cobra.Command) {
	launcher, _ := cmd.Flags().GetString("agent-launcher")
	switch launcher {
	case "docker":
		cmd := exec.Command("docker", "rm", "-f", "velda-mini-agent")
		if err := cmd.Run(); err != nil {
			fmt.Printf("failed to stop docker container: %v\n", err)
		}
	default:
		fmt.Printf("unknown agent launcher: %s\n", launcher)
	}
}

func initMini(cmd *cobra.Command, sandboxDir string) error {
	// Create the sandbox directory if it doesn't exist
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create sandbox directory: %w", err)
	}
	rootDir := path.Join(sandboxDir, "root", "0", "1")
	stat, err := os.Stat(rootDir)

	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat root directory: %w", err)
	}
	if err == nil && !stat.IsDir() {
		return fmt.Errorf("root directory %s exists but is not a directory", rootDir)
	}
	if err == nil {
		return nil
	}
	baseImage, _ := cmd.Flags().GetString("base-image")
	log.Printf("Creating dev-sandbox from docker image %s", baseImage)
	// Initialize the root directory
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return fmt.Errorf("failed to create root directory: %w", err)
	}
	createCmd := exec.Command(
		"docker", "create", baseImage,
	)
	createCmd.Stderr = os.Stderr
	container, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create base image container: %w", err)
	}
	containerID := string(container[:len(container)-1]) // Remove trailing newline
	defer func() {
		exec.Command("docker", "rm", containerID).Run()
	}()
	exportCmd := exec.Command(
		"docker", "export", containerID,
	)

	p, err := exportCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := exportCmd.Start(); err != nil {
		return fmt.Errorf("failed to start export command: %w", err)
	}
	tr := tar.NewReader(p)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %v\n", err)
		}

		target := filepath.Join(rootDir, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory: %v\n", err)
			}
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file: %v\n", err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to write file: %v\n", err)
			}
			f.Close()
			if err := os.Chown(target, header.Uid, header.Gid); err != nil {
				return fmt.Errorf("Failed to update owner")
			}
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("failed to create symlink: %v\n", err)
			}
		}
	}

	if err := exportCmd.Wait(); err != nil {
		return fmt.Errorf("failed to wait for export command: %w", err)
	}

	os.Symlink("/run/velda/velda", path.Join(rootDir, "bin", "velda"))
	os.Symlink("/run/velda/velda", path.Join(rootDir, "bin", "vrun"))
	os.Symlink("/run/velda/velda", path.Join(rootDir, "bin", "vbatch"))
	os.Symlink("/run/velda/velda", path.Join(rootDir, "bin", "/sbin/mount.host"))
	return nil
}
