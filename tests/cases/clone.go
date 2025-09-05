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
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testInstanceClone(t *testing.T, r Runner) {
	if !r.Supports(FeatureSnapshot) {
		t.Skip("Image operations are not supported by this runner")
	}
	// Create a unique test instance for image operations
	instanceName := r.CreateTestInstance(t, "snapshot-test-instance", "")
	// Add a test file to the instance
	testContent := "This is a test file for SCP command testing"
	tmpFile, err := os.CreateTemp("", "velda-scp-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(testContent); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	require.NoError(t, runVelda("scp", "-u", "root", tmpFile.Name(), fmt.Sprintf("%s:/scp-test-file.txt", instanceName)))

	// Generate a unique snapshot name for our tests
	snapshotName := fmt.Sprintf("test-snap-%d", time.Now().Unix())

	// Test case 1: Create a snapshot from an instance
	t.Run("CreateSnapshot", func(t *testing.T) {
		// Create a snapshot from the current state of the instance
		require.NoError(t, runVelda("snapshot", "create", snapshotName, "-i", instanceName))
	})

	clonedInstanceName := fmt.Sprintf("cloned-instance-%d", time.Now().Unix())
	// Test case 2: Clone instance from snapshot (using the snapshot we created)
	t.Run("CloneInstanceFromSnapshot", func(t *testing.T) {
		// Create a new instance from the snapshot
		require.NoError(t, runVelda("instance", "create", clonedInstanceName, "-f", instanceName, "--snapshot", snapshotName))
		require.NoError(t, runVelda("instance", "create", clonedInstanceName+"-1", "-f", instanceName, "--snapshot", snapshotName))

		// Verify the instance exists
		listOutput, err := runVeldaWithOutput("instance", "list")
		require.NoError(t, err, "Failed to list instances")
		assert.Contains(t, listOutput, clonedInstanceName,
			"Cloned instance should appear in the instance list")
	})

	// Test case 3: Delete an instance
	t.Run("DeleteInstances", func(t *testing.T) {
		require.NoError(t, runVelda("instance", "delete", clonedInstanceName+"-1"), "Failed to delete cloned instance")
		require.NoError(t, runVelda("instance", "delete", instanceName), "Failed to delete original instance")
		require.NoError(t, runVelda("instance", "delete", clonedInstanceName), "Failed to delete cloned instance")

		// Verify the instance is no longer in the list
		listOutput, err := runVeldaWithOutput("instance", "list")
		require.NoError(t, err, "Failed to list instances")

		// Should not contain the deleted instance
		assert.False(t, strings.Contains(listOutput, instanceName),
			"Deleted instance should not appear in the instance list")
		assert.False(t, strings.Contains(listOutput, clonedInstanceName),
			"Deleted instance should not appear in the instance list")
	})
}
