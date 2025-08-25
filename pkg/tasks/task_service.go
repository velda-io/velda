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
	"fmt"
	"strings"

	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

const (
	ActionGetTask = "task.get"
)

type TaskDb interface {
	GetTask(ctx context.Context, taskId string) (*proto.Task, error)
	ListTasks(ctx context.Context, request *proto.ListTasksRequest) ([]*proto.Task, string, error)
	SearchTasks(ctx context.Context, request *proto.SearchTasksRequest) ([]*proto.Task, string, error)
}

type TaskServiceServer struct {
	proto.UnimplementedTaskServiceServer
	db         TaskDb
	permission rbac.Permissions
}

func NewTaskServiceServer(db TaskDb, permissions rbac.Permissions) *TaskServiceServer {
	return &TaskServiceServer{
		db:         db,
		permission: permissions,
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
