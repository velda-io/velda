package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfigSshAppends(t *testing.T) {
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
	if !strings.Contains(s, "Host velda-mini") {
		t.Fatalf("expected Host velda-mini in config, got:\n%s", s)
	}
	if !strings.Contains(s, "ProxyCommand") || !strings.Contains(s, "port-forward") {
		t.Fatalf("expected ProxyCommand port-forward in config, got:\n%s", s)
	}
}

func TestConfigSshReplaces(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("failed to set HOME: %v", err)
	}

	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("failed to create ssh dir: %v", err)
	}

	// create an existing config with a velda-mini block and other hosts
	initial := `# global
Host other
    HostName other.example.com

Host velda-mini
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
	if !strings.Contains(s, "Host velda-mini") {
		t.Fatalf("expected Host velda-mini in config, got:\n%s", s)
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
