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
	"crypto/ed25519"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	"velda.io/velda/pkg/broker/backends"
	"velda.io/velda/pkg/clientlib"
	configpb "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

var clusterInitCmd = &cobra.Command{
	Use:   "init sandbox-dir",
	Short: "Initialize a velda Cluster",
	Long: `
This command initialize a new velda cluster from the current machine.

The sandbox-dir will be the path to the directory where the velda environment will be stored, including config and sandbox data.
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if p, err := os.Readlink(currentSandboxLinkLocation); err == nil {
			return fmt.Errorf("A cluster at %s may be already running. Use velda cluster down to stop it. If this is in error, remove %s", p, currentSandboxLinkLocation)
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
			return fmt.Errorf("sandbox directory %s already exists", sandboxDir)
		}
		if err := initClusterConfig(cmd, sandboxDir); err != nil {
			return fmt.Errorf("failed to initialize cluster config: %w", err)
		}
		profile := "velda"
		var profileCfg *clientlib.Configs
		if cfg, err := clientlib.LoadConfigs("velda"); err == nil {
			profileCfg = cfg
		} else {
			_, cfg, err := clientlib.InitCurrentConfig("localhost:50051", true)
			profileCfg = cfg
			if err != nil {
				return fmt.Errorf("Error initializing config: %w", err)
			}
			if err := cfg.RenameConfig(profile); err != nil {
				return fmt.Errorf("Error renaming config: %w", err)
			}
		}
		if err := profileCfg.MakeCurrent(); err != nil {
			return fmt.Errorf("Error making config current: %w", err)
		}
		cmd.PrintErrf("%s%sInitialized velda config%s\n", utils.ColorBold, utils.ColorGreen, utils.ColorReset)
		if err := startCluster(cmd, sandboxDir); err != nil {
			return fmt.Errorf("failed to start velda cluster: %w", err)
		}
		printClusterInstruction(cmd)
		return nil
	},
}

func init() {
	ClusterCmd.AddCommand(clusterInitCmd)
	clusterInitCmd.Flags().String("base-image", "ubuntu:24.04", "The docker image to initialize the sandbox")
	clusterInitCmd.Flags().StringSlice("backends", []string{"all"}, "The backends to enable (comma-separated list, e.g., 'aws,gce')")
}

func initClusterConfig(cmd *cobra.Command, sandboxDir string) error {
	config := &configpb.Config{
		Server: &configpb.Server{
			GrpcAddress: ":50051",
			HttpAddress: ":8081",
		},
		Storage: &configpb.Storage{
			Storage: &configpb.Storage_Mini{
				Mini: &configpb.Storage_MiniVelda{
					Root: sandboxDir,
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
	if err := os.MkdirAll(sandboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create sandbox directory: %w", err)
	}
	if err := os.WriteFile(cfgFile, cfgYaml, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
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
		"Host mini-velda",
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
		cmd.Printf("Appended SSH config for host 'mini-velda' to %s\n", cfgPath)
		return nil
	}

	lines := strings.Split(string(data), "\n")
	changed := false

	// Iterate and replace any Host mini-velda block entirely
	for i := 0; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 && strings.ToLower(fields[0]) == "host" {
			// check if this host line includes mini-velda
			matches := false
			for _, h := range fields[1:] {
				if h == "mini-velda" {
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

	// If there was no existing Host mini-velda, append it
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
		cmd.Printf("Updated SSH config for host 'mini-velda' in %s\n", cfgPath)
	}

	return nil
}

func initSshKey(cmd *cobra.Command, sandboxDir string) error {
	// Generate an ED25519 key pair

	// Generate a new Ed25519 key pair
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("failed to generate new Ed25519 key: %w", err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("failed to create SSH public key: %w", err)
	}
	sshPublicKeyBytes := ssh.MarshalAuthorizedKey(sshPublicKey)

	// Save the private key to the file
	privateKeyPem, err := ssh.MarshalPrivateKey(privateKey, "ED25519 PRIVATE KEY")
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	privateKeyBytes := pem.EncodeToMemory(privateKeyPem)
	if err := os.WriteFile(path.Join(sandboxDir, "ssh_key"), privateKeyBytes, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}
	if err := os.WriteFile(path.Join(sandboxDir, "ssh_key.pub"), sshPublicKeyBytes, 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}
	// Move key to rootfs/.velda/authorized_keys
	rootfs := path.Join(sandboxDir, "root", "0", "1", ".velda")
	c := exec.Command("sudo", "mkdir", "-p", rootfs)
	if output, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create directory %s: %w\nOutput: %s", rootfs, err, output)
	}
	c = exec.Command("sudo", "cp", path.Join(sandboxDir, "ssh_key.pub"), path.Join(rootfs, "authorized_keys"))
	if output, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to move SSH public key: %w\nOutput: %s", err, output)
	}
	return nil
}
