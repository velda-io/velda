// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sqlite

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
)

func TestPollTasks(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:" // Use in-memory database for testing
	database, err := NewSqliteDatabase(dbPath)
	assert.NoError(t, err, "Failed to create database")
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a test task
	workload := &proto.Workload{
		Command: "echo test",
	}

	session := &proto.SessionRequest{
		TaskId:     "test-job/task1",
		Pool:       "test-pool",
		InstanceId: 123,
		Priority:   10,
		Workload:   workload,
		Labels:     []string{"env=test"},
	}

	taskId, upstreamCount, err := database.CreateTask(ctx, session)
	assert.NoError(t, err, "Failed to create task")
	assert.Equal(t, "test-job/task1", taskId)
	assert.Equal(t, 0, upstreamCount, "Task should have no dependencies")

	// Test polling
	var polledTasks []*db.TaskWithUser
	var mu sync.Mutex

	callback := func(leaserIdentity string, task *db.TaskWithUser) error {
		mu.Lock()
		defer mu.Unlock()
		polledTasks = append(polledTasks, task)
		assert.Equal(t, "test-leaser", leaserIdentity)
		assert.Equal(t, "test-job/task1", task.Task.Id)
		assert.Equal(t, proto.TaskStatus_TASK_STATUS_QUEUEING, task.Task.Status)
		return nil
	}

	// Start polling in a goroutine
	pollCtx, pollCancel := context.WithTimeout(ctx, 3*time.Second)
	defer pollCancel()

	pollDone := make(chan bool)
	go func() {
		defer close(pollDone)
		database.PollTasks(pollCtx, "test-leaser", callback)
	}()

	// Wait a moment for polling to start and process the task
	time.Sleep(1 * time.Second)

	// Cancel polling
	pollCancel()

	// Wait for polling to finish
	<-pollDone

	// Check results
	mu.Lock()
	assert.Len(t, polledTasks, 1, "Should have polled exactly one task")
	if len(polledTasks) > 0 {
		assert.Equal(t, "test-job/task1", polledTasks[0].Task.Id)
	}
	mu.Unlock()

	// Verify the task is now leased by checking we can't poll it again
	tasks, err := database.pollTasksOnce(ctx, "another-leaser")
	assert.NoError(t, err)
	assert.Len(t, tasks, 0, "Task should not be available for polling again")
}

func TestPollTasksWithDependencies(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:"
	database, err := NewSqliteDatabase(dbPath)
	assert.NoError(t, err, "Failed to create database")
	defer database.Close()

	ctx := context.Background()

	// Create upstream task
	upstreamSession := &proto.SessionRequest{
		TaskId:     "test-job/upstream",
		Pool:       "test-pool",
		InstanceId: 123,
		Priority:   10,
		Workload:   &proto.Workload{Command: "echo upstream"},
	}

	_, _, err = database.CreateTask(ctx, upstreamSession)
	assert.NoError(t, err, "Failed to create upstream task")

	// Create downstream task that depends on upstream
	downstreamSession := &proto.SessionRequest{
		TaskId:     "test-job/downstream",
		Pool:       "test-pool",
		InstanceId: 124,
		Priority:   5,
		Workload:   &proto.Workload{Command: "echo downstream"},
		Dependencies: []*proto.Dependency{
			{
				UpstreamTaskId: "test-job/upstream",
				Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
			},
		},
	}

	_, upstreamCount, err := database.CreateTask(ctx, downstreamSession)
	assert.NoError(t, err, "Failed to create downstream task")
	assert.Equal(t, 1, upstreamCount, "Downstream task should have 1 dependency")

	// Initially, only upstream task should be available for polling
	tasks, err := database.pollTasksOnce(ctx, "test-leaser")
	assert.NoError(t, err, "Failed to poll tasks")
	assert.Len(t, tasks, 1, "Should only have upstream task available")
	assert.Equal(t, "test-job/upstream", tasks[0].Task.Id)

	// Complete upstream task
	exitCode := int32(0)
	result := &BatchTaskResult{
		Payload: &proto.BatchTaskResult{
			ExitCode: &exitCode,
		},
		StartTime: time.Now().Add(-time.Minute),
	}

	err = database.UpdateTaskFinalResult(ctx, "test-job/upstream", result)
	assert.NoError(t, err, "Failed to update upstream task")

	// Now downstream task should be available for polling
	tasks, err = database.pollTasksOnce(ctx, "test-leaser")
	assert.NoError(t, err, "Failed to poll tasks after upstream completion")
	assert.Len(t, tasks, 1, "Should have downstream task available")
	assert.Equal(t, "test-job/downstream", tasks[0].Task.Id)
}
