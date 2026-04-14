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
package sshconnector

import (
	"strings"
	"testing"

	configpb "velda.io/velda/pkg/proto/config"
)

func TestBuildBootstrapScriptIncludesCoreSteps(t *testing.T) {
	c := &Connector{cfg: Config{
		RemoteMTLSCertPath:    "/run/velda/root-ca.pem",
		RemoteAgentConfigPath: "/run/velda/velda.yaml",
		AgentConfigContent:    "pool: p1\n",
		AgentVersionOverride:  "v9.9.9",
		TailscaleConfig: &configpb.TailscaleConfig{
			Server:     "https://ts.example",
			PreAuthKey: "ts-key",
		},
	}}

	script := c.buildBootstrapScript()

	checks := []string{
		"dpkg -s nfs-common",
		"tailscale up --login-server='https://ts.example' --authkey='ts-key' --accept-routes",
		"curl -fsSL https://releases.velda.io/velda-v9.9.9-linux-amd64 -o /tmp/velda",
		"systemctl restart velda-agent.service",
		"cat << 'VELDA_CONFIG_EOF' > '/run/velda/velda.yaml'",
		"chmod 0644 '/run/velda/root-ca.pem'",
	}

	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("expected script to contain %q", check)
		}
	}
}

func TestBuildBootstrapScriptWithoutTailscale(t *testing.T) {
	c := &Connector{cfg: Config{
		RemoteMTLSCertPath: "/run/velda/root-ca.pem",
	}}

	script := c.buildBootstrapScript()
	if strings.Contains(script, "tailscale up") {
		t.Fatalf("did not expect tailscale setup without tailscale config")
	}
}

func TestShellSingleQuote(t *testing.T) {
	in := "abc'def"
	out := shellSingleQuote(in)
	if out != "'abc'\\''def'" {
		t.Fatalf("unexpected quoted string: %s", out)
	}
}

func TestUniqueLogIDUsesHostname(t *testing.T) {
	id1 := uniqueLogID("worker-1")
	id2 := uniqueLogID("worker-1")
	if id1 == id2 {
		t.Fatalf("expected unique log IDs")
	}
	if !strings.HasPrefix(id1, "worker-1-") {
		t.Fatalf("expected hostname prefix in log ID, got %s", id1)
	}
}
