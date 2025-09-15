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
	ActionGetTask   = "task.get"
	ActionCancelJob = "task.cancel"
)

type TaskDb interface {
	GetTask(ctx context.Context, taskId string) (*proto.Task, error)
	ListTasks(ctx context.Context, request *proto.ListTasksRequest) ([]*proto.Task, string, error)
	SearchTasks(ctx context.Context, request *proto.SearchTasksRequest) ([]*proto.Task, string, error)
	CancelJob(ctx context.Context, jobId string) error
}

type TaskTracker interface {
	CancelJob(ctx context.Context, jobId string) error
}

type TaskLogDb interface {
	GetTaskLogs(ctx context.Context, instanceId int64, taskId string) (stdout storage.ByteStream, stderr storage.ByteStream, err error)
}

type TaskServiceServer struct {
	proto.UnimplementedTaskServiceServer
	db          TaskDb
	logDb       TaskLogDb
	taskTracker TaskTracker
	permission  rbac.Permissions
}

func NewTaskServiceServer(db TaskDb, logDb TaskLogDb, taskTracker TaskTracker, permissions rbac.Permissions) *TaskServiceServer {
	return &TaskServiceServer{
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

func (s *TaskServiceServer) Logs(in *proto.LogTaskRequest, stream proto.TaskService_LogsServer) error {
	task, err := s.db.GetTask(stream.Context(), in.TaskId)
	stdout, stderr, err := s.logDb.GetTaskLogs(stream.Context(), task.InstanceId, in.TaskId)
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
				if errors.Is(err, io.EOF) {
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
