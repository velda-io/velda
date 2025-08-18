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
	"log"
	"os"
	"os/exec"
	"path"

	"github.com/spf13/cobra"
	"velda.io/velda/pkg/apiserver"
	"velda.io/velda/pkg/utils"
)

var miniUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring up a mini-Velda cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		sandboxDir := args[0]
		if sandboxDir == "" {
			return cmd.Help()
		}
		if sandboxDir[0] != '/' {
			// If the path is not absolute, make it relative to the current directory
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}
			sandboxDir = path.Join(cwd, sandboxDir)
		}
		if stat, err := os.Stat(sandboxDir); err != nil || !stat.IsDir() {
			return fmt.Errorf("sandbox directory %s does not exist or is not a directory: %w", sandboxDir, err)
		}
		return startMini(cmd, sandboxDir)
	},
}

func init() {
	MiniCmd.AddCommand(miniUpCmd)
}

func startMini(cmd *cobra.Command, sandboxDir string) error {
	if err := startMiniAgent(cmd, sandboxDir); err != nil {
		return err
	}
	if err := startMiniApiserver(sandboxDir); err != nil {
		return err
	}
	cmd.PrintErrf("%s%sMini cluster started successfully%s\n", utils.ColorBold, utils.ColorGreen, utils.ColorReset)
	cmd.PrintErrf("%sTo terminate the cluster, use %svelda mini down%s\n", utils.ColorLightGray, utils.ColorReset, utils.ColorReset)
	cmd.PrintErrf("Use %svelda run%s or %sssh velda-mini%s to connect to it.\n", utils.ColorBold, utils.ColorReset, utils.ColorBold, utils.ColorReset)
	return nil
}

func startMiniApiserver(sandboxDir string) error {
	go apiserver.StartMetricServer("localhost:6060")
	configPath := path.Join(sandboxDir, "service.yaml")
	err := apiserver.RunAsDaemon([]string{"apiserver", "--config", configPath},
		path.Join(sandboxDir, "apiserver.log"), path.Join(sandboxDir, "apiserver.pid")) // Use the daemon mode to run the service
	if err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	log.Printf("API server logs at %s", path.Join(sandboxDir, "apiserver.log"))
	return nil
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
			"--platform", "linux/amd64",
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
