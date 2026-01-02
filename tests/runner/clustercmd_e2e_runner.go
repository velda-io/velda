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

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"velda.io/velda/tests/cases"
)

type ClusterCmdE2ERunner struct {
	veldaBin string
}

func NewClusterCmdE2ERunner() *ClusterCmdE2ERunner {
	return &ClusterCmdE2ERunner{}
}

// Run executes a command in the local runner environment.
func (r *ClusterCmdE2ERunner) Setup(t *testing.T) {
	// Start API server
	bindir := os.Getenv("VELDA_BIN_DIR")
	if bindir == "" {
		bindir = "../bin"
	}

	veldaBinB, err := exec.Command("realpath", fmt.Sprintf("%s/velda", bindir)).Output()
	if err != nil {
		t.Fatalf("Failed to resolve full path for velda binary: %v", err)
	}
	veldaBin := string(veldaBinB[:len(veldaBinB)-1]) // Remove trailing newline

	rootDir, err := os.MkdirTemp("", t.Name())
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	dataDir := rootDir + "/data"
	configDir := rootDir + "/config"

	// Initialize the client
	os.Setenv("VELDA", veldaBin)
	os.Setenv("VELDA_CONFIG_DIR", configDir)

	cmd := exec.Command("sudo", "-E", veldaBin, "cluster", "init", dataDir, "--backends=")
	cmd.Stderr = os.Stderr
	if output, err := cmd.Output(); err != nil {
		t.Fatalf("Failed to initialize cluster client: %v, output: %s", err, output)
	}
	t.Cleanup(func() {
		cmd := exec.Command("sudo", "-E", veldaBin, "cluster", "down", dataDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Failed to clean up cluster client: %v, output: %s", err, output)
		}
		cmd = exec.Command("sudo", "rm", "-rf", rootDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Failed to clean up root directory: %v, output: %s", err, output)
		}
	})

	r.veldaBin = veldaBin
}

func (r *ClusterCmdE2ERunner) Supports(feature cases.Feature) bool {
	switch feature {
	case cases.FeatureImage:
		return true
	case cases.FeatureSnapshot:
		return false
	case cases.FeatureMultiAgent:
		return false
	case cases.FeatureZeroMaxPool:
		return false
	}
	return false
}

func (r *ClusterCmdE2ERunner) CreateTestInstance(t *testing.T, namePrefix string, image string) string {
	instanceName := fmt.Sprintf("%s-%d", namePrefix, time.Now().Unix())
	args := []string{"instance", "create", instanceName}
	if image != "" {
		args = append(args, "--image", image)
	}
	o, e := exec.Command(r.veldaBin, args...).CombinedOutput()
	require.NoError(t, e, "Failed to create test instance: %s", o)
	return instanceName
}
