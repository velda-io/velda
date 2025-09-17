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

func TestRecursiveTaskStatusUpdate(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:" // Use in-memory database for testing
	db, err := NewSqliteDatabase(dbPath)
	assert.NoError(t, err, "Failed to create database")
	defer db.Close()

	ctx := context.Background()

	// Create upstream task (task1)
	upstreamWorkload := &proto.Workload{
		Command: "echo upstream",
	}

	upstreamSession := &proto.SessionRequest{
		TaskId:     "test-job/task1",
		Pool:       "default",
		InstanceId: 123,
		Priority:   10,
		Workload:   upstreamWorkload,
		Labels:     []string{"env=test", "type=upstream"},
	}

	upstreamTaskId, upstreamCount, err := db.CreateTask(ctx, upstreamSession)
	assert.NoError(t, err, "Failed to create upstream task")
	assert.Equal(t, "test-job/task1", upstreamTaskId)
	assert.Equal(t, 0, upstreamCount, "Upstream task should have no dependencies")

	// Create downstream task (task2) that depends on task1
	downstreamWorkload := &proto.Workload{
		Command: "echo downstream",
	}

	downstreamSession := &proto.SessionRequest{
		TaskId:     "test-job/task2",
		Pool:       "default",
		InstanceId: 124,
		Priority:   5,
		Workload:   downstreamWorkload,
		Dependencies: []*proto.Dependency{
			{
				UpstreamTaskId: "task1",
				Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
			},
		},
		Labels: []string{"env=test", "type=downstream"},
	}

	downstreamTaskId, downstreamCount, err := db.CreateTask(ctx, downstreamSession)
	assert.NoError(t, err, "Failed to create downstream task")
	assert.Equal(t, "test-job/task2", downstreamTaskId)
	assert.Equal(t, 1, downstreamCount, "Downstream task should have 1 dependency")

	// Verify initial states
	upstreamTask, err := db.GetTask(ctx, "test-job/task1")
	assert.NoError(t, err)
	assert.Equal(t, proto.TaskStatus_TASK_STATUS_QUEUEING, upstreamTask.Status)

	downstreamTask, err := db.GetTask(ctx, "test-job/task2")
	assert.NoError(t, err)
	assert.Equal(t, proto.TaskStatus_TASK_STATUS_PENDING, downstreamTask.Status, "Downstream should be pending")

	// Complete upstream task successfully
	exitCode := int32(0)
	result := &BatchTaskResult{
		Payload: &proto.BatchTaskResult{
			ExitCode: &exitCode,
		},
		StartTime: time.Now().Add(-time.Minute),
	}

	err = db.UpdateTaskFinalResult(ctx, "test-job/task1", result)
	assert.NoError(t, err, "Failed to update upstream task result")

	// Check that upstream task is marked as completed
	upstreamTask, err = db.GetTask(ctx, "test-job/task1")
	assert.NoError(t, err)
	assert.Equal(t, proto.TaskStatus_TASK_STATUS_SUCCESS, upstreamTask.Status, "Upstream should be completed")

	// Check that downstream task's pending_upstreams count is decremented
	downstreamTask, err = db.GetTask(ctx, "test-job/task2")
	assert.NoError(t, err)
	assert.Equal(t, proto.TaskStatus_TASK_STATUS_QUEUEING, downstreamTask.Status, "Downstream should be queueing now")
}

func TestRecursiveTaskStatusUpdateFailure(t *testing.T) {
	// Create temporary database file
	dbPath := ":memory:" // Use in-memory database for testing
	db, err := NewSqliteDatabase(dbPath)
	assert.NoError(t, err, "Failed to create database")
	defer db.Close()

	ctx := context.Background()

	// Create upstream task (task1)
	upstreamWorkload := &proto.Workload{
		Command: "echo upstream",
	}

	upstreamSession := &proto.SessionRequest{
		TaskId:     "test-job/task1",
		Pool:       "default",
		InstanceId: 123,
		Priority:   10,
		Workload:   upstreamWorkload,
		Labels:     []string{"env=test", "type=upstream"},
	}

	upstreamTaskId, _, err := db.CreateTask(ctx, upstreamSession)
	assert.NoError(t, err, "Failed to create upstream task")
	assert.Equal(t, "test-job/task1", upstreamTaskId)

	// Create downstream task (task2) that depends on task1 success
	downstreamSession := &proto.SessionRequest{
		TaskId:     "test-job/task2",
		Pool:       "default",
		InstanceId: 124,
		Priority:   5,
		Workload:   &proto.Workload{Command: "echo downstream"},
		Dependencies: []*proto.Dependency{
			{
				UpstreamTaskId: "task1",
				Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
			},
		},
		Labels: []string{"env=test", "type=downstream"},
	}

	downstreamTaskId, downstreamCount, err := db.CreateTask(ctx, downstreamSession)
	assert.NoError(t, err, "Failed to create downstream task")
	assert.Equal(t, "test-job/task2", downstreamTaskId)
	assert.Equal(t, 1, downstreamCount, "Downstream task should have 1 dependency")

	// Fail upstream task
	exitCode := int32(1)
	result := &BatchTaskResult{
		Payload: &proto.BatchTaskResult{
			ExitCode: &exitCode,
		},
		StartTime: time.Now().Add(-time.Minute),
	}

	err = db.UpdateTaskFinalResult(ctx, "test-job/task1", result)
	assert.NoError(t, err, "Failed to update upstream task result")

	// Check that upstream task is marked as failed
	upstreamTask, err := db.GetTask(ctx, "test-job/task1")
	assert.NoError(t, err)
	assert.Equal(t, proto.TaskStatus_TASK_STATUS_FAILURE, upstreamTask.Status, "Upstream should be failed")

	// Check that downstream task is marked as failed upstream
	downstreamTask, err := db.GetTask(ctx, "test-job/task2")
	assert.NoError(t, err)
	assert.Equal(t, proto.TaskStatus_TASK_STATUS_FAILED_UPSTREAM, downstreamTask.Status, "Downstream should be failed upstream")
}
