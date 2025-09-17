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
	"sync"
	"time"

	pb "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"velda.io/velda/pkg/proto"
)

type schedulingState int

const DefaultStaleTimeout = 2 * time.Minute

const (
	schedulingStateUnknown schedulingState = iota
	schedulingStateCreated
	schedulingStateQueueing
	schedulingStateSentToAgent
	schedulingStateRunning
	schedulingStateCancelling
	schedulingStateCancellingAgent
	schedulingStateCancelled
	// Lost connection. Wait for termination or reconnect.
	// If terminated, it will be rescheduled if requested.
	schedulingStateStale
	schedulingStateReconnected
	schedulingStateStaleTimerExpired
	schedulingStateTerminated
)

type AgentSessionResponse struct {
	ExecutionStatus *proto.ExecutionStatus
	Error           error
}

type Session struct {
	id        string
	scheduler *Scheduler
	mu        sync.Mutex
	Request   *proto.SessionRequest
	agent     *Agent
	status    *proto.ExecutionStatus
	state     schedulingState

	priority int64
	// Contexts that are waiting for session to be scheduled.
	scheduleCtx []context.Context

	agentChan     chan *Agent
	agentResponse chan AgentSessionResponse
	cancelConfirm chan struct{}
	gang          *GangCoordinator

	scheduleCompletion chan struct{}
	schedulingErr      error

	staleCompletion *time.Timer

	// Request to connect to session from client.
	staleConnectionRequest chan struct{}
	// Agent re-connect to broker.
	staleReconnect chan struct{}
	staleTimeout   time.Duration

	createTime     time.Time
	startTime      time.Time
	lastActiveTime time.Time

	completions []func()
	helpers     SessionHelper
}

func NewSession(request *proto.SessionRequest, scheduler *Scheduler, helpers SessionHelper) *Session {
	return &Session{
		id:        request.SessionId,
		scheduler: scheduler,
		Request:   request,
		priority:  request.Priority,
		status: &proto.ExecutionStatus{
			SessionId: request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_UNSPECIFIED,
		},
		agentChan:     make(chan *Agent, 1),
		agentResponse: make(chan AgentSessionResponse, 1),
		cancelConfirm: make(chan struct{}, 1),
		staleTimeout:  DefaultStaleTimeout,
		createTime:    time.Now(),
		helpers:       helpers,
	}
}

func (s *Session) SetGang(gang *GangCoordinator) {
	if s.status.Status != proto.ExecutionStatus_STATUS_UNSPECIFIED {
		panic("Cannot set gang after scheduling started")
	}
	s.gang = gang
}

func (s *Session) Status() *proto.ExecutionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := pb.Clone(s.status).(*proto.ExecutionStatus)
	return status
}

func (s *Session) Key() SessionKey {
	return SessionKey{
		InstanceId: s.Request.InstanceId,
		SessionId:  s.Request.SessionId,
	}
}

func (s *Session) Schedule(ctx context.Context) (*proto.ExecutionStatus, error) {
	for {
		status, completion := func() (*proto.ExecutionStatus, chan struct{}) {
			s.mu.Lock()
			defer s.mu.Unlock()
			switch s.status.Status {
			case proto.ExecutionStatus_STATUS_RUNNING:
				status := pb.Clone(s.status).(*proto.ExecutionStatus)
				return status, nil

			case proto.ExecutionStatus_STATUS_CHECKPOINTED:
				s.Request.Checkpointed = true
				fallthrough
			case proto.ExecutionStatus_STATUS_UNSPECIFIED:
				s.scheduleCompletion = make(chan struct{})
				s.status.Status = proto.ExecutionStatus_STATUS_QUEUEING
				go s.scheduleLoop(schedulingStateCreated)
				fallthrough
			case proto.ExecutionStatus_STATUS_QUEUEING:
				s.scheduleCtx = append(s.scheduleCtx, ctx)
				return nil, s.scheduleCompletion
			case proto.ExecutionStatus_STATUS_WAITING_FOR_OTHER_SHARDS:
				return s.status, nil
			case proto.ExecutionStatus_STATUS_STALE:
				s.scheduleCtx = append(s.scheduleCtx, ctx)
				close(s.staleConnectionRequest)
				s.staleConnectionRequest = make(chan struct{})
				return nil, s.scheduleCompletion
			case proto.ExecutionStatus_STATUS_TERMINATED:
				return s.status, nil
			}
			panic("unreachable")
		}()
		if status != nil {
			return status, s.schedulingErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-completion:
			// Restart the loop to check latest status.
			continue
		}
	}
}

