// Package testhelper provides shared helpers for tests.
package testhelper

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var veldaDebug = flag.Bool("debug", false, "Enable debug mode for Velda commands")
var streamCmdErr = flag.Bool("stream-cmd-err", false, "Stream command stderr to test output")

// RunVelda executes the velda command and returns error on failure.
func RunVelda(args ...string) error {
	out, stderr, err := RunVeldaWithOutErr(args...)
	if err != nil {
		return fmt.Errorf("command failed: %v, stdout: %s, stderr: %s, error: %w", args, out, stderr, err)
	}
	return nil
}

// RunVeldaWithOutput executes the velda command and returns stdout and error.
func RunVeldaWithOutput(args ...string) (string, error) {
	stdout, stderr, err := RunVeldaWithOutErr(args...)
	if err != nil {
		err = fmt.Errorf("command %s failed: %w, stderr: %s", args, err, stderr)
	}
	return stdout, err
}

// RunVeldaRaw builds the exec.Cmd for velda command without running it.
func RunVeldaRaw(args ...string) (*exec.Cmd, error) {
	veldaPath := os.Getenv("VELDA")
	if veldaPath == "" {
		var err error
		veldaPath, err = exec.LookPath("velda")
		if err != nil {
			// Fall back to binary in the project
			veldaPath = filepath.Join("..", "..", "bin", "client")
			if _, err := os.Stat(veldaPath); err != nil {
				return nil, fmt.Errorf("velda client binary not accessible: %w", err)
			}
		}
	}

	if *veldaDebug {
		args = append([]string{"--debug"}, args...)
	}
	cmd := exec.Command(veldaPath, args...)
	if *streamCmdErr {
		cmd.Stderr = os.Stderr
	}
	return cmd, nil
}

// RunVeldaWithOutErr executes command and returns stdout, stderr and error.
func RunVeldaWithOutErr(args ...string) (string, string, error) {
	cmd, err := RunVeldaRaw(args...)
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
