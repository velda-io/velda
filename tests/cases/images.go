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

func testImagesCommand(t *testing.T, r Runner) {
	if !r.Supports(FeatureImage) {
		t.Skip("Image operations are not supported by this runner")
	}
	// Create a unique test instance for image operations
	instanceName := r.CreateTestInstance(t, "image-test-instance", "")
	defer func() {
		// Clean up the instance after tests
		_ = runVelda("instance", "delete", instanceName)
	}()

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

	// Generate a unique image name for our tests
	imageName := fmt.Sprintf("test-image-%d", time.Now().Unix())

	// Test case 1: Create an image from an instance
	t.Run("CreateImage", func(t *testing.T) {
		// Create an image from the current state of the instance
		require.NoError(t, runVelda("image", "create", imageName, "-i", instanceName))

		// Verify the image exists in the list
		listOutput, err := runVeldaWithOutput("image", "list")
		require.NoError(t, err, "Failed to list images")
		assert.Contains(t, listOutput, imageName, "Created image should appear in the image list")
	})

	// Test case 2: Create an image from a snapshot
	t.Run("CreateImageFromSnapshot", func(t *testing.T) {
		// Create a snapshot first
		snapshotName := fmt.Sprintf("test-snap-%d", time.Now().Unix())
		require.NoError(t, runVelda("snapshot", "create", snapshotName, "-i", instanceName),
			"Failed to create snapshot")

		// Create an image from the snapshot
		snapshotImageName := fmt.Sprintf("snap-image-%d", time.Now().Unix())
		err := runVelda("image", "create", snapshotImageName,
			"-i", instanceName, "-s", snapshotName)
		require.NoError(t, err, "Failed to create image from snapshot")
	})

	// Test case 3: Clone instance from image (using the image we created)
	t.Run("CloneInstanceFromImage", func(t *testing.T) {
		// Create a new instance from the image
		clonedInstanceName := fmt.Sprintf("cloned-instance-%d", time.Now().Unix())
		require.NoError(t, runVelda("instance", "create", clonedInstanceName, "-i", imageName))

		// Clean up the cloned instance
		defer func() {
			_ = runVelda("instance", "delete", clonedInstanceName)
		}()

		// Verify the instance exists
		listOutput, err := runVeldaWithOutput("instance", "list")
		require.NoError(t, err, "Failed to list instances")
		assert.Contains(t, listOutput, clonedInstanceName,
			"Cloned instance should appear in the instance list")
	})

	// Test case 4: Delete an image
	t.Run("DeleteImage", func(t *testing.T) {
		// Delete the image with the force flag to bypass confirmation
		require.NoError(t, runVelda("image", "delete", imageName, "-f"), "Failed to delete image")

		// Verify the image is no longer in the list
		listOutput, err := runVeldaWithOutput("image", "list")
		require.NoError(t, err, "Failed to list images")

		// Should not contain the deleted image
		assert.False(t, strings.Contains(listOutput, imageName),
			"Deleted image should not appear in the image list")
	})
}