func (s *Session) popCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.popCtxLocked()
}

func (s *Session) popCtxLocked() context.Context {
	if len(s.scheduleCtx) == 0 {
		return nil
	}
	ctx := s.scheduleCtx[0]
	s.scheduleCtx = s.scheduleCtx[1:]
	return ctx
}

func (s *Session) scheduleLoop(startingState schedulingState) {
	scheduler := s.scheduler
	s.state = startingState
	pool := scheduler.PoolManager
	ctx := s.popCtx()
	defer func() {
		for ctx != nil {
			ctx = s.popCtx()
		}
	}()

	batched := strings.Contains(s.Request.Pool, ":")
	s.schedulingErr = nil
	defer close(s.scheduleCompletion)
	for {
		nextState := s.state
		switch s.state {
		case schedulingStateCreated:
			nextState = schedulingStateQueueing
			if !batched {
				// For batched, the request happens during reservation.
				pool.RequestWorker()
			}
			scheduler.AddSession(s)
		case schedulingStateQueueing:
			select {
			case <-ctx.Done():
				ctx = s.popCtx()
				if ctx != nil {
					continue
				}
				nextState = schedulingStateCancelling
				scheduler.RemoveSession(s)
				s.schedulingErr = errors.New("all context cancelled")
			case agent := <-s.agentChan:
				// This should not block with buffer size 1.
				s.agent = agent
				if s.gang != nil {
					s.agent.sessionReq <- SessionRequest{session: s, ctx: ctx, accept: true, holdResourceOnly: true}
					c := ctx
					s.status.Status = proto.ExecutionStatus_STATUS_WAITING_FOR_OTHER_SHARDS
					s.gang.Notify(int(s.Request.Workload.ShardIndex), func() {
						s.agent.sessionReq <- SessionRequest{session: s, ctx: c, accept: true}
					})
					return
				}
				s.agent.sessionReq <- SessionRequest{session: s, ctx: ctx, accept: true}
				nextState = schedulingStateSentToAgent
			}
		case schedulingStateSentToAgent:
			response := <-s.agentResponse
			if response.Error != nil {
				// TODO: Retry scheduling?
				s.schedulingErr = fmt.Errorf("Error from agent: %w", response.Error)
				s.Complete(proto.SessionExecutionFinalState_SESSION_EXECUTION_FINAL_STATE_STARTUP_FAILURE)
				return
			}
			// Successfully scheduled.
			s.mu.Lock()
			defer s.mu.Unlock()
			s.updateFromAgentResponseLocked(response)
			s.notifyChangeLocked()
			return
		case schedulingStateCancelling:
			select {
			case <-s.cancelConfirm:
			case agent := <-s.agentChan:
				// It's possible that scheduler send to agent before processing
				// cancellation request.
				// We tell the agent to resubmit itself to request next session.
				// Still mark it as request cancelled, since the agent is only
				// mark it busy after it received the request.
				agent.sessionReq <- SessionRequest{session: s, accept: false}
			}
			pool.RequestCancelled()
			s.Complete(proto.SessionExecutionFinalState_SESSION_EXECUTION_FINAL_STATE_CANCELLED)
			return
		case schedulingStateStale:
			var staleConnectionRequest chan struct{}
			var ctxDone <-chan struct{}
			var staleCompletion *time.Timer
			var staleReconnect chan struct{}

			// Need to retrieve stale related signals with lock, in the event of
			// reconnection become stale.
			func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				if ctx == nil || ctx.Err() != nil {
					ctx = s.popCtxLocked()
				}
				if ctx == nil {
					staleConnectionRequest = s.staleConnectionRequest
				} else {
					ctxDone = ctx.Done()
				}
				staleCompletion = s.staleCompletion
				staleReconnect = s.staleReconnect
			}()

			select {
			case <-ctxDone:
				// Reevaluate ctx.
				continue
			case <-staleConnectionRequest:
				// Reevaluate ctx
				continue
			case <-staleCompletion.C:
				nextState = schedulingStateStaleTimerExpired
			case <-staleReconnect:
				nextState = schedulingStateReconnected
			}
		case schedulingStateReconnected:
			return
		case schedulingStateStaleTimerExpired:
			func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				// We need to check the status again because it might have been
				// reconnected when the completion timer expired.
				if s.status.Status == proto.ExecutionStatus_STATUS_RUNNING {
					select {
					case <-s.staleReconnect:
					default:
						panic("Transited to running state without reconnection")
					}
					nextState = schedulingStateReconnected
					return
				}
				if ctx == nil {
					// Stale and no connection request. Terminate.
					s.completeLocked(proto.SessionExecutionFinalState_SESSION_EXECUTION_FINAL_STATE_WORKER_LOST)
					nextState = schedulingStateTerminated
					return
				} else {
					// Reconnect to another agent, use a new execution Id.
					s.status.Status = proto.ExecutionStatus_STATUS_QUEUEING
					nextState = schedulingStateCreated
					s.notifyChangeLocked()
				}
			}()
		case schedulingStateTerminated:
			return
		}
		s.state = nextState
	}
	panic("unreachable")
}

