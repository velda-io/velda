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
	getTaskStatus := func(t *testing.T, taskId string) string {
		o, err := runVeldaWithOutput("task", "get", taskId, "-o", "status", "--header=false")
		require.NoError(t, err, "Failed to get task status %s", taskId)
		return strings.TrimSpace(o)
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
			require.NoError(t, err)
			return strings.Contains(output, "TASK_STATUS_SUCCESS")
		}, 30*time.Second, 1000*time.Millisecond)
		stdout, stderr, err := runVeldaWithOutErr("task", "log", taskId)
		require.NoError(t, err, "Failed to get logs with err", stderr)
		assert.Contains(t, stdout, "STDOUT")
		assert.Contains(t, stderr, "STDERR")
	})
	t.Run("LogsFollow", func(t *testing.T) {
		// Setup test scripts
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script-log-follow.sh
#!/bin/bash
for ((i=0;i<5;i++)) do
  echo \$i
  sleep 1
done
EOF
chmod +x script-log-follow.sh
`))

		taskId, err := runCommandGetOutput("vbatch", "./script-log-follow.sh")
		require.NoError(t, err)
		taskId = strings.TrimSpace(taskId)
		time.Sleep(1 * time.Second) // Wait a moment to ensure the task is started.
		// Wait until the job is finished
		output, err := runVeldaWithOutput("task", "log", "-f", taskId)
		require.NoError(t, err, "Failed to get logs with err", output)
		lines := strings.Split(strings.TrimSpace(output), "\n")
		assert.GreaterOrEqual(t, len(lines), 5, "Expect at least 5 lines of output")
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
	t.Run("Sharded", func(t *testing.T) {
		// Setup test scripts
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script.sh
#!/bin/sh
echo \${VELDA_SHARD_ID}/\${VELDA_TOTAL_SHARDS} > \$1.\${VELDA_SHARD_ID}
EOF
chmod +x script.sh
vbatch -N 2 ./script.sh testfile_sharded
`))

		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runCommandGetOutput("cat", "testfile_sharded.0")
			if err != nil {
				log.Printf("%v", err)
				return false
			}
			assert.Equal(t, "0/2\n", output)
			return true
		}, 30*time.Second, 1000*time.Millisecond)
		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runCommandGetOutput("cat", "testfile_sharded.1")
			if err != nil {
				return false
			}
			assert.Equal(t, "1/2\n", output)
			return true
		}, 30*time.Second, 1000*time.Millisecond)
	})
	t.Run("Gang", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		// Occupy one worker
		completed := false
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			require.NoError(t, runCommand("sh", "-c", "touch gang.start; sleep 1"))
			completed = true
			wg.Done()
		}()
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script.sh
#!/bin/sh
echo \${VELDA_SHARD_ID}/\${VELDA_TOTAL_SHARDS} > \$1.\${VELDA_SHARD_ID}
EOF
chmod +x script.sh
while [ ! -f gang.start ]; do sleep 0.1; done
vbatch -N 5 --gang ./script.sh testfile_gang
`))
		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runCommandGetOutput("cat", "testfile_gang.0")
			if err != nil {
				return false
			}
			assert.Equal(t, "0/5\n", output)
			assert.True(t, completed, "Gang job started before all shards are ready")
			return true
		}, 30*time.Second, 1000*time.Millisecond)
		wg.Wait()
	})
	t.Run("BatchedGang", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		if !r.Supports(FeatureBatchedSchedule) {
			t.Skip("Batched scheduling feature is not supported")
		}
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > bgang_script.sh
#!/bin/sh
echo \${VELDA_SHARD_ID}/\${VELDA_TOTAL_SHARDS} > \$1.\${VELDA_SHARD_ID}
EOF
chmod +x bgang_script.sh
vbatch -N 5 --gang -P batch ./bgang_script.sh testfile_bgang
`))
		// Wait until the job is finished
		assert.Eventually(t, func() bool {
			output, err := runCommandGetOutput("cat", "testfile_bgang.0")
			if err != nil {
				return false
			}
			assert.Equal(t, "0/5\n", output)
			return true
		}, 30*time.Second, 1000*time.Millisecond)
	})

	t.Run("Cancel", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		require.NoError(t, runCommand("bash", "-c", `
cat << EOF > cancel_script.sh
#!/bin/bash
set +x
trap "touch \$1.CANCELLED" SIGTERM
touch \$1.STARTED
tail -f /dev/null # Sleep forever
EOF
chmod +x cancel_script.sh
job_id=$(vbatch ./cancel_script.sh testfile_cancel)

while [ velda task get $job_id | grep -q TASK_STATUS_RUNNING ]; do sleep 0.1; done
while [ ! -f testfile_cancel.STARTED ]; do sleep 0.2; done
velda task cancel $job_id
while ! (velda task get $job_id | grep -q TASK_STATUS_FAIL); do sleep 0.1; done
`))
	})

	t.Run("CancelRecursive", func(t *testing.T) {
		if !r.Supports(FeatureMultiAgent) {
			t.Skip("Multi-agent feature is not supported")
		}
		jobId, err := runCommandGetOutput("bash", "-c", `
cat << EOF > cancel_script2.sh
#!/bin/bash
set +x
trap "touch \$1.CANCELLED" SIGTERM
touch \$1.STARTED_STEP2
tail -f /dev/null # Sleep forever
EOF
chmod +x cancel_script2.sh

cat << EOF > cancel_script_wrapper.sh
#!/bin/bash
set +x
touch \$1.STARTED
vbatch --name actual_task ./cancel_script2.sh \$1
vbatch --after-success actual_task --name succ true
vbatch --after-success actual_task --name fail true
EOF
chmod +x cancel_script_wrapper.sh
job_id=$(vbatch ./cancel_script_wrapper.sh testfile_cancel_rec)

while [ ! -f testfile_cancel_rec.STARTED_STEP2 ]; do sleep 0.2; done
velda task cancel $job_id
while ! (velda task get $job_id/actual_task | grep -q TASK_STATUS_FAIL); do sleep 0.1; done
echo $job_id
`)
		jobId = strings.TrimSpace(jobId)
		require.NoError(t, err, "Failed to run cancel recursive", jobId)
		assert.Equal(t, "TASK_STATUS_CANCELLED", getTaskStatus(t, jobId))
		// Started task have status FAILURE.
		assert.Equal(t, "TASK_STATUS_FAILURE", getTaskStatus(t, jobId+"/actual_task"))
		assert.Equal(t, "TASK_STATUS_CANCELLED", getTaskStatus(t, jobId+"/succ"))
		assert.Equal(t, "TASK_STATUS_CANCELLED", getTaskStatus(t, jobId+"/fail"))
	})
	t.Run("Watch", func(t *testing.T) {
		// Setup test scripts
		jobId, err := runCommandGetOutput("sh", "-c", `
cat << EOF > test_watch.sh
#!/bin/sh
vbatch --name begin sleep 3
vbatch --name body --after-success begin sleep 2
EOF
chmod +x test_watch.sh
vbatch ./test_watch.sh
`)
		require.NoError(t, err, "Failed to start job", jobId)
		jobId = strings.TrimSpace(jobId)

		time.Sleep(1 * time.Second) // Wait until the job sub-dag is created.
		// Watch the body.
		// TODO: Watch the root job, currently do not support notification for child-tasks.
		output, err := runVeldaWithOutput("task", "watch", jobId+"/body")
		require.NoError(t, err, "Failed to watch task", jobId)
		// Should contain all status.
		assert.True(t, strings.Contains(output, "TASK_STATUS_PENDING"), "Expect pending status, got %s", output)
		// Currently no queueing because it is immediately scheduled.
		//assert.True(t, strings.Contains(output, "TASK_STATUS_QUEUEING"), "Expect pending status, got %s", output)
		assert.True(t, strings.Contains(output, "TASK_STATUS_RUNNING"), "Expect running status, got %s", output)
		assert.True(t, strings.Contains(output, "TASK_STATUS_SUCCESS"), "Expect success status, got %s", output)
	})
}
