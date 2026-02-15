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

// The runner that starts a local server with a BTRFS filesystem for testing purposes.
// It creates a 1GB sparse file, sets up a loop device, formats it as BTRFS,
// and cleans up after the tests are done.
package runner

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"velda.io/velda/tests/cases"
)

type LocalBtrfsRunner struct {
	btrfsRoot   string
	mountPoint  string
	loopDevice  string
	sparseFile  string
	suiteName   string
	configDir   string
	apiServer   *exec.Cmd
	fileServer  *exec.Cmd
	veldaBin    string
}

func NewLocalBtrfsRunner() *LocalBtrfsRunner {
	return &LocalBtrfsRunner{}
}

// Setup prepares the BTRFS test environment
func (r *LocalBtrfsRunner) Setup(t *testing.T) {
	// Create a temporary directory for the sparse file
	tmpDir := t.TempDir()
	r.sparseFile = filepath.Join(tmpDir, "btrfs.img")
	r.mountPoint = filepath.Join(tmpDir, "btrfs_mount")

	// Create mount point
	err := os.MkdirAll(r.mountPoint, 0755)
	require.NoError(t, err, "Failed to create mount point")

	// Create 1GB sparse file
	t.Logf("Creating 1GB sparse file at %s", r.sparseFile)
	err = exec.Command("truncate", "-s", "1G", r.sparseFile).Run()
	require.NoError(t, err, "Failed to create sparse file")

	// Setup loop device
	t.Logf("Setting up loop device")
	loopOutput, err := exec.Command("sudo", "losetup", "-f", "--show", r.sparseFile).Output()
	require.NoError(t, err, "Failed to setup loop device")
	r.loopDevice = strings.TrimSpace(string(loopOutput))
	t.Logf("Loop device created: %s", r.loopDevice)

	// Cleanup: detach loop device and remove sparse file
	t.Cleanup(func() {
		t.Logf("Cleaning up BTRFS test environment")
		
		// Unmount the filesystem
		if r.mountPoint != "" {
			_ = exec.Command("sudo", "umount", r.mountPoint).Run()
			time.Sleep(1 * time.Second)
		}

		// Detach loop device
		if r.loopDevice != "" {
			err := exec.Command("sudo", "losetup", "-d", r.loopDevice).Run()
			if err != nil {
				t.Logf("Warning: Failed to detach loop device %s: %v", r.loopDevice, err)
			} else {
				t.Logf("Loop device %s detached", r.loopDevice)
			}
		}

		// Sparse file will be automatically removed by t.TempDir() cleanup
	})

	// Format as BTRFS
	t.Logf("Formatting %s as BTRFS", r.loopDevice)
	err = exec.Command("sudo", "mkfs.btrfs", "-f", r.loopDevice).Run()
	require.NoError(t, err, "Failed to format BTRFS filesystem")

	// Mount the BTRFS filesystem
	t.Logf("Mounting BTRFS filesystem at %s", r.mountPoint)
	err = exec.Command("sudo", "mount", r.loopDevice, r.mountPoint).Run()
	require.NoError(t, err, "Failed to mount BTRFS filesystem")

	// Set permissions
	err = exec.Command("sudo", "chmod", "a+w", r.mountPoint).Run()
	require.NoError(t, err, "Failed to set permissions on mount point")

	r.btrfsRoot = r.mountPoint
	suiteName := fmt.Sprintf("tests-%d", time.Now().Unix())

	// Start API server
	bindir := os.Getenv("VELDA_BIN_DIR")
	if bindir == "" {
		bindir = "../bin"
	}

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
      use_direct_protocol: true
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
  btrfs:
    root_path: "%s"
    max_disk_size_gb: 10

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
- name: "single"
  auto_scaler:
    backend:
     command:
       start: |
          name=agent-single
          docker run --rm -d --name $name --add-host=host.docker.internal:host-gateway  -e AGENT_NAME=$name -v %s/agent.yaml:/run/velda/velda.yaml -h $name  --mount type=volume,target=/tmp/agent -v %s:/velda --privileged -q veldaio/agent:latest --pool single > /dev/null
          echo $name
       stop: |
         name=$1
         docker rm -f $name -v >/dev/null
       list: |
         docker ps -a --format '{{.Names}}' | egrep "agent-single" || true
    max_agents: 1
    min_idle_agents: 0
    max_idle_agents: 1
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
`, r.btrfsRoot, configDir, veldaBin, configDir, veldaBin, configDir, veldaBin, configDir, veldaBin)

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

	r.fileServer = exec.Command("sudo", veldaBin, "fileserver", "--addr", ":7655", "--root", r.btrfsRoot)
	r.fileServer.Stdout = os.Stdout
	r.fileServer.Stderr = os.Stderr

	if err := r.fileServer.Start(); err != nil {
		t.Fatalf("Failed to start file server: %v", err)
	}

	t.Cleanup(func() {
		if err := r.apiServer.Process.Kill(); err != nil {
			t.Errorf("Failed to kill API server: %v", err)
		}
		if err := exec.Command("sudo", "kill", fmt.Sprintf("%d", r.fileServer.Process.Pid)).Run(); err != nil {
			t.Errorf("Failed to kill file server: %v", err)
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

func (r *LocalBtrfsRunner) Supports(feature cases.Feature) bool {
	return true
}

func (r *LocalBtrfsRunner) CreateTestInstance(t *testing.T, namePrefix string, image string) string {
	instanceName := fmt.Sprintf("%s-%d", namePrefix, time.Now().Unix())
	args := []string{"instance", "create", instanceName}
	if image != "" {
		args = append(args, "--image", image)
	}
	o, e := exec.Command(r.veldaBin, args...).CombinedOutput()
	require.NoError(t, e, "Failed to create test instance: %s", o)
	return instanceName
}