func (s *Session) MarkStale(lastActiveTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Status == proto.ExecutionStatus_STATUS_RUNNING && lastActiveTime.After(s.lastActiveTime) {
		// TODO: This should be retry/reconnect at client and ensure a new
		// session ID is generated.
		log.Printf("Session %s: Marked stale(worker lost). Rescheduling", s.id)
		s.status.Status = proto.ExecutionStatus_STATUS_STALE
		s.staleCompletion = time.NewTimer(s.staleTimeout)
		s.staleConnectionRequest = make(chan struct{})
		s.staleReconnect = make(chan struct{})
		s.scheduleCompletion = make(chan struct{})
		if s.Request.TaskId != "" {
			s.scheduleCtx = append(s.scheduleCtx, s.scheduler.ctx)
		}
		go s.scheduleLoop(schedulingStateStale)
	} else if s.status.Status == proto.ExecutionStatus_STATUS_WAITING_FOR_OTHER_SHARDS {
		// TODO: There may be race if the gang is ready at the same time.
		s.gang.Unnotify(int(s.Request.Workload.ShardIndex))
		// Restart the scheduling.
		s.status.Status = proto.ExecutionStatus_STATUS_QUEUEING
		s.scheduleCompletion = make(chan struct{})
		if s.Request.TaskId != "" {
			s.scheduleCtx = append(s.scheduleCtx, s.scheduler.ctx)
		}
		go s.scheduleLoop(schedulingStateCreated)
	}
	s.notifyChangeLocked()
}

func (s *Session) Reconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.status.Status {
	case proto.ExecutionStatus_STATUS_STALE:
		s.staleCompletion.Stop()
		close(s.staleReconnect)
		s.status.Status = proto.ExecutionStatus_STATUS_RUNNING
	case proto.ExecutionStatus_STATUS_UNSPECIFIED:
		s.updateFromAgentResponseLocked(<-s.agentResponse)
	case proto.ExecutionStatus_STATUS_RUNNING:
		// May happen if stale handling is delayed.
	default:
		log.Printf("Session %s: Reconnect called in state %v", s.id, s.state)
	}
	s.lastActiveTime = time.Now()
	s.notifyChangeLocked()
}

