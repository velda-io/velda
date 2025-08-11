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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testUbuntu(t *testing.T) {
	// Create a unique test instance for image operations
	instanceName := fmt.Sprintf("ubuntu-test-%d-%d", os.Getpid(), time.Now().Unix())
	require.NoError(t, runVelda("instance", "create", instanceName, "--image", "ubuntu"))
	defer func() {
		// Clean up the instance after tests
		_ = runVelda("instance", "delete", instanceName)
	}()

	runCommandGetOutput := func(args ...string) (string, error) {
		return runVeldaWithOutput(append([]string{"run", "--instance", instanceName}, args...)...)
	}
	runCommand := func(args ...string) error {
		return runVelda(append([]string{"run", "--instance", instanceName}, args...)...)
	}

	t.Run("CreateEchoCreateFile", func(t *testing.T) {
		// Verify the image exists in the list
		require.NoError(t, runCommand("sh", "-c", "echo foo > testfile"))
		output, err := runCommandGetOutput("cat", "testfile")
		assert.NoError(t, err, "Failed to read test file")
		assert.Equal(t, "foo\n", output, "File content should match expected output")
	})

	t.Run("VrunCreateFile", func(t *testing.T) {
		// Verify the image exists in the list
		require.NoError(t, runCommand("vrun", "sh", "-c", "echo foo > testfile2"))
		output, err := runCommandGetOutput("vrun", "cat", "testfile2")
		assert.NoError(t, err, "Failed to read test file")
		assert.Equal(t, "foo\n", output, "File content should match expected output")
	})
}
