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
	"os"
	"os/exec"
	"path"

	"github.com/spf13/cobra"
	"velda.io/velda/pkg/apiserver"
	"velda.io/velda/pkg/utils"
)

const currentSandboxLinkLocation = "/tmp/current-velda"

var clusterUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring up a Velda cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			cmd.PrintErrln("Usage: velda cluster up <sandbox-dir>")
			return fmt.Errorf("sandbox directory must be specified")
		}
		if p, err := os.Readlink(currentSandboxLinkLocation); err == nil {
			return fmt.Errorf("A cluster at %s may be already running. Use velda cluster down to stop it. If this is in error, remove %s", p, currentSandboxLinkLocation)
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
		if err := startCluster(cmd, sandboxDir); err != nil {
			return err
		}
		printClusterInstruction(cmd)
		return nil
	},
}

func init() {
	ClusterCmd.AddCommand(clusterUpCmd)
}

func startCluster(cmd *cobra.Command, sandboxDir string) error {
	if err := startLocalAgent(cmd, sandboxDir); err != nil {
		return err
	}
	if err := startClusterApiserver(cmd, sandboxDir); err != nil {
		return err
	}
	os.Symlink(sandboxDir, currentSandboxLinkLocation)
	return nil
}

func printClusterInstruction(cmd *cobra.Command) {
	cmd.PrintErrf("%s%sMini-velda cluster started successfully%s\n", utils.ColorBold, utils.ColorGreen, utils.ColorReset)
	cmd.PrintErrf("To stop the cluster, run %svelda mini down%s\n", utils.ColorYellow, utils.ColorReset)
	cmd.PrintErrf("To connect to the sandbox, run %svelda run%s or %sssh mini-velda%s\n", utils.ColorBold+utils.ColorCyan, utils.ColorReset, utils.ColorBold+utils.ColorCyan, utils.ColorReset)
	cmd.PrintErrf("To run workload with extra compute, run %svelda run -P [pool] cmdline%s from the sandbox\n", utils.ColorBold+utils.ColorCyan, utils.ColorReset)
	cmd.PrintErrf("To view available pools, use %svelda pool list%s\n", utils.ColorBold+utils.ColorCyan, utils.ColorReset)
}

func startClusterApiserver(cmd *cobra.Command, sandboxDir string) error {
	configPath := path.Join(sandboxDir, "service.yaml")
	err := apiserver.RunAsDaemon([]string{"apiserver", "--config", configPath},
		path.Join(sandboxDir, "apiserver.log"), path.Join(sandboxDir, "apiserver.pid")) // Use the daemon mode to run the service
	if err != nil {
		return fmt.Errorf("failed to start API server: %w", err)
	}
	cmd.PrintErrf("%sAPI server started successfully%s\n", utils.ColorGreen, utils.ColorReset)
	cmd.PrintErrf("%sLogs of API server: %s%s\n", utils.ColorLightGray, path.Join(sandboxDir, "apiserver.log"), utils.ColorReset)
	return nil
}

func startLocalAgent(cmd *cobra.Command, sandboxDir string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	var localPath string
	var server string
	launcher, _ := cmd.Flags().GetString("agent-launcher")
	switch launcher {
	case "docker":
		localPath = "/run/velda/instances"
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
	var logs string
	switch launcher {
	case "docker":
		cmd := exec.Command("docker", "run", "-d",
			"--name", "local-velda-agent",
			"--privileged",
			"--platform", "linux/amd64",
			"-h", "local-velda-main",
			"--add-host=host.docker.internal:host-gateway",
			"-v", fmt.Sprintf("%s:/run/velda", sandboxDir),
			"-v", fmt.Sprintf("%s:/bin/velda", executable), "ubuntu:24.04",
			"/bin/velda", "agent", "daemon")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to start docker container: %w", err)
		}
		logs = "docker logs local-velda-agent"
	default:
		return fmt.Errorf("unknown agent launcher: %s", launcher)
	}

	cmd.PrintErrf("%sLocal-velda agent started successfully%s\n", utils.ColorGreen, utils.ColorReset)
	cmd.PrintErrf("%sLogs of Local-velda agent: %s%s\n", utils.ColorLightGray, logs, utils.ColorReset)
	/*
		// Execute "docker logs -f local-velda-agent" in background to follow the logs
		cmdExec := exec.Command("sh", "-c", "docker logs -f local-velda-agent &")
		cmdExec.Stderr = os.Stderr
		if err := cmdExec.Start(); err != nil {
			return fmt.Errorf("failed to start following docker logs: %w", err)
		}
		cmdExec.Process.Release()
	*/
	return nil
}

func stopLocalAgent(cmd *cobra.Command) {
	launcher, _ := cmd.Flags().GetString("agent-launcher")
	switch launcher {
	case "docker":
		cmd := exec.Command("docker", "rm", "-f", "local-velda-agent")
		if err := cmd.Run(); err != nil {
			fmt.Printf("failed to stop docker container: %v\n", err)
		}
	default:
		fmt.Printf("unknown agent launcher: %s\n", launcher)
	}
}
