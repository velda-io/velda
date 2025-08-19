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
	runCommandGetOutput := func(args ...string) (string, error) {
		return runVeldaWithOutput(append([]string{"run", "--instance", instanceName, "-s", ""}, args...)...)
	}
	runCommand := func(args ...string) error {
		return runVelda(append([]string{"run", "--instance", instanceName, "-s", ""}, args...)...)
	}

	t.Run("CreateEchoCreateFile", func(t *testing.T) {
		// Verify the image exists in the list
		require.NoError(t, runCommand("sh", "-c", "echo foo > testfile"))
		output, err := runCommandGetOutput("cat", "testfile")
		assert.NoError(t, err, "Failed to read test file")
		assert.Equal(t, "foo\n", output, "File content should match expected output")
	})

	t.Run("VrunCreateFile", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		// Verify the image exists in the list
		require.NoError(t, runCommand("vrun", "sh", "-c", "echo foo > testfile2"))
		output, err := runCommandGetOutput("vrun", "cat", "testfile2")
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
			runCommand("vrun", "-s", "victim", "sleep", "10")
			assert.Less(t, time.Since(start), 5*time.Second, "Session should be killed before 5 seconds")
			close(complete)
		}()
		started := false
		for {
			output, err := runVeldaWithOutput("ls", "--instance", instanceName)
			require.NoError(t, err, "Failed to list sessions")
			if strings.Contains(output, "RUNNING  victim") {
				break
			}
			if strings.Contains(output, "victim") {
				started = true
				time.Sleep(10 * time.Millisecond)
			} else if started {
				t.FailNow()
			}
		}

		require.NoError(t, runVelda("kill-session", "--instance", instanceName, "-s", "victim"))
		<-complete
	})

	t.Run("KillSessionForce", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		complete := make(chan struct{})
		go func() {
			start := time.Now()
			log.Printf("Starting session %s", "victim")
			runCommand("-s", "victim", "bash", "-c", "trap \"\" SIGTERM; sleep 100; ls")
			assert.Less(t, time.Since(start), 5*time.Second, "Session should be killed before 5 seconds")
			close(complete)
		}()
		started := false
		for {
			output, err := runVeldaWithOutput("ls", "--instance", instanceName)
			require.NoError(t, err, "Failed to list sessions")
			if strings.Contains(output, "RUNNING  victim") {
				break
			}
			if strings.Contains(output, "victim") {
				started = true
				time.Sleep(10 * time.Millisecond)
			} else if started {
				t.FailNow()
			}
		}

		require.NoError(t, runVelda("kill-session", "--instance", instanceName, "-s", "victim", "--force"))
		<-complete
	})
}
