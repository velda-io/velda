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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testBatch(t *testing.T, r Runner) {
	// Create a unique test instance for image operations
	instanceName := r.CreateTestInstance(t, "ubuntu-batch", "ubuntu")
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

	t.Run("Simple", func(t *testing.T) {
		// Setup test scripts
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script.sh
#!/bin/sh
echo COMPLETED > \$1
EOF
chmod +x script.sh
vbatch ./script.sh testfile
`))

		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runCommandGetOutput("cat", "testfile")
			if err != nil {
				return false
			}
			assert.Equal(t, "COMPLETED\n", output)
			return true
		}, 30*time.Second, 1000*time.Millisecond)
	})
	t.Run("Logs", func(t *testing.T) {
		// Setup test scripts
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script-log.sh
#!/bin/sh
echo STDOUT
echo STDERR >&2
EOF
chmod +x script-log.sh
`))

		taskId, err := runCommandGetOutput("vbatch", "./script-log.sh")
		taskId = strings.TrimSpace(taskId)
		require.NoError(t, err)
		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runVeldaWithOutput("task", "get", taskId, "-o", "status", "--header=false")
			t.Logf("Task status: %s", output)
			require.NoError(t, err)
			return strings.Contains(output, "TASK_STATUS_SUCCESS")
		}, 30*time.Second, 1000*time.Millisecond)
		stdout, stderr, err := runVeldaWithOutErr("task", "log", taskId)
		require.NoError(t, err)
		assert.Contains(t, stdout, "STDOUT")
		assert.Contains(t, stderr, "STDERR")
	})
	t.Run("Recursive", func(t *testing.T) {
		// Setup test scripts
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script.sh
#!/bin/sh
echo COMPLETED > \$1
EOF
chmod +x script.sh
cat << EOF > script_rec.sh
#!/bin/sh
vbatch --name actual_task ./script.sh \$1
EOF
chmod +x script_rec.sh
vbatch ./script_rec.sh testfile_rec
`))

		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runCommandGetOutput("cat", "testfile_rec")
			if err != nil {
				return false
			}
			assert.Equal(t, "COMPLETED\n", output)
			return true
		}, 30*time.Second, 1000*time.Millisecond)
	})
}
