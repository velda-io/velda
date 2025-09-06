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
package broker

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/emptypb"

	"velda.io/velda"
	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

const (
	ActionRequestSession = rbac.Action("instance.request_session")
)

type TaskDb interface {
	CreateTask(ctx context.Context, session *proto.SessionRequest) (string, int, error)
	UpdateTaskFinalResult(ctx context.Context, taskId string, result *db.BatchTaskResult) error
}

type AuthHelper interface {
	UpdateServerInfo(context.Context, *proto.ServerInfo) error
	GrantAccessToAgent(context.Context, *Agent, *Session) error
	RevokeAccessToAgent(context.Context, *Agent, *Session) error
	GrantAccessToClient(context.Context, *Session, *proto.ExecutionStatus) error
}

type server struct {
	proto.UnimplementedBrokerServiceServer
	AuthHelper
	permissions rbac.Permissions
	ctx         context.Context
	scheduler   *SchedulerSet
	sessions    *SessionDatabase
	taskTracker *TaskTracker

	db TaskDb
}

func NewBrokerServer(schedulerSet *SchedulerSet, sessions *SessionDatabase, permission rbac.Permissions, taskTracker *TaskTracker, authHelper AuthHelper, db TaskDb) *server {
	return &server{
		AuthHelper:  authHelper,
		scheduler:   schedulerSet,
		sessions:    sessions,
		ctx:         schedulerSet.ctx,
		permissions: permission,
		taskTracker: taskTracker,
		db:          db,
	}
}

func (s *server) AgentUpdate(stream proto.BrokerService_AgentUpdateServer) error {
	req, err := stream.Recv()
	identity := req.AgentIdentity
	if err != nil {
		return err
	}
	peerInfo, ok := peer.FromContext(stream.Context())
	if !ok {
		return fmt.Errorf("peer info not found")
	}
	scheduler, err := s.scheduler.GetPool(identity.Pool)
	if err != nil {
		return err
	}
	agent := newAgent(req, peerInfo, scheduler, s)
	for _, session := range req.CurrentExecutions {
		request := session.Request
		key := SessionKey{
			InstanceId: request.InstanceId,
			SessionId:  request.SessionId,
		}
		if request.TaskId != "" {
			err := s.taskTracker.ReconnectTask(stream.Context(), request.TaskId)
			if err != nil {
				log.Printf("Failed to reconnect task %s: %v", request.TaskId, err)
				// We should terminate the task? Also need to validate the previous leaser identity.
				// For now, let's just ignore it.
			}
		}
		update := func(sess *Session) error {
			agent.mySessions[key] = sess
			agent.handleSessionInitResponse(session.Response)
			sess.Reconnect()
			return nil
		}
		session, err := s.sessions.RegisterSession(request, scheduler, update)
		if err != nil {
			log.Printf("Failed to register session %s: %v", request.SessionId, err)
			continue
		}
		log.Printf("Agent %s: Reconnected session %s", agent.id, session.Key())
	}
	serverInfo := &proto.ServerInfo{
		Version: velda.GetVersion(),
	}
	if err := s.UpdateServerInfo(s.ctx, serverInfo); err != nil {
		return err
	}
	if err := stream.Send(&proto.AgentUpdateResponse{ServerInfo: serverInfo}); err != nil {
		return err
	}
	return agent.Run(stream, s.ctx)
}

