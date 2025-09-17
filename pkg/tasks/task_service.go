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
package tasks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/emptypb"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
	"velda.io/velda/pkg/storage"
)

const (
	ActionGetTask    = "task.get"
	ActionGetTaskLog = "task.get.log"
	ActionCancelJob  = "task.cancel"
)

type TaskDb interface {
	GetTask(ctx context.Context, taskId string) (*proto.Task, error)
	ListTasks(ctx context.Context, request *proto.ListTasksRequest) ([]*proto.Task, string, error)
	SearchTasks(ctx context.Context, request *proto.SearchTasksRequest) ([]*proto.Task, string, error)
	CancelJob(ctx context.Context, jobId string) error
}

type TaskTracker interface {
	CancelJob(ctx context.Context, jobId string) error
	GetTaskStatus(ctx context.Context, taskId string) (*proto.ExecutionStatus, error)
	WatchTask(ctx context.Context, taskId string, callback func(*proto.ExecutionStatus) bool)
}

type TaskLogDb interface {
	GetTaskLogs(ctx context.Context, instanceId int64, taskId string, options *storage.ReadFileOptions) (stdout storage.ByteStream, stderr storage.ByteStream, err error)
}

type TaskServiceServer struct {
	proto.UnimplementedTaskServiceServer
	ctx         context.Context
	db          TaskDb
	logDb       TaskLogDb
	taskTracker TaskTracker
	permission  rbac.Permissions
}

func NewTaskServiceServer(ctx context.Context, db TaskDb, logDb TaskLogDb, taskTracker TaskTracker, permissions rbac.Permissions) *TaskServiceServer {
	return &TaskServiceServer{
		ctx:         ctx,
		db:          db,
		logDb:       logDb,
		taskTracker: taskTracker,
		permission:  permissions,
	}
}

