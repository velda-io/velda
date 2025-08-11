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
package cases

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var veldaDebug = flag.Bool("debug", false, "Enable debug mode for Velda commands")

func runVelda(args ...string) error {
	out, err := runVeldaWithOutput(args...)
	if err != nil {
		return fmt.Errorf("command failed: %v, output: %s, error: %w", args, out, err)
	}
	return nil
}

func runVeldaWithOutput(args ...string) (string, error) {
	veldaPath := os.Getenv("VELDA")
	if veldaPath == "" {
		var err error
		veldaPath, err = exec.LookPath("velda")
		if err != nil {
			// Fall back to binary in the project
			veldaPath = filepath.Join("..", "..", "bin", "client")
			if _, err := os.Stat(veldaPath); os.IsNotExist(err) {
				return "", fmt.Errorf("velda client binary not found: %v", err)
			}
		}
	}

	if *veldaDebug {
		args = append([]string{"--debug"}, args...)
	}
	// Execute the command
	cmd := exec.Command(veldaPath, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

type Runner interface {
	Setup(t *testing.T)
	TearDown(t *testing.T)
}
