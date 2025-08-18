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
	"path"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"velda.io/velda/pkg/utils"
)

var miniDownCmd = &cobra.Command{
	Use:   "down sandbox-dir",
	Short: "Bring down a mini-Velda cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Usage()
		}
		sandboxDir := args[0]
		if sandboxDir == "" {
			return fmt.Errorf("Sandbox directory must be specified")
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
		return stopMini(cmd, sandboxDir)
	},
}

func init() {
	MiniCmd.AddCommand(miniDownCmd)
}

func stopMini(cmd *cobra.Command, sandboxDir string) error {
	stopMiniAgent(cmd)
	if err := stopMiniApiserver(sandboxDir); err != nil {
		return err
	}
	cmd.PrintErrf("%s%sMini cluster stopped successfully%s\n", utils.ColorBold, utils.ColorGreen, utils.ColorReset)
	return nil
}

func stopMiniApiserver(sandboxDir string) error {
	pidfile := path.Join(sandboxDir, "apiserver.pid")
	pidBytes, err := os.ReadFile(pidfile)
	if os.IsNotExist(err) {
		return fmt.Errorf("API server PID file does not exist. Is service actually running?")
	}
	if err != nil {
		return fmt.Errorf("failed to read API server PID file: %w", err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		return fmt.Errorf("failed to parse API server PID: %w", err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find API server process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop API server process: %w", err)
	}
	if err := os.Remove(pidfile); err != nil {
		return fmt.Errorf("failed to remove API server PID file: %w", err)
	}
	return nil
}