func (s *Session) Checkpointed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Status = proto.ExecutionStatus_STATUS_CHECKPOINTED
	log.Printf("Session %s: Checkpointed", s.id)
	s.recordExecution(proto.SessionExecutionFinalState_SESSION_EXECUTION_FINAL_STATE_CHECKPOINTED)
	s.notifyChangeLocked()
}

func (s *Session) Complete(finalStatus proto.SessionExecutionFinalState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completeLocked(finalStatus)
}

func (s *Session) Kill(ctx context.Context, force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.Status == proto.ExecutionStatus_STATUS_RUNNING {
		return s.agent.RequestKill(ctx, s.Request.InstanceId, s.Request.SessionId, force)
	}
	// TODO: Maybe remove from the queue?
	return fmt.Errorf("Session %s is not running, cannot kill", s.id)
}

func (s *Session) AddCompletion(completion func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completions = append(s.completions, completion)
}

func (s *Session) completeLocked(finalStatus proto.SessionExecutionFinalState) {
	s.status.Status = proto.ExecutionStatus_STATUS_TERMINATED
	for i := len(s.completions) - 1; i >= 0; i-- {
		s.completions[i]()
	}
	s.recordExecution(finalStatus)
	s.notifyChangeLocked()
}

func (s *Session) updateFromAgentResponseLocked(response AgentSessionResponse) {
	s.status.Status = proto.ExecutionStatus_STATUS_RUNNING
	s.status.SshConnection = response.ExecutionStatus.SshConnection
	s.startTime = time.Now()
}

func (s *Session) String() string {
	str := fmt.Sprintf("Session %s", s.Request.SessionId)
	if s.Request.TaskId != "" {
		str += fmt.Sprintf(" Task %s", s.Request.TaskId)
	}
	if s.Request.ServiceName != "" {
		str += fmt.Sprintf(" Service %s", s.Request.ServiceName)
	}
	return str
}

// SessionLike compatibility methods so *Session implements the interface
// expected by the scheduler.
func (s *Session) ID() string {
	return s.id
}

func (s *Session) Priority() int64 {
	return s.priority
}

func (s *Session) AgentChan() chan *Agent {
	return s.agentChan
}

func (s *Session) CancelConfirmChan() chan struct{} {
	return s.cancelConfirm
}

func (s *Session) recordExecution(finalState proto.SessionExecutionFinalState) error {
	if s.helpers == nil {
		return fmt.Errorf("No accounting database configured for session %s", s.id)
	}
	data := &proto.SessionExecutionRecord{
		InstanceId:  s.Request.InstanceId,
		Pool:        s.Request.Pool,
		CreateTime:  timestamppb.New(s.createTime),
		EndTime:     timestamppb.New(time.Now()),
		ServiceName: s.Request.ServiceName,
		SessionId:   s.Request.SessionId,
		BatchTaskId: s.Request.TaskId,
		FinalState:  finalState,
	}
	if !s.startTime.IsZero() {
		data.StartTime = timestamppb.New(s.startTime)
	}
	if s.agent != nil {
		data.AgentId = s.agent.id
	}
	err := s.helpers.RecordExecution(data)
	if err != nil {
		log.Printf("Failed to record execution for session %s: %v", s.id, err)
	}
	return err
}

func (s *Session) notifyChangeLocked() {
	if s.helpers == nil {
		return
	}
	status := pb.Clone(s.status).(*proto.ExecutionStatus)
	s.helpers.NotifyStateChange(s.Request.InstanceId, s.id, status)
	if s.Request.TaskId != "" {
		s.helpers.NotifyTaskChange(s.Request.TaskId, status)
	}
}