func (s *TaskServiceServer) GetTask(ctx context.Context, in *proto.GetTaskRequest) (*proto.Task, error) {
	task, err := s.db.GetTask(ctx, in.TaskId)
	if err != nil {
		return nil, err
	}
	s.patchTaskStatus(ctx, task)
	jobIdIndex := strings.Index(in.TaskId, "/")
	jobId := in.TaskId
	if jobIdIndex != -1 {
		jobId = in.TaskId[:jobIdIndex]
	}
	if err := s.permission.Check(ctx, ActionGetTask, fmt.Sprintf("tasks/%d/%s", task.InstanceId, jobId)); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *TaskServiceServer) ListTasks(ctx context.Context, in *proto.ListTasksRequest) (*proto.TaskPageResult, error) {
	tasks, nextCursor, err := s.db.ListTasks(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(tasks) > 0 && in.GetParentId() != "" {
		jobId := in.ParentId[:strings.Index(in.ParentId, "/")]
		if err := s.permission.Check(ctx, ActionGetTask, fmt.Sprintf("tasks/%d/%s", tasks[0].InstanceId, jobId)); err != nil {
			return nil, err
		}
	}
	s.patchTaskStatus(ctx, tasks...)
	return &proto.TaskPageResult{
		Tasks:         tasks,
		NextPageToken: nextCursor,
	}, nil
}

func (s *TaskServiceServer) SearchTasks(ctx context.Context, in *proto.SearchTasksRequest) (*proto.TaskPageResult, error) {
	in.LabelFilters = append(in.LabelFilters, s.permission.SearchKeys(ctx)...)
	tasks, nextCursor, err := s.db.SearchTasks(ctx, in)
	if err != nil {
		return nil, err
	}
	s.patchTaskStatus(ctx, tasks...)
	return &proto.TaskPageResult{
		Tasks:         tasks,
		NextPageToken: nextCursor,
	}, nil
}

func (s *TaskServiceServer) CancelJob(ctx context.Context, in *proto.CancelJobRequest) (*emptypb.Empty, error) {
	task, err := s.db.GetTask(ctx, in.JobId)
	if err != nil {
		return nil, err
	}
	s.patchTaskStatus(ctx, task)
	if err := s.permission.Check(ctx, ActionCancelJob, fmt.Sprintf("tasks/%d/%s", task.InstanceId, task.Id)); err != nil {
		return nil, err
	}
	err = s.db.CancelJob(ctx, in.JobId)
	if err != nil {
		return nil, err
	}
	err = s.taskTracker.CancelJob(ctx, in.JobId)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *TaskServiceServer) WatchTask(in *proto.GetTaskRequest, stream proto.TaskService_WatchTaskServer) error {
	ctx := stream.Context()
	task, err := s.db.GetTask(ctx, in.TaskId)
	if err != nil {
		return err
	}
	if err := s.permission.Check(ctx, ActionGetTask, fmt.Sprintf("tasks/%d/%s", task.InstanceId, task.Id)); err != nil {
		return err
	}
	if task.Status == proto.TaskStatus_TASK_STATUS_QUEUEING || task.Status == proto.TaskStatus_TASK_STATUS_PENDING {
		sendErr := make(chan error, 1)
		s.taskTracker.WatchTask(ctx, in.TaskId, func(status *proto.ExecutionStatus) bool {
			if status == nil {
				// Not leased yet. Send the initial task status.
				err := stream.Send(task)
				if err != nil {
					sendErr <- err
					return false
				}
				return true
			}
			switch status.Status {
			case proto.ExecutionStatus_STATUS_RUNNING:
				task.Status = proto.TaskStatus_TASK_STATUS_RUNNING
				err := stream.Send(task)
				if err != nil {
					sendErr <- err
					return false
				}
			case proto.ExecutionStatus_STATUS_QUEUEING:
				task.Status = proto.TaskStatus_TASK_STATUS_QUEUEING
				err := stream.Send(task)
				if err != nil {
					sendErr <- err
					return false
				}
			case proto.ExecutionStatus_STATUS_TERMINATED:
				sendErr <- io.EOF
				return false
			}
			return true
		})
		select {
		case err := <-sendErr:
			if !errors.Is(err, io.EOF) {
				return err
			}
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
		// Get final status from db
		finalTask, err := s.db.GetTask(ctx, in.TaskId)
		if err != nil {
			return err
		}
		if finalTask.Status == proto.TaskStatus_TASK_STATUS_QUEUEING {
			log.Printf("Unexpected: Task %s is still queueing after session terminated", in.TaskId)
		}
		err = stream.Send(finalTask)
		if err != nil {
			return err
		}
		return nil
	} else {
		err := stream.Send(task)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *TaskServiceServer) Logs(in *proto.LogTaskRequest, stream proto.TaskService_LogsServer) error {
	task, err := s.db.GetTask(stream.Context(), in.TaskId)
	if err != nil {
		return err
	}

	ctx := stream.Context()
	if err := s.permission.Check(ctx, ActionGetTaskLog, fmt.Sprintf("tasks/%d/%s", task.InstanceId, task.Id)); err != nil {
		return err
	}
	options := &storage.ReadFileOptions{}
	if in.Follow && (task.Status == proto.TaskStatus_TASK_STATUS_PENDING || task.Status == proto.TaskStatus_TASK_STATUS_QUEUEING) {
		// Wait until the task is started, and finish when completed.
		subctx, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = subctx
		taskStarted := make(chan struct{})
		s.taskTracker.WatchTask(ctx, in.TaskId, func(status *proto.ExecutionStatus) bool {
			if status == nil {
				return true
			}
			switch status.Status {
			case proto.ExecutionStatus_STATUS_RUNNING:
				// TODO: Currently there's a chance of race if the file is not created yet.
				close(taskStarted)
			case proto.ExecutionStatus_STATUS_TERMINATED:
				cancel()
				return false
			}
			return true
		})
		select {
		case <-taskStarted:
			options.Follow = true
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	stdout, stderr, err := s.logDb.GetTaskLogs(ctx, task.InstanceId, in.TaskId, options)
	if err != nil {
		return err
	}
	wg := sync.WaitGroup{}
	wg.Add(2)
	handleStream := func(streamType proto.LogTaskResponse_Stream, s storage.ByteStream) {
		defer wg.Done()
		for {
			select {
			case line := <-s.Data:
				stream.Send(&proto.LogTaskResponse{Stream: streamType, Data: line})
			case err := <-s.Err:
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				log.Printf("Error reading stderr for task %s: %v", in.TaskId, err)
				return
			}
		}
	}
	go handleStream(proto.LogTaskResponse_STREAM_STDOUT, stdout)
	go handleStream(proto.LogTaskResponse_STREAM_STDERR, stderr)
	wg.Wait()
	return nil
}

func (s *TaskServiceServer) patchTaskStatus(ctx context.Context, tasks ...*proto.Task) error {
	// The storage returns running/queueing as queueing.
	// Needs to query the task tracker for the real status.
	for _, task := range tasks {
		if task.Status == proto.TaskStatus_TASK_STATUS_QUEUEING {
			status, err := s.taskTracker.GetTaskStatus(ctx, task.Id)
			// Possibly the task has finished and removed from the tracker.
			// Check the status from DB again.
			if err != nil {
				newtask, err := s.db.GetTask(ctx, task.Id)
				if err != nil {
					return err
				}
				task.Status = newtask.Status
				continue
			}
			if status != nil && status.Status == proto.ExecutionStatus_STATUS_RUNNING {
				task.Status = proto.TaskStatus_TASK_STATUS_RUNNING
			}
		}
	}
	return nil
}
