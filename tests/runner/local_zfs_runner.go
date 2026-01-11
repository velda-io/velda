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

// The runner that start a local server with a root ZFS pool for testing purposes.
// It creates a sub-volume for the entire test suite and cleans it up after the tests are done.
// Use tests/init_zpool.sh to initialize the images used for the tests.
package runner

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"velda.io/velda/tests/cases"
)

type LocalZfsRunner struct {
	zfsRoot   string
	suiteName string
	configDir string
	apiServer *exec.Cmd
	veldaBin  string
}

func NewLocalRunner(zfsRoot string) *LocalZfsRunner {
	return &LocalZfsRunner{
		zfsRoot: zfsRoot,
	}
}

// Run executes a command in the local runner environment.
func (r *LocalZfsRunner) Setup(t *testing.T) {
	suiteName := fmt.Sprintf("%s/tests-%d", r.zfsRoot, time.Now().Unix())
	// Start API server
	bindir := os.Getenv("VELDA_BIN_DIR")
	if bindir == "" {
		bindir = "../bin"
	}

	err := exec.Command("sudo", "zfs", "create", suiteName).Run()
	if err != nil {
		t.Fatalf("Failed to create ZFS dataset: %v", err)
	}
	err = exec.Command("sudo", "chmod", "a+w", fmt.Sprintf("/%s", suiteName)).Run()
	if err != nil {
		t.Fatalf("Failed to set permissions on ZFS dataset: %v", err)
	}
	t.Cleanup(func() {
		// ZFS volume stays busy for a while, so we need to run a command to destroy it after some time.
		// This is a workaround to avoid the dataset being busy immediately after the test.
		cmd := exec.Command("sh", "-c", fmt.Sprintf("sleep 120 && sudo zfs destroy -rf %s", suiteName))
		if err := cmd.Start(); err != nil {
			t.Errorf("Failed to start detached process to destroy ZFS dataset %s: %v", suiteName, err)
		}
	})
	configDir := t.TempDir()

	agentConfig := (`
broker:
  address: "host.docker.internal:50051"
sandbox_config:
  max_time: "3600s"
  disk_source:
    nfs_mount_source: 
      mount_options: nolock,acregmax=5,acregmin=1,acdirmax=5,acdirmin=1
    cas_config:
      cas_cache_dir: /tmp/velda_cas_cache
daemon_config:
pool: shell
`)

	if err := os.WriteFile(fmt.Sprintf("%s/agent.yaml", configDir), []byte(agentConfig), 0644); err != nil {
		t.Fatalf("Failed to write agent config file: %v", err)
	}

	configFile := fmt.Sprintf("%s/config.yaml", configDir)

	veldaBinB, err := exec.Command("realpath", fmt.Sprintf("%s/velda", bindir)).Output()
	if err != nil {
		t.Fatalf("Failed to resolve full path for velda binary: %v", err)
	}
	veldaBin := string(veldaBinB[:len(veldaBinB)-1]) // Remove trailing newline

	config := fmt.Sprintf(`
server:
  grpc_address: ":50051"
  http_address: ":8081"

storage:
  zfs:
    pool: "%s"

agent_pools:
- name: "shell"
  auto_scaler:
    backend:
      command:
        start: |
          name=agent-test-${RANDOM}
          docker run -d --name $name --add-host=host.docker.internal:host-gateway  -e AGENT_NAME=$name -v %s/agent.yaml:/run/velda/velda.yaml -h $name  --mount type=volume,target=/tmp/agent --rm -v %s:/velda --privileged -q veldaio/agent:latest > /dev/null
          echo $name
        stop: |
          name=$1
          docker rm -f $name -v >/dev/null
        list: |
          docker ps -a --format '{{.Names}}' | egrep "agent-test-[0-9]+\$" | grep -v -- -1926 || true
    max_agents: 5
    min_idle_agents: 0
    max_idle_agents: 3
    idle_decay: 40s
- name: "batch"
  auto_scaler:
    backend:
      command:
        stop: |
          name=$1
          docker rm -f $name -v >/dev/null
        list: |
          docker ps -a --format '{{.Names}}' | egrep "agent-testbatch-[0-9]+\$" | grep -v -- -1926 || true
        batchStart: |
          CNT=$1
          LABEL=$2
          for i in $(seq 1 $CNT); do
            name=agent-testbatch-${RANDOM}
            docker run \
              -d \
              --name $name \
              --add-host=host.docker.internal:host-gateway \
              -e AGENT_NAME=$name \
              -v %s/agent.yaml:/run/velda/velda.yaml \
              -h $name \
              --mount type=volume,target=/tmp/agent \
              --rm \
              -v %s:/velda \
              --privileged \
              -q veldaio/agent:latest \
              --pool batch:$LABEL \
              > /dev/null
            echo $name
          done
    max_agents: 5
    min_idle_agents: 0
    max_idle_agents: 0
    mode: MODE_BATCH
- name: "zero_max"
  auto_scaler:
    backend:
      command:
        start: |
          name=agent-test-zero-${RANDOM}
          docker run  -d --name $name --add-host=host.docker.internal:host-gateway  -e AGENT_NAME=$name -v %s/agent.yaml:/run/velda/velda.yaml -h $name  --mount type=volume,target=/tmp/agent --rm -v %s:/velda --privileged -q veldaio/agent:latest > /dev/null
          echo $name
        stop: |
          name=$1
          docker rm -f $name -v >/dev/null
        list: |
          docker ps -a --format '{{.Names}}' | egrep "agent-test-zero-[0-9]+\$" || true
    max_agents: 0
    min_idle_agents: 0
    max_idle_agents: 0
    idle_decay: 40s
`, suiteName, configDir, veldaBin, configDir, veldaBin, configDir, veldaBin)

	if err := os.WriteFile(configFile, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}
	r.suiteName = suiteName
	r.configDir = configDir

	r.apiServer = exec.Command(veldaBin, "apiserver", "--config", configFile, "--foreground")
	r.apiServer.Stdout = os.Stdout
	r.apiServer.Stderr = os.Stderr

	if err := r.apiServer.Start(); err != nil {
		t.Fatalf("Failed to start API server: %v", err)
	}

	t.Cleanup(func() {
		if err := r.apiServer.Process.Kill(); err != nil {
			t.Errorf("Failed to kill API server: %v", err)
		}
		// Clean up all remaining containers
		if err := exec.Command("sh", "-c", "docker ps -a | grep agent-test | awk '{print $1}'  | xargs docker rm -f || true").Run(); err != nil {
			t.Errorf("Failed to clean up remaining containers: %v", err)
		}
	})
	go r.apiServer.Wait()

	// Wait for the API server to be ready
	for {
		// Wait until the port is open
		conn, err := net.Dial("tcp", "localhost:50051")
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
		if r.apiServer.ProcessState != nil && r.apiServer.ProcessState.Exited() {
			t.Fatalf("API server exited unexpectedly: %v", r.apiServer.ProcessState.String())
		}
	}

	// Initialize the client
	os.Setenv("VELDA", veldaBin)
	os.Setenv("VELDA_CONFIG_DIR", r.configDir+"/client_cfg")
	if err := exec.Command(veldaBin, "init", "--broker", "localhost:50051").Run(); err != nil {
		t.Fatalf("Failed to initialize Velda client: %v", err)
	}
	r.veldaBin = veldaBin
}

func (r *LocalZfsRunner) Supports(feature cases.Feature) bool {
	return true
}

func (r *LocalZfsRunner) CreateTestInstance(t *testing.T, namePrefix string, image string) string {
	instanceName := fmt.Sprintf("%s-%d", namePrefix, time.Now().Unix())
	args := []string{"instance", "create", instanceName}
	if image != "" {
		args = append(args, "--image", image)
	}
	o, e := exec.Command(r.veldaBin, args...).CombinedOutput()
	require.NoError(t, e, "Failed to create test instance: %s", o)
	return instanceName
}
