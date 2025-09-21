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
	database.db.SetMaxOpenConns(1) // memory db only supports one connection
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

	taskPolled := make(chan bool)
	callback := func(leaserIdentity string, task *db.TaskWithUser) error {
		mu.Lock()
		defer mu.Unlock()
		polledTasks = append(polledTasks, task)
		assert.Equal(t, "test-leaser", leaserIdentity)
		assert.Equal(t, "test-job/task1", task.Task.Id)
		assert.Equal(t, proto.TaskStatus_TASK_STATUS_QUEUEING, task.Task.Status)
		taskPolled <- true
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

	<-taskPolled // Wait until a task is polled

	// Cancel polling
	pollCancel()

	// Wait for polling to finish
	<-pollDone
}

func TestPollTasksWithDependencies(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:"
	database, err := NewSqliteDatabase(dbPath)
	database.db.SetMaxOpenConns(1) // memory db only supports one connection

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
				UpstreamTaskId: "upstream",
				Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
			},
		},
	}

	_, upstreamCount, err := database.CreateTask(ctx, downstreamSession)
	assert.NoError(t, err, "Failed to create downstream task")
	assert.Equal(t, 1, upstreamCount, "Downstream task should have 1 dependency")

	wg := sync.WaitGroup{}
	wg.Add(1)
	pollCtx, pollCancel := context.WithCancel(ctx)
	completed := false
	database.PollTasks(pollCtx, "test-leaser", func(leaserIdentity string, task *db.TaskWithUser) error {
		if task.Id == "test-job/upstream" {
			// Complete upstream task
			exitCode := int32(0)
			result := &BatchTaskResult{
				Payload: &proto.BatchTaskResult{
					ExitCode: &exitCode,
				},
				StartTime: time.Now().Add(-time.Minute),
			}
			completed = true
			err = database.UpdateTaskFinalResult(ctx, "test-job/upstream", result)
			assert.NoError(t, err, "Failed to update upstream task")
		} else if task.Id == "test-job/downstream" {
			// Downstream task should only be available after upstream is completed
			assert.True(t, completed, "Downstream task polled before upstream completion")
			pollCancel()
		}
		return nil
	})
	wg.Done()
}
