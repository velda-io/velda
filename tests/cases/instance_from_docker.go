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

	// Cleanup
	require.NoError(t, runVelda("instance", "delete", instanceName))
}