func (s *server) RequestSession(ctx context.Context, req *proto.SessionRequest) (*proto.ExecutionStatus, error) {
	instanceIdStr := fmt.Sprintf("instances/%d", req.InstanceId)
	if err := s.permissions.Check(ctx, ActionRequestSession, instanceIdStr); err != nil {
		return nil, err
	}

	if req.Workload != nil {
		user := rbac.UserFromContext(ctx)
		sessionUser, ok := user.(rbac.SessionUser)
		inBatch := ok && sessionUser.TaskId() != ""
		// Assign & fixup task ID
		if inBatch {
			if strings.Contains(req.TaskId, "/") {
				return nil, fmt.Errorf("Task ID cannot contain '/'")
			}

			if req.TaskId == "" {
				req.TaskId = "task"
			}
			req.TaskId = fmt.Sprintf("%s/%s", sessionUser.TaskId(), req.TaskId)
		} else if req.TaskId == "" {
			req.TaskId = uuid.NewString()
		}

		req.Labels = append(req.Labels,
			fmt.Sprintf("pool=%s", req.Pool),
			fmt.Sprintf("priority=%d", req.Priority),
		)
		if inBatch {
			req.Labels = append(req.Labels, fmt.Sprintf("parent_id=%s", sessionUser.TaskId()))
		} else {
			req.Labels = append(req.Labels, "job")
		}
		req.Labels = append(req.Labels, s.permissions.SearchKeys(ctx)...)
		if req.Workload.TotalShards > 0 {
			req.Workload.ShardIndex = -1
		}

		taskid, _, err := s.db.CreateTask(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to create task: %v", err)
		}
		return &proto.ExecutionStatus{
			SessionId: req.SessionId,
			Status:    proto.ExecutionStatus_STATUS_QUEUEING,
			TaskId:    taskid,
		}, nil
	}
	var scheduler *Scheduler
	if req.Pool != "" {
		var err error
		scheduler, err = s.scheduler.GetPool(req.Pool)
		if err != nil {
			return nil, err
		}
	}
	session, err := s.sessions.AddSession(req, scheduler)
	if err != nil && !errors.Is(err, AlreadyExistsError) {
		return nil, err
	}

	resp, err := session.Schedule(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.GrantAccessToClient(ctx, session, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *server) ListSessions(ctx context.Context, req *proto.ListSessionsRequest) (*proto.ListSessionsResponse, error) {
	var sessions []*Session
	var err error
	if req.ServiceName != "" {
		sessions, err = s.sessions.ListSessionForService(req.InstanceId, req.ServiceName)
	} else {
		sessions, err = s.sessions.ListSessions(req.InstanceId)
	}
	if err != nil {
		return nil, err
	}
	resp := &proto.ListSessionsResponse{
		Sessions: make([]*proto.Session, 0, len(sessions)),
	}
	for _, session := range sessions {
		sessionProto := &proto.Session{
			Status:      session.Status().GetStatus(),
			SessionId:   session.Request.SessionId,
			Pool:        session.Request.Pool,
			InstanceId:  session.Request.InstanceId,
			User:        session.Request.User,
			ServiceName: session.Request.ServiceName,
			Labels:      session.Request.Labels,
		}
		if session.status.SshConnection != nil {
			sessionProto.InternalIpAddress = session.status.SshConnection.Host
		}
		resp.Sessions = append(resp.Sessions, sessionProto)
	}
	return resp, nil
}

func (s *server) KillSession(ctx context.Context, req *proto.KillSessionRequest) (*emptypb.Empty, error) {
	if err := s.permissions.Check(ctx, rbac.Action("instance.kill_session"), fmt.Sprintf("instances/%d", req.InstanceId)); err != nil {
		return nil, err
	}

	var sessions []*Session
	if req.ServiceName != "" {
		var err error
		sessions, err = s.sessions.ListSessionForService(req.InstanceId, req.ServiceName)
		if err != nil {
			return nil, fmt.Errorf("failed to list sessions for service %s: %v", req.ServiceName, err)
		}
		if len(sessions) == 0 {
			return nil, fmt.Errorf("no sessions found for service %s", req.ServiceName)
		}
	} else {
		session, err := s.sessions.GetSession(req.InstanceId, req.SessionId)
		if err != nil {
			return nil, fmt.Errorf("failed to get session: %v", err)
		}
		if session == nil {
			return nil, fmt.Errorf("session %s not found", req.SessionId)
		}
		sessions = []*Session{session}
	}
	log.Printf("Killing %d session for instance %d with service name %s", len(sessions), req.InstanceId, req.ServiceName)
	for _, session := range sessions {
		if err := session.Kill(ctx, req.Force); err != nil {
			return nil, fmt.Errorf("failed to kill session: %v", err)
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *server) UpdateTaskResult(ctx context.Context, session *Session, result *proto.BatchTaskResult) error {
	return s.db.UpdateTaskFinalResult(ctx, session.Request.TaskId, &db.BatchTaskResult{
		Payload:   result,
		StartTime: session.startTime,
	})
}
