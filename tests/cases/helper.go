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
