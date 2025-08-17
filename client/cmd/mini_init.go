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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
	"velda.io/velda/pkg/broker/backends"
	configpb "velda.io/velda/pkg/proto/config"
)

var miniInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a Velda-mini sandbox",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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
		if _, err := os.Stat(sandboxDir); !os.IsNotExist(err) {
			return fmt.Errorf("sandbox directory %s already exists: %w", sandboxDir, err)
		}
		os.MkdirAll(sandboxDir, 0755)
		if err := initMiniConfig(cmd, sandboxDir); err != nil {
			return fmt.Errorf("failed to initialize mini config: %w", err)
		}
		if err := initMiniSandbox(cmd, sandboxDir); err != nil {
			return fmt.Errorf("failed to initialize mini: %w", err)
		}
		return startMini(cmd, sandboxDir)
	},
}

func init() {
	MiniCmd.AddCommand(miniInitCmd)
	miniInitCmd.Flags().String("agent-launcher", "docker", "The agent launcher to use (docker)")
	miniInitCmd.Flags().String("base-image", "ubuntu:24.04", "The docker image to initialize the sandbox")
	miniInitCmd.Flags().StringSlice("backends", []string{"all"}, "The backends to enable (comma-separated list, e.g., 'aws,gce')")
}

func initMiniConfig(cmd *cobra.Command, sandboxDir string) error {
	root := path.Join(sandboxDir, "root", "0", "1")
	config := &configpb.Config{
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
	err := initBackends(cmd, config)
	if err != nil {
		return err
	}
	cfgFile := path.Join(sandboxDir, "service.yaml")
	cfgJson, err := protojson.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config to JSON: %w", err)
	}
	cfgJsonParsed := map[string]interface{}{}
	if err := json.Unmarshal(cfgJson, &cfgJsonParsed); err != nil {
		return fmt.Errorf("failed to parse config JSON: %w", err)
	}
	cfgYaml, err := yaml.Marshal(cfgJsonParsed)
	if err != nil {
		return fmt.Errorf("failed to marshal config to YAML: %w", err)
	}
	if err := os.WriteFile(cfgFile, cfgYaml, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

func initMiniSandbox(cmd *cobra.Command, sandboxDir string) error {
	// Create the sandbox directory if it doesn't exist
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

	exportCmd.Stderr = os.Stderr
	p, err := exportCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	unpackCmd := exec.Command("sudo", "tar", "-xf", "-", "-C", rootDir)
	unpackCmd.Stdin = p
	unpackCmd.Stdout = os.Stdout
	unpackCmd.Stderr = os.Stderr
	if err := exportCmd.Start(); err != nil {
		return fmt.Errorf("failed to start export command: %w", err)
	}
	if err := unpackCmd.Run(); err != nil {
		return fmt.Errorf("failed to unpack tar: %w", err)
	}

	if err := exportCmd.Wait(); err != nil {
		return fmt.Errorf("failed to wait for export command: %w", err)
	}

	symlink("/run/velda/velda", path.Join(rootDir, "bin", "velda"))
	symlink("/run/velda/velda", path.Join(rootDir, "bin", "vrun"))
	symlink("/run/velda/velda", path.Join(rootDir, "bin", "vbatch"))
	symlink("/run/velda/velda", path.Join(rootDir, "sbin/mount.host"))
	return nil
}

func symlink(target, link string) {
	if _, err := os.Lstat(link); err == nil {
		return
	}
	output, err := exec.Command("sudo", "ln", "-s", target, link).CombinedOutput()
	if err != nil {
		log.Printf("Failed to create symlink %s -> %s: %v\nOutput: %s", link, target, err, output)
	}
}

func initBackends(cmd *cobra.Command, config *configpb.Config) error {
	backend, _ := cmd.Flags().GetStringSlice("backends")
	allowedBackends := map[string]bool{}
	for _, backend := range backend {
		allowedBackends[backend] = true
	}

	return backends.AutoConfigureMini(cmd, config, allowedBackends)
}
