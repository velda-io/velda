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
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testUbuntu(t *testing.T, r Runner) {
	// Create a unique test instance for image operations
	instanceName := r.CreateTestInstance(t, "ubuntu-test", "ubuntu")
	defer func() {
		// Clean up the instance after tests
		_ = runVelda("instance", "delete", instanceName)
	}()

	// TODO: remove -s "" for testing. There's a race where returning a svc while being terminated.
	vrunCommandGetOutput := func(args ...string) (string, error) {
		return runVeldaWithOutput(append([]string{"run", "--instance", instanceName, "-s", ""}, args...)...)
	}
	vrunCommand := func(args ...string) error {
		return runVelda(append([]string{"run", "--instance", instanceName, "-s", ""}, args...)...)
	}

	t.Run("CreateEchoCreateFile", func(t *testing.T) {
		// Verify the image exists in the list
		require.NoError(t, vrunCommand("sh", "-c", "echo foo > testfile"))
		output, err := vrunCommandGetOutput("cat", "testfile")
		assert.NoError(t, err, "Failed to read test file")
		assert.Equal(t, "foo\n", output, "File content should match expected output")
	})

	t.Run("VrunCreateFile", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		// Verify the image exists in the list
		require.NoError(t, vrunCommand("vrun", "sh", "-c", "echo foo > testfile2"))
		output, err := vrunCommandGetOutput("vrun", "cat", "testfile2")
		assert.NoError(t, err, "Failed to read test file")
		assert.Equal(t, "foo\n", output, "File content should match expected output")
	})

	t.Run("KillSession", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		complete := make(chan struct{})
		go func() {
			start := time.Now()
			vrunCommand("vrun", "-s", "victim1", "sleep", "10")
			assert.Less(t, time.Since(start), 5*time.Second, "Session should be killed before 5 seconds")
			close(complete)
		}()
		started := false
		for {
			output, err := runVeldaWithOutput("ls", "--instance", instanceName)
			require.NoError(t, err, "Failed to list sessions")
			if strings.Contains(output, "RUNNING  victim1") {
				break
			}
			if strings.Contains(output, "victim1") {
				started = true
				time.Sleep(10 * time.Millisecond)
			} else if started {
				t.FailNow()
			}
		}

		require.NoError(t, runVelda("kill-session", "--instance", instanceName, "-s", "victim1"))
		<-complete
	})

	t.Run("KillSessionForce", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		complete := make(chan struct{})
		go func() {
			start := time.Now()
			log.Printf("Starting session %s", "victim2")
			vrunCommand("-s", "victim2", "bash", "-c", "trap \"\" SIGTERM; sleep 100; ls")
			assert.Less(t, time.Since(start), 5*time.Second, "Session should be killed before 5 seconds")
			close(complete)
		}()
		started := false
		for {
			output, err := runVeldaWithOutput("ls", "--instance", instanceName)
			require.NoError(t, err, "Failed to list sessions")
			if strings.Contains(output, "RUNNING  victim2") {
				break
			}
			if strings.Contains(output, "victim2") {
				started = true
				time.Sleep(10 * time.Millisecond)
			} else if started {
				t.FailNow()
			}
		}

		require.NoError(t, runVelda("kill-session", "--instance", instanceName, "-s", "victim2", "--force"))
		<-complete
	})

	// The test is to repro an issue where cloning disk would erase the transient entry in exportfs.
	// Note it currently takes over 30s to run due to NFS attribute caching.
	t.Run("CloneWhileRunning", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		if !*runSlowTests {
			t.Skip("Skipping slow test")
		}
		wg := sync.WaitGroup{}
		wg.Add(1)
		// Verify the image exists in the list
		go func() {
			require.NoError(t, vrunCommand("sh", "-c", "touch start; while ! [ -f testfile3 ]; do sleep 0.1; done"))
			wg.Done()
		}()
		require.NoError(t, vrunCommand("sh", "-c", "while [ -f start ]; do sleep 0.1; done"))
		require.NoError(t, runVelda("instance", "create", "-f", instanceName, instanceName+"-clone"))
		assert.NoError(t, vrunCommand("touch", "testfile3"), "Failed to read test file")
		wg.Wait()
	})

	t.Run("VrunSnapshotModeAutoCreate", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		if !r.Supports(FeatureSnapshot) {
			t.Skip("Snapshot is required to use writable overlay directories")
		}
		// Test vrun with snapshot mode (auto-created snapshot) and writable-dir
		// This tests that non-writable directories are not persisted

		// Create initial data
		require.NoError(t, vrunCommand("sh", "-c", `
mkdir -p vrun-test
echo "original-data" > vrun-test/file.txt
`))

		// Run vrun with writable-dir (only /tmp is writable)
		// This should auto-create a snapshot and use overlay filesystem
		output, err := vrunCommandGetOutput("vrun", "--writable-dir", "/tmp", "sh", "-c", `
# Modify non-writable home directory (should not persist)
echo "modified-data" > vrun-test/file.txt
# Write to writable directory (should persist)
echo "vrun-writable" > /tmp/vrun-test.txt
# Verify write succeeded in session
cat vrun-test/file.txt
`)
		require.NoError(t, err, "vrun command should succeed")
		assert.Equal(t, "modified-data\n", output, "Modification should be visible within the session")

		// Verify the modification to non-writable directory is NOT persisted
		output, err = vrunCommandGetOutput("cat", "vrun-test/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "original-data\n", output, "Non-writable directory should not persist changes")

		// Verify writes to /tmp persisted
		output, err = vrunCommandGetOutput("cat", "/tmp/vrun-test.txt")
		require.NoError(t, err)
		assert.Equal(t, "vrun-writable\n", output, "Writable directory should persist changes")
	})

	t.Run("VrunSnapshotModeWithExistingSnapshot", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		if !r.Supports(FeatureSnapshot) {
			t.Skip("Snapshot is required to use writable overlay directories")
		}
		// Test vrun with an existing snapshot and writable-dir

		snapshotName := "vrun-test-snapshot"

		// Create initial data and a snapshot
		require.NoError(t, vrunCommand("sh", "-c", `
mkdir -p vrun-snapshot-test
echo "snapshot-data" > vrun-snapshot-test/file.txt
`))

		// Create snapshot
		_, err := runVeldaWithOutput("snapshot", "create", snapshotName, "-i", instanceName)
		require.NoError(t, err, "Failed to create snapshot")
		t.Cleanup(func() {
			// Clean up snapshot
			_ = runVelda("snapshot", "delete", snapshotName, "-i", instanceName)
		})

		// Modify the data after snapshot creation
		require.NoError(t, vrunCommand("sh", "-c", `
echo "modified-after-snapshot" > vrun-snapshot-test/file.txt
`))

		// Run vrun with the existing snapshot (should see original snapshot data)
		output, err := vrunCommandGetOutput("vrun", "--snapshot", snapshotName, "--writable-dir", "/tmp", "sh", "-c", `
cat vrun-snapshot-test/file.txt
`)
		require.NoError(t, err, "vrun command should succeed")
		assert.Equal(t, "snapshot-data\n", output, "Should see data from snapshot, not current state")

		// Run vrun with the existing snapshot, but mount the modified data as well (should see modified data)
		output, err = vrunCommandGetOutput("vrun", "--snapshot", snapshotName, "--writable-dir", "/home", "sh", "-c", `
cat vrun-snapshot-test/file.txt
`)
		require.NoError(t, err, "vrun command should succeed")
		assert.Equal(t, "modified-after-snapshot\n", output, "Should see modified data when mounted as writable")

		// Verify current state is unchanged (still has the modified data)
		output, err = vrunCommandGetOutput("cat", "vrun-snapshot-test/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "modified-after-snapshot\n", output, "Current state should be unaffected")
	})
}
