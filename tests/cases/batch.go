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
	runBatchJob := func(args ...string) (string, error) {
		return runVeldaWithOutput(append([]string{"run", "--batch", "--instance", instanceName, "-s", ""}, args...)...)
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

		taskId, err := runBatchJob("./script-log.sh")
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

		taskId, err := runBatchJob("./script-log-follow.sh")
		require.NoError(t, err)
		taskId = strings.TrimSpace(taskId)
		// Wait until the job is finished
		output, err := runVeldaWithOutput("task", "log", "-f", taskId)
		require.NoError(t, err, "Failed to get logs with err", output)
		lines := strings.Split(strings.TrimSpace(output), "\n")
		assert.GreaterOrEqual(t, len(lines), 5, "Expect at least 5 lines of output")
	})

	t.Run("RunWithFollow", func(t *testing.T) {
		// Setup test scripts
		require.NoError(t, runCommand("sh", "-c", `
cat << EOF > script-log-follow.sh
#!/bin/bash
for ((i=0;i<5;i++)) do
  echo \$i
  sleep 0.5
done
EOF
chmod +x script-log-follow.sh
`))

		output, err := runBatchJob("-f", "./script-log-follow.sh")
		require.NoError(t, err, "Failed to get logs with err", output)
		lines := strings.Split(strings.TrimSpace(output), "\n")
		assert.GreaterOrEqual(t, len(lines), 5, "Expect at least 5 lines of output")
	})

	t.Run("WatchRecursive", func(t *testing.T) {
		// Setup test scripts
		jobId, err := runCommandGetOutput("sh", "-c", `
cat << EOF > script.sh
#!/bin/sh
sleep 0.5
echo COMPLETED > \$1
EOF
chmod +x script.sh
cat << EOF > script_rec.sh
#!/bin/sh
sleep 0.5
vbatch --name actual_task ./script.sh \$1
EOF
chmod +x script_rec.sh
vbatch ./script_rec.sh testfile_rec
`)
		require.NoError(t, err, "Failed to start job", jobId)
		jobId = strings.TrimSpace(jobId)

		output, err := runVeldaWithOutput("task", "watch", jobId)
		assert.NoError(t, err, "Failed to watch task %s", jobId)
		// Should contain all status.
		assert.True(t, strings.Contains(output, "TASK_STATUS_RUNNING"), "Expect running status, got %s", output)
		assert.True(t, strings.Contains(output, "TASK_STATUS_RUNNING_SUBTASKS"), "Expect running_subtasks status, got %s", output)
		assert.True(t, strings.Contains(output, "TASK_STATUS_SUCCESS"), "Expect success status, got %s", output)

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
			// Occupy one worker with a long running job to delay the gang job from starting
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
		assert.NoError(t, err, "Failed to watch task %s", jobId)
		// Should contain all status.
		assert.True(t, strings.Contains(output, "TASK_STATUS_PENDING"), "Expect pending status, got %s", output)
		// Currently no queueing because it is immediately scheduled.
		//assert.True(t, strings.Contains(output, "TASK_STATUS_QUEUEING"), "Expect pending status, got %s", output)
		assert.True(t, strings.Contains(output, "TASK_STATUS_RUNNING"), "Expect running status, got %s", output)
		assert.True(t, strings.Contains(output, "TASK_STATUS_SUCCESS"), "Expect success status, got %s", output)
	})

	t.Run("CancelBeforeScheduling", func(t *testing.T) {
		if !r.Supports(FeatureZeroMaxPool) {
			t.Skip("Zero max pool feature is not supported")
		}

		// Submit a batch job to a pool with zero capacity (zero_max pool)
		// This ensures the task won't get scheduled immediately
		jobId, err := runCommandGetOutput("bash", "-c", `
cat << EOF > cancel_before_sched.sh
#!/bin/bash
echo "This should never run"
EOF
chmod +x cancel_before_sched.sh
vbatch -P zero_max ./cancel_before_sched.sh
`)
		require.NoError(t, err, "Failed to start job", jobId)
		jobId = strings.TrimSpace(jobId)

		// Verify the task is in QUEUEING status (not scheduled yet)
		time.Sleep(500 * time.Millisecond) // Give it a moment to be registered
		status := getTaskStatus(t, jobId)
		assert.Equal(t, "TASK_STATUS_QUEUEING", status, "Task should be queueing, got %s", status)

		// Cancel the job before it gets scheduled
		_, err = runVeldaWithOutput("task", "cancel", jobId)
		require.NoError(t, err, "Failed to cancel task %s", jobId)

		// Verify the task is now cancelled
		assert.Eventually(t, func() bool {
			status := getTaskStatus(t, jobId)
			return status == "TASK_STATUS_CANCELLED"
		}, 10*time.Second, 500*time.Millisecond, "Task should be cancelled")

		// Double check final status
		finalStatus := getTaskStatus(t, jobId)
		assert.Equal(t, "TASK_STATUS_CANCELLED", finalStatus)
	})

	t.Run("OverlayNonWritableNotPersisted", func(t *testing.T) {
		if !r.Supports(FeatureSnapshot) {
			t.Skip("Snapshot is required to use writable overlay directories")
		}
		// Test that writes to non-writable directories are not persisted
		// and not visible in other sessions

		// Create a snapshot first
		require.NoError(t, runCommand("sh", "-c", `
# Create a test file in a non-writable location
mkdir -p /opt/testdata
echo "original" > /opt/testdata/file.txt
`))

		// Run a job with snapshot and writable dir (only /tmp is writable)
		jobId, err := runBatchJob("--writable-dir", "/tmp",
			"sh", "-c", `
# Try to modify the non-writable directory
cat /proc/self/mountinfo > /opt/testdata/file.txt
# Write to writable directory
echo "writable-data" > /tmp/writable.txt
`)
		require.NoError(t, err, "Failed to start job")
		jobId = strings.TrimSpace(jobId)

		// Wait for job completion
		assert.Eventually(t, func() bool {
			status := getTaskStatus(t, jobId)
			return status == "TASK_STATUS_SUCCESS" || status == "TASK_STATUS_FAILURE"
		}, 30*time.Second, 500*time.Millisecond)

		// Verify the modification to /opt/testdata is NOT persisted
		output, err := runCommandGetOutput("cat", "/opt/testdata/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "original\n", output, "Non-writable directory should not persist changes")

		// Verify writes to /tmp are visible (since it's writable)
		output, err = runCommandGetOutput("cat", "/tmp/writable.txt")
		require.NoError(t, err)
		assert.Equal(t, "writable-data\n", output, "Writable directory should persist changes")
	})

	t.Run("OverlayWritableDirShared", func(t *testing.T) {
		if !r.Supports(FeatureSnapshot) {
			t.Skip("Snapshot is required to use writable overlay directories")
		}
		// Test that writes to writable directories are visible to other jobs

		// Create a snapshot
		snapshotName := "overlay-shared-snapshot"
		require.NoError(t, runCommand("sh", "-c", `
# Create initial data
mkdir -p /var/shared
echo "initial" > /var/shared/data.txt
`))

		// Create snapshot
		_, err := runVeldaWithOutput("instance", "snapshot", instanceName, snapshotName)
		require.NoError(t, err, "Failed to create snapshot")

		// Job 1: Write to writable directory
		job1Id, err := runBatchJob("--writable-dir", "/var/shared", "sh", "-c", `
echo "job1-data" > /var/shared/job1.txt
sleep 2
`)
		require.NoError(t, err, "Failed to start job 1")
		job1Id = strings.TrimSpace(job1Id)

		// Wait a moment for job1 to write
		time.Sleep(1 * time.Second)

		// Job 2: Read from writable directory
		job2Id, err := runBatchJob("--writable-dir", "/var/shared", "sh", "-c", `
# Wait for job1 to complete
sleep 2
# Check if we can see job1's write
if [ -f /var/shared/job1.txt ]; then
  cat /var/shared/job1.txt > /var/shared/job2-saw-job1.txt
  echo "success" > /var/shared/job2-result.txt
else
  echo "failure" > /var/shared/job2-result.txt
fi
`)
		require.NoError(t, err, "Failed to start job 2")
		job2Id = strings.TrimSpace(job2Id)

		// Wait for both jobs to complete
		assert.Eventually(t, func() bool {
			status1 := getTaskStatus(t, job1Id)
			status2 := getTaskStatus(t, job2Id)
			return (status1 == "TASK_STATUS_SUCCESS" || status1 == "TASK_STATUS_FAILURE") &&
				(status2 == "TASK_STATUS_SUCCESS" || status2 == "TASK_STATUS_FAILURE")
		}, 30*time.Second, 500*time.Millisecond)

		// Verify job2 saw job1's write
		output, err := runCommandGetOutput("cat", "/var/shared/job2-result.txt")
		require.NoError(t, err)
		assert.Equal(t, "success\n", output, "Job 2 should see job 1's writes to writable directory")

		output, err = runCommandGetOutput("cat", "/var/shared/job2-saw-job1.txt")
		require.NoError(t, err)
		assert.Equal(t, "job1-data\n", output, "Job 2 should be able to read job 1's data")

		// Clean up snapshot
		_ = runVelda("instance", "snapshot", "delete", instanceName, snapshotName)
	})
}
