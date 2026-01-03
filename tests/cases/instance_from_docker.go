package cases

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testInstanceFromDocker creates an instance from ubuntu:24.04 and verifies basic run
func testInstanceFromDocker(t *testing.T, r Runner) {
	// Skip if runner doesn't support images (some runners create instances differently)
	if !r.Supports(FeatureImage) {
		t.Skip("Runner does not support image-based instance creation")
	}

	// Unique instance name
	instanceName := fmt.Sprintf("test-docker-ubuntu-%d", time.Now().UnixNano())

	// Create instance from docker image
	require.NoError(t, runVelda("instance", "create", "-d", "ubuntu:24.04", instanceName))

	// Run a simple command to validate instance is reachable
	require.NoError(t, runVelda("run", "--instance", instanceName, "echo", "ok-from-ubuntu"))

	// Make it an image named "ubuntu"
	require.NoError(t, runVelda("image", "create", "--instance", instanceName, "ubuntu"))

	// Install Python3
	require.NoError(t, runVelda("run", "--instance", instanceName, "sudo", "apt-get", "install", "-y", "python3"))

	// Verify Python3 installation
	// Add a session name to workaround a race condition with deallocating session.
	output, err := runVeldaWithOutput("run", "--instance", instanceName, "-s", "test1", "python3", "--version")
	require.NoError(t, err, "Failed to get Python3 version: %s", output)
	require.Contains(t, string(output), "Python 3")

	// Make a new image named "ubuntu-python"
	require.NoError(t, runVelda("image", "create", "--instance", instanceName, "ubuntu-python"))

	// Cleanup
	require.NoError(t, runVelda("instance", "delete", instanceName))
}
