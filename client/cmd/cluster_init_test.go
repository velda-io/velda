// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// SSH key related features are being re-worked.
func TestConfigSshAppends(t *testing.T) {
	t.Skip("Skipping ssh config tests temporarily")
	tmp := t.TempDir()
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("failed to set HOME: %v", err)
	}

	cmd := &cobra.Command{}
	if err := configSsh(cmd); err != nil {
		t.Fatalf("configSsh failed: %v", err)
	}

	cfgPath := filepath.Join(tmp, ".ssh", "config")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read ssh config: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "Host mini-velda") {
		t.Fatalf("expected Host mini-velda in config, got:\n%s", s)
	}
	if !strings.Contains(s, "ProxyCommand") || !strings.Contains(s, "port-forward") {
		t.Fatalf("expected ProxyCommand port-forward in config, got:\n%s", s)
	}
}

func TestConfigSshReplaces(t *testing.T) {
	t.Skip("Skipping ssh config tests temporarily")
	tmp := t.TempDir()
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("failed to set HOME: %v", err)
	}

	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("failed to create ssh dir: %v", err)
	}

	// create an existing config with a mini-velda block and other hosts
	initial := `# global
Host other
    HostName other.example.com

Host mini-velda
    User old
    ProxyCommand old-proxy

Host later
    HostName later.example.com
`
	cfgPath := filepath.Join(sshDir, "config")
	if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	cmd := &cobra.Command{}
	if err := configSsh(cmd); err != nil {
		t.Fatalf("configSsh failed: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read ssh config: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "Host mini-velda") {
		t.Fatalf("expected Host mini-velda in config, got:\n%s", s)
	}
	if strings.Contains(s, "User old") {
		t.Fatalf("expected old User to be replaced, but it's still present\n%s", s)
	}
	if !strings.Contains(s, "User user") {
		t.Fatalf("expected User user in replaced block, got:\n%s", s)
	}
	if !strings.Contains(s, "ProxyCommand") || !strings.Contains(s, "port-forward") {
		t.Fatalf("expected ProxyCommand port-forward in config, got:\n%s", s)
	}
}
