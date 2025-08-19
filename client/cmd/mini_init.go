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
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	"velda.io/velda/pkg/broker/backends"
	"velda.io/velda/pkg/clientlib"
	configpb "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

var miniInitCmd = &cobra.Command{
	Use:   "init sandbox-dir",
	Short: "Initialize a Velda-mini sandbox",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		if err := checkEnv(cmd); err != nil {
			return err
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
		profile := "mini"
		var profileCfg *clientlib.Configs
		if cfg, err := clientlib.LoadConfigs("mini"); err == nil {
			profileCfg = cfg
		} else {
			log.Printf("Failed to load %v", err)
			_, cfg, err := clientlib.InitCurrentConfig("localhost:50051", true)
			profileCfg = cfg
			if err != nil {
				return fmt.Errorf("Error initializing config: %w", err)
			}
			if err := cfg.RenameConfig(profile); err != nil {
				return fmt.Errorf("Error renaming config: %w", err)
			}
			// There's only one instance.
			cfg.SetConfig("default-instance", "1")
		}
		if err := profileCfg.MakeCurrent(); err != nil {
			return fmt.Errorf("Error making config current: %w", err)
		}
		if err := configSsh(cmd); err != nil {
			// This is a warning.
			cmd.PrintErrln("Failed to configure SSH client for mini cluster")
		}
		cmd.PrintErrf("%s%sInitialized mini config%s\n", utils.ColorBold, utils.ColorGreen, utils.ColorReset)
		if err := startMini(cmd, sandboxDir); err != nil {
			return fmt.Errorf("failed to start mini cluster: %w", err)
		}
		return nil
	},
}

func init() {
	MiniCmd.AddCommand(miniInitCmd)
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
		"docker", "create", "--platform", "linux/amd64", baseImage,
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

func checkEnv(cmd *cobra.Command) error {
	if _, err := exec.LookPath("docker"); err != nil {
		cmd.PrintErrln("Docker is not installed or not in the PATH")
		return fmt.Errorf("docker not found")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		cmd.PrintErrln("Sudo is not installed or not in the PATH")
		return fmt.Errorf("sudo not found")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		cmd.PrintErrln("Tar is not installed or not in the PATH")
		return fmt.Errorf("tar not found")
	}
	if _, err := exec.LookPath("exportfs"); err != nil {
		cmd.PrintErrln("exportfs is not installed or not in the PATH. Use apt install nfs-kernel-server to install it.")
		return fmt.Errorf("exportfs not found")
	}
	return nil
}

func configSsh(cmd *cobra.Command) error {
	// Determine current executable full path
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine current executable: %w", err)
	}
	// Resolve symlinks and make absolute
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to determine user home directory: %w", err)
	}
	sshDir := path.Join(home, ".ssh")
	cfgPath := path.Join(sshDir, "config")

	// Ensure ~/.ssh exists
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("failed to create .ssh directory: %w", err)
	}

	// Read existing config if present
	data, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read ssh config: %w", err)
	}

	// Desired ProxyCommand line (without leading indentation)
	desiredProxy := fmt.Sprintf("ProxyCommand %s port-forward -W -p 2222 -s ssh --profile mini -u %%r", exe)

	// Canonical block lines
	canonical := []string{
		"Host velda-mini",
		"    User user",
		"    StrictHostKeyChecking no",
		"    UserKnownHostsFile /dev/null",
		"    " + desiredProxy,
	}

	// If config empty, just write canonical block
	if len(data) == 0 {
		entry := "\n" + strings.Join(canonical, "\n") + "\n"
		if err := os.WriteFile(cfgPath, []byte(entry), 0600); err != nil {
			return fmt.Errorf("failed to write ssh config: %w", err)
		}
		cmd.Printf("Appended SSH config for host 'velda-mini' to %s\n", cfgPath)
		return nil
	}

	lines := strings.Split(string(data), "\n")
	changed := false

	// Iterate and replace any Host velda-mini block entirely
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && strings.ToLower(fields[0]) == "host" {
			// check if this host line includes velda-mini
			matches := false
			for _, h := range fields[1:] {
				if h == "velda-mini" {
					matches = true
					break
				}
			}
			if matches {
				// find end of this Host block (next Host line or EOF)
				j := i + 1
				for j < len(lines) {
					t := strings.TrimSpace(lines[j])
					f := strings.Fields(t)
					if len(f) >= 1 && strings.ToLower(f[0]) == "host" {
						break
					}
					j++
				}
				// Replace lines[i:j] with canonical
				newLines := make([]string, 0, len(lines)-(j-i)+len(canonical))
				newLines = append(newLines, lines[:i]...)
				newLines = append(newLines, canonical...)
				if j < len(lines) {
					newLines = append(newLines, lines[j:]...)
				}
				lines = newLines
				changed = true
				// advance i past the inserted canonical block
				i += len(canonical)
				continue
			}
		}
		i++
	}

	// If there was no existing Host velda-mini, append it
	if !changed {
		lines = append(lines, "")
		lines = append(lines, canonical...)
		lines = append(lines, "")
		changed = true
	}

	if changed {
		out := strings.Join(lines, "\n")
		if err := os.WriteFile(cfgPath, []byte(out), 0600); err != nil {
			return fmt.Errorf("failed to write ssh config: %w", err)
		}
		cmd.Printf("Updated SSH config for host 'velda-mini' in %s\n", cfgPath)
	}

	return nil
}
