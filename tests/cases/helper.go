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
package cases

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var veldaDebug = flag.Bool("debug", false, "Enable debug mode for Velda commands")
var streamCmdErr = flag.Bool("stream-cmd-err", false, "Stream command stderr to test output")

func runVelda(args ...string) error {
	out, stderr, err := runVeldaWithOutErr(args...)
	if err != nil {
		return fmt.Errorf("command failed: %v, stdout: %s, stderr: %s, error: %w", args, out, stderr, err)
	}
	return nil
}

func runVeldaWithOutput(args ...string) (string, error) {
	stdout, stderr, err := runVeldaWithOutErr(args...)
	if err != nil {
		err = fmt.Errorf("command %s failed: %w, stderr: %s", args, err, stderr)
	}
	return stdout, err
}

func runVeldaRaw(args ...string) (*exec.Cmd, error) {
	veldaPath := os.Getenv("VELDA")
	if veldaPath == "" {
		var err error
		veldaPath, err = exec.LookPath("velda")
		if err != nil {
			// Fall back to binary in the project
			veldaPath = filepath.Join("..", "..", "bin", "client")
			if _, err := os.Stat(veldaPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("velda client binary not found: %v", err)
			}
		}
	}

	if *veldaDebug {
		args = append([]string{"--debug"}, args...)
	}
	// Execute the command
	cmd := exec.Command(veldaPath, args...)
	return cmd, nil
}

func runVeldaWithOutErr(args ...string) (string, string, error) {
	cmd, err := runVeldaRaw(args...)
	if err != nil {
		return "", "", err
	}
	var stderr bytes.Buffer
	if *streamCmdErr {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	output, err := cmd.Output()
	return string(output), stderr.String(), err
}

type Feature string

const (
	FeatureImage           Feature = "image"
	FeatureSnapshot        Feature = "snapshot"
	FeatureMultiAgent      Feature = "multi-agent"
	FeatureBatchedSchedule Feature = "batched-schedule"
	FeatureZeroMaxPool     Feature = "zero-max-pool"
)

type Runner interface {
	Setup(t *testing.T)
	CreateTestInstance(t *testing.T, namePrefix string, image string) string
	Supports(feature Feature) bool
}
