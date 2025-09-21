package tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
)

// TaskDB is the minimal interface required to run task-related tests.
type TaskDB interface {
	CreateTask(ctx context.Context, session *proto.SessionRequest) (string, int, error)
	ListTasks(ctx context.Context, request *proto.ListTasksRequest) ([]*proto.Task, string, error)
	SearchTasks(ctx context.Context, request *proto.SearchTasksRequest) ([]*proto.Task, string, error)
	PollTasks(ctx context.Context, leaserIdentity string, callback func(string, *db.TaskWithUser) error) error
	GetUpstreamCount(ctx context.Context, taskId string) (int, error)
	GetTaskStatus(ctx context.Context, taskId string) (string, error)
	UpdateTaskFinalResult(ctx context.Context, taskId string, result *db.BatchTaskResult) error
	RenewLeaser(ctx context.Context, leaserIdentity string, now time.Time) error
	ReconnectTask(ctx context.Context, taskId string, leaserIdentity string) error
	ReleaseExpiredLeaser(ctx context.Context, lastLeaseTime time.Time) error
}

func RunTestTaskWithDb(t *testing.T, sdb TaskDB, instanceId int64) {
	workload1 := &proto.Workload{
		Command: "echo",
	}
	workload2 := &proto.Workload{
		Command: "cat",
	}
	ctx := context.Background()
	createTaskOfId := func(t *testing.T, id string) {
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     id,
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		assert.NoError(t, err)
	}
	t.Run("CreateTask", func(t *testing.T) {
		createTaskOfId(t, "job1")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "job1/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Priority:   1,
			Workload:   workload1,
			Labels:     []string{"label1=value1"},
		})
		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "job1/s2",
			Pool:       "pool2",
			InstanceId: instanceId,
			Priority:   1,
			Workload:   workload2,
			Labels:     []string{"label1=value2"},
		})
		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "job2/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Priority:   1,
			Workload:   workload1,
			Labels:     []string{"label1=value2"},
		})
		tasks := []*db.TaskWithUser{}
		assert.NoError(t, err)
		pollCtx, cancel := context.WithCancel(ctx)
		callback := func(leaser string, task *db.TaskWithUser) error {
			tasks = append(tasks, task)
			if len(tasks) == 4 {
				cancel()
			}
			return nil
		}
		err = sdb.PollTasks(pollCtx, "leaser", callback)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Len(t, tasks, 4)
		assert.Equal(t, "job1", tasks[0].Id)
		assert.Equal(t, "job1/s1", tasks[1].Id)
	})

	t.Run("CreateTaskWithDupName", func(t *testing.T) {
		createTaskOfId(t, "job_seq")
		var taskIds []string
		for i := 0; i < 5; i++ {
			taskId, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
				TaskId:     "job_seq/task",
				Pool:       "pool",
				InstanceId: instanceId,
				Workload:   workload1,
			})
			assert.NoError(t, err)
			taskIds = append(taskIds, taskId)
		}
		assert.Equal(t, []string{
			"job_seq/task", "job_seq/task_2", "job_seq/task_3", "job_seq/task_4", "job_seq/task_5",
		}, taskIds)
	})

	t.Run("ListTasks", func(t *testing.T) {
		tasks, _, err := sdb.ListTasks(ctx, &proto.ListTasksRequest{
			ParentId: "job1",
		})
		assert.NoError(t, err)
		assert.Len(t, tasks, 2)
		assert.Equal(t, "job1/s1", tasks[0].Id)
		assert.Equal(t, "job1/s2", tasks[1].Id)
		assert.Equal(t, "pool", tasks[0].Pool)
		assert.Equal(t, "pool2", tasks[1].Pool)
		assert.Equal(t, []string{"label1=value1"}, tasks[0].Labels)
		assert.Equal(t, []string{"label1=value2"}, tasks[1].Labels)
	})

	t.Run("SearchTasksWithFilter", func(t *testing.T) {
		tasks, _, err := sdb.SearchTasks(ctx, &proto.SearchTasksRequest{
			LabelFilters: []string{"label1=value2"},
		})
		assert.NoError(t, err)
		assert.Len(t, tasks, 2)
		assert.Equal(t, "job2/s1", tasks[0].Id)
		assert.Equal(t, "job1/s2", tasks[1].Id)
	})

	t.Run("DependencyMet", func(t *testing.T) {
		createTaskOfId(t, "jobd")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd/s2",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)

		cnt, err := sdb.GetUpstreamCount(ctx, "jobd/s2")
		assert.NoError(t, err)
		assert.Equal(t, 1, cnt)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd/s1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)

		cnt, err = sdb.GetUpstreamCount(ctx, "jobd/s2")
		assert.NoError(t, err)
		assert.Equal(t, 0, cnt)
	})

	t.Run("DependencyUnmetRecursiveUpdate", func(t *testing.T) {
		createTaskOfId(t, "jobd2")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd2/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd2/s2",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)

		// Recursively should fail.
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd2/s3",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s2",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)

		// Recursively should be met.
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd2/s4",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s2",
					Type:           proto.Dependency_DEPENDENCY_TYPE_FAILURE,
				},
			},
		})
		assert.NoError(t, err)
		// Task enqueueing completed.
		err = sdb.UpdateTaskFinalResult(ctx, "jobd2", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)
		status, err := sdb.GetTaskStatus(ctx, "jobd2")
		assert.NoError(t, err)
		assert.Equal(t, "RUNNING_SUBTASKS", status)

		cnt, err := sdb.GetUpstreamCount(ctx, "jobd2/s2")
		assert.NoError(t, err)
		assert.Equal(t, 1, cnt)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd2/s1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				TerminatedSignal: 13,
			},
		})
		assert.NoError(t, err)

		status, err = sdb.GetTaskStatus(ctx, "jobd2/s2")
		assert.NoError(t, err)
		assert.Equal(t, "FAILED_UPSTREAM", status)

		status, err = sdb.GetTaskStatus(ctx, "jobd2/s3")
		assert.NoError(t, err)
		assert.Equal(t, "FAILED_UPSTREAM", status)

		status, err = sdb.GetTaskStatus(ctx, "jobd2/s4")
		assert.NoError(t, err)
		assert.Equal(t, "QUEUEING", status)
		cnt, err = sdb.GetUpstreamCount(ctx, "jobd2/s4")
		assert.NoError(t, err)
		assert.Equal(t, 0, cnt)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd2/s4", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)

		// The job should be marked as completed now.

		status, err = sdb.GetTaskStatus(ctx, "jobd2")
		assert.NoError(t, err)
		assert.Equal(t, "FAILED", status)
	})

	t.Run("DependencyMetBeforeCreate", func(t *testing.T) {
		createTaskOfId(t, "jobd3")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd3/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		err = sdb.UpdateTaskFinalResult(ctx, "jobd3/s1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)

		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd3/s2",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)
		cnt, err := sdb.GetUpstreamCount(ctx, "jobd3/s2")
		assert.NoError(t, err)
		assert.Equal(t, 0, cnt)
	})

	t.Run("DependencyUnmetBeforeCreate", func(t *testing.T) {
		createTaskOfId(t, "jobd4")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd4/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})

		assert.NoError(t, err)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd4/s1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				TerminatedSignal: 13,
			},
		})

		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd4/s2",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)

		status, err := sdb.GetTaskStatus(ctx, "jobd4/s2")
		assert.NoError(t, err)
		assert.Equal(t, "FAILED_UPSTREAM", status)
	})

	t.Run("DependencyMultiMet", func(t *testing.T) {
		createTaskOfId(t, "jobd5")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd5/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd5/s2",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)

		// Both dependencies are met in one update.
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd5/s3",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "s1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_UNSPECIFIED,
				},
				{
					UpstreamTaskId: "s2",
					Type:           proto.Dependency_DEPENDENCY_TYPE_UNSPECIFIED,
				},
			},
		})
		assert.NoError(t, err)

		cnt, err := sdb.GetUpstreamCount(ctx, "jobd5/s3")
		assert.NoError(t, err)
		assert.Equal(t, 2, cnt)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd5/s1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				TerminatedSignal: 13,
			},
		})
		assert.NoError(t, err)

		status, err := sdb.GetTaskStatus(ctx, "jobd5/s2")
		assert.NoError(t, err)
		assert.Equal(t, "FAILED_UPSTREAM", status)

		status, err = sdb.GetTaskStatus(ctx, "jobd5/s3")
		assert.NoError(t, err)
		assert.Equal(t, "QUEUEING", status)
		cnt, err = sdb.GetUpstreamCount(ctx, "jobd5/s3")
		assert.NoError(t, err)
		assert.Equal(t, 0, cnt)
	})

	t.Run("DependencyMultiLevel", func(t *testing.T) {
		createTaskOfId(t, "jobd6")
		_, _, err := sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd6/l1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		assert.NoError(t, err)
		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd6/l2",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
			Dependencies: []*proto.Dependency{
				{
					UpstreamTaskId: "l1",
					Type:           proto.Dependency_DEPENDENCY_TYPE_SUCCESS,
				},
			},
		})
		assert.NoError(t, err)

		_, _, err = sdb.CreateTask(ctx, &proto.SessionRequest{
			TaskId:     "jobd6/l1/s1",
			Pool:       "pool",
			InstanceId: instanceId,
			Workload:   workload1,
		})
		assert.NoError(t, err)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd6", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd6/l1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)

		status, err := sdb.GetTaskStatus(ctx, "jobd6/l1")
		assert.NoError(t, err)
		assert.Equal(t, "RUNNING_SUBTASKS", status)

		err = sdb.UpdateTaskFinalResult(ctx, "jobd6/l1/s1", &db.BatchTaskResult{
			Payload: &proto.BatchTaskResult{
				ExitCode: new(int32),
			},
		})
		assert.NoError(t, err)

		status, err = sdb.GetTaskStatus(ctx, "jobd6/l1")
		assert.NoError(t, err)
		assert.Equal(t, "COMPLETED", status)

		cnt, err := sdb.GetUpstreamCount(ctx, "jobd6/l2")
		assert.NoError(t, err)
		assert.Equal(t, 0, cnt)
	})
}
