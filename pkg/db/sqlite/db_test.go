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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"velda.io/velda/pkg/proto"
)

func TestSqliteDatabase(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:" // Use in-memory database for testing
	db, err := NewSqliteDatabase(dbPath)
	assert.NoError(t, err, "Failed to create database")
	defer db.Close()

	ctx := context.Background()

	// Test CreateTask
	t.Run("CreateTask", func(t *testing.T) {
		workload := &proto.Workload{
			Command: "echo hello",
		}

		session := &proto.SessionRequest{
			TaskId:     "test-job/task1",
			Pool:       "default",
			InstanceId: 123,
			Priority:   10,
			Workload:   workload,
			Labels:     []string{"env=test", "type=unit"},
		}

		taskId, upstreamCount, err := db.CreateTask(ctx, session)
		assert.NoError(t, err, "CreateTask failed")
		assert.Equal(t, "test-job/task1", taskId, "Unexpected task ID")
		assert.Equal(t, 0, upstreamCount, "Unexpected upstream count")
	})

	// Test GetTask
	t.Run("GetTask", func(t *testing.T) {
		task, err := db.GetTask(ctx, "test-job/task1")
		assert.NoError(t, err, "GetTask failed")
		assert.Equal(t, "test-job/task1", task.Id, "Unexpected task ID")
		assert.Equal(t, "default", task.Pool, "Unexpected pool")
		assert.Equal(t, int64(123), task.InstanceId, "Unexpected instance ID")
		assert.Equal(t, int64(10), task.Priority, "Unexpected priority")
		assert.NotNil(t, task.Workload, "Workload should not be nil")
		assert.Equal(t, "echo hello", task.Workload.Command, "Unexpected workload command")
	})

	// Test ListTasks
	t.Run("ListTasks", func(t *testing.T) {
		// Create another task
		session := &proto.SessionRequest{
			TaskId:     "test-job/task2",
			Pool:       "default",
			InstanceId: 124,
			Priority:   5,
			Workload:   &proto.Workload{Command: "echo world"},
		}
		db.CreateTask(ctx, session)

		request := &proto.ListTasksRequest{
			ParentId: "test-job/",
			PageSize: 10,
		}

		tasks, nextCursor, err := db.ListTasks(ctx, request)
		assert.NoError(t, err, "ListTasks failed")
		assert.Len(t, tasks, 2, "Unexpected number of tasks")
		assert.Empty(t, nextCursor, "Unexpected next cursor")
	})

	// Test SearchTasks
	t.Run("SearchTasks", func(t *testing.T) {
		request := &proto.SearchTasksRequest{
			LabelFilters: []string{"env=test"},
			PageSize:     10,
		}

		tasks, nextCursor, err := db.SearchTasks(ctx, request)
		assert.NoError(t, err, "SearchTasks failed")
		assert.NotEmpty(t, tasks, "Expected at least one task")
		assert.Empty(t, nextCursor, "Unexpected next cursor")
	})

	// Test UpdateTaskFinalResult
	t.Run("UpdateTaskFinalResult", func(t *testing.T) {
		exitCode := int32(0)
		result := &BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: &exitCode,
			},
			StartTime: time.Now().Add(-time.Minute),
		}

		err := db.UpdateTaskFinalResult(ctx, "test-job/task1", result)
		assert.NoError(t, err, "UpdateTaskFinalResult failed")

		// Verify the task was updated
		task, err := db.GetTask(ctx, "test-job/task1")
		assert.NoError(t, err, "GetTask after update failed")
		assert.Equal(t, proto.TaskStatus_TASK_STATUS_SUCCESS, task.Status, "Unexpected task status")
		assert.NotNil(t, task.StartedAt, "Expected StartedAt to be set")
		assert.NotNil(t, task.FinishedAt, "Expected FinishedAt to be set")
	})

	// Test leaser operations
	t.Run("LeaserOperations", func(t *testing.T) {
		now := time.Now()
		err := db.RenewLeaser(ctx, "test-leaser", now)
		assert.NoError(t, err, "RenewLeaser failed")

		// Test release expired leasers
		pastTime := now.Add(-2 * time.Minute)
		err = db.ReleaseExpiredLeaser(ctx, pastTime)
		assert.NoError(t, err, "ReleaseExpiredLeaser failed")
	})
}
