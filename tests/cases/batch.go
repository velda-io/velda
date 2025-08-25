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
