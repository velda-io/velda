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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

// TaskLogServiceServer implements the TaskLogService gRPC service.
// It receives streamed stdout/stderr from agents and saves them to logDir.
type TaskLogServiceServer struct {
	proto.UnimplementedTaskLogServiceServer
	logDir string
}

func NewTaskLogServiceServer(logDir string) *TaskLogServiceServer {
	return &TaskLogServiceServer{logDir: logDir}
}

// PushLogs receives streamed stdout/stderr chunks from an agent and appends
// them to per-task log files under s.logDir.
func (s *TaskLogServiceServer) PushLogs(stream proto.TaskLogService_PushLogsServer) error {
	if s.logDir == "" {
		return status.Errorf(codes.FailedPrecondition, "log_dir not configured on server")
	}

	// Only a session user (i.e. an authenticated agent running a specific task)
	// is allowed to push logs. We also verify that the task ID embedded in the
	// session token matches the task ID supplied in the stream.
	sessionCaller, ok := rbac.UserFromContext(stream.Context()).(rbac.SessionUser)
	if !ok {
		return status.Errorf(codes.PermissionDenied, "PushLogs requires a session user")
	}

	files := map[proto.LogTaskResponse_Stream]*os.File{}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	getFile := func(taskId string, streamType proto.LogTaskResponse_Stream) (*os.File, error) {
		if f, ok := files[streamType]; ok {
			return f, nil
		}
		var name string
		switch streamType {
		case proto.LogTaskResponse_STREAM_STDOUT:
			name = "stdout"
		case proto.LogTaskResponse_STREAM_STDERR:
			name = "stderr"
		default:
			return nil, fmt.Errorf("unknown stream type %v", streamType)
		}
		path := filepath.Join(s.logDir, taskId, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("create log dir: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("open log file %s: %w", path, err)
		}
		files[streamType] = f
		return f, nil
	}
	var taskId string
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if taskId == "" {
			taskId = req.TaskId
			if sessionCaller.TaskId() != taskId {
				return status.Errorf(codes.PermissionDenied,
					"session task_id %q does not match push task_id %q",
					sessionCaller.TaskId(), taskId)
			}
		} else if taskId != req.TaskId {
			return status.Errorf(codes.InvalidArgument, "task_id changed mid-stream: %s -> %s", taskId, req.TaskId)
		}
		f, err := getFile(req.TaskId, req.Stream)
		if err != nil {
			return status.Errorf(codes.Internal, "%v", err)
		}
		if _, err := f.Write(req.Data); err != nil {
			return status.Errorf(codes.Internal, "write log: %v", err)
		}
	}
	return nil
}
