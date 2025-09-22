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
	"io"
	"log"
	"net"
	"time"

	"google.golang.org/grpc/peer"

	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

type agentDelegate interface {
	GrantAccessToAgent(context.Context, *Agent, *Session) (rbac.User, error)
	RevokeAccessToAgent(context.Context, *Agent, *Session) error
	UpdateTaskResult(context.Context, *Session, *proto.BatchTaskResult) error
}

var ErrorFailedToStartSession = errors.New("Failed to start session, check agent logs")

type SessionRequest struct {
	session          *Session
	ctx              context.Context
	accept           bool
	holdResourceOnly bool
}

type AgentStatusResponse struct {
	Id        string
	Pool      string
	Host      net.IP
	Sessions  map[SessionKey]*proto.SessionRequest
	Receiving bool
}

type AgentStatusRequest struct {
	response chan *AgentStatusResponse
	ctx      context.Context
	// Soft shutdown request: Stop the agent if nothing is running.
	shutdown bool
	target   string
}

type Agent struct {
	id         string
	Pool       string
	Host       net.IP
	PeerInfo   *peer.Peer
	delegate   agentDelegate
	scheduler  *Scheduler
	slots      int
	mySessions map[SessionKey]*Session
	statusChan chan AgentStatusRequest
	killChan   chan *proto.KillSessionAgentRequest

	receiving bool
	stopping  bool

	sessionReq    chan SessionRequest
	cancelConfirm chan struct{}
}

func newAgent(initialReq *proto.AgentUpdateRequest, peerInfo *peer.Peer, scheduler *Scheduler, delegate agentDelegate) *Agent {
	addr, ok := peerInfo.Addr.(*net.TCPAddr)
	if !ok {
		log.Printf("Failed to get TCP address from %v", peerInfo.Addr)
		panic("Failed to get TCP address")
	}
	identity := initialReq.AgentIdentity
	concurrency := initialReq.Slots
	if concurrency <= 0 {
		concurrency = 1
	}
	return &Agent{
		id:            identity.AgentId,
		Pool:          identity.Pool,
		Host:          addr.IP,
		PeerInfo:      peerInfo,
		sessionReq:    make(chan SessionRequest, concurrency),
		slots:         int(concurrency),
		scheduler:     scheduler,
		delegate:      delegate,
		cancelConfirm: make(chan struct{}, 1),
		mySessions:    make(map[SessionKey]*Session),
		statusChan:    make(chan AgentStatusRequest, 1),
		killChan:      make(chan *proto.KillSessionAgentRequest, 1),
	}
}

func (a *Agent) Run(
	stream proto.BrokerService_AgentUpdateServer,
	serverContext context.Context) error {
	reqChan := make(chan *proto.AgentUpdateRequest)
	errChan := make(chan error, 1)
	resChan := make(chan func(), 1)
	var lastMessageTime time.Time
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				errChan <- err
				return
			}
			lastMessageTime = time.Now()
			reqChan <- req
		}
	}()
	go func() {
		for {
			if f, ok := <-resChan; ok {
				f()
			} else {
				return
			}
		}
	}()
	defer close(resChan)

	if len(a.mySessions) == 0 {
		a.scheduler.AddAgent(a)
		a.receiving = true
	}
	poolManager := a.scheduler.PoolManager
	poolManager.NotifyAgentAvailable(a.id, len(a.mySessions) > 0, a.statusChan, a.slots-len(a.mySessions))
	defer poolManager.MarkLost(a.id)

	var finalErr error
	stop := func(err error) {
		if !a.stopping {
			finalErr = err
			if a.receiving {
				a.scheduler.RemoveAgent(a)
			}
			for _, session := range a.mySessions {
				session.MarkStale(lastMessageTime)
			}
			a.stopping = true
		}
	}
	// Shut-down requested by autoscaler.
	shutdownRequested := false

	for !a.stopping || a.receiving {
		select {
		case <-stream.Context().Done():
			stop(nil)
		case resp := <-reqChan:
			if err := a.handleSessionUpdate(stream.Context(), resp); err != nil {
				stop(err)
			}
		case <-serverContext.Done():
			stop(nil)

		case <-a.cancelConfirm:
			a.receiving = false
			if shutdownRequested {
				poolManager.MarkDeleting(a.id)
				continue
			}
			if !a.stopping {
				panic("Unexpected cancel confirm")
			}
		case req := <-a.statusChan:
			if req.shutdown {
				if len(a.mySessions) > 0 {
					// Cancel the shutdown as there are still sessions running.
					poolManager.MarkBusy(a.id, a.slots-len(a.mySessions))
				} else {
					if a.receiving {
						shutdownRequested = true
						a.scheduler.RemoveAgent(a)
					} else {
						// Instruct the worker to be killed.
						//  Will let the stream termination handler to complete.
						poolManager.MarkDeleting(a.id)
					}
				}
				continue
			}
			sessions := make(map[SessionKey]*proto.SessionRequest, len(a.mySessions))
			for k, v := range a.mySessions {
				sessions[k] = v.Request
			}
			go func(sessions map[SessionKey]*proto.SessionRequest) {
				select {
				case req.response <- &AgentStatusResponse{
					Id:        a.id,
					Pool:      a.Pool,
					Host:      a.Host,
					Sessions:  sessions,
					Receiving: a.receiving,
				}:
				case <-req.ctx.Done():
				}
			}(sessions)
		case err := <-errChan:
			if errors.Is(err, io.EOF) {
				err = nil
			}
			stop(err)
		case kill := <-a.killChan:
			resChan <- func() {
				response := &proto.AgentUpdateResponse{
					KillSessionRequest: kill,
				}
				if err := stream.Send(response); err != nil {
					log.Printf("Failed to send kill request for session %s to agent %s: %v", kill.SessionId, a.id, err)
					stop(err)
				}
			}
		case reqData := <-a.sessionReq:
			a.receiving = false
			if a.stopping {
				if reqData.accept {
					req := reqData.session
					req.agentResponse <- AgentSessionResponse{
						Error: errors.New("agent is stopping"),
					}
				}
				continue
			}
			if shutdownRequested {
				if reqData.accept {
					// Cancel the shutdown as new request is received.
					shutdownRequested = false
					log.Printf("Agent %s received a new session request, canceling shutdown", a.id)
				} else {
					poolManager.MarkDeleting(a.id)
					continue
				}
			}

			if !reqData.accept {
				a.receiving = true
				a.scheduler.AddAgent(a)
				continue
			}
			req := reqData.session

			_, isExistingSession := a.mySessions[req.Key()]
			a.mySessions[req.Key()] = req
			if a.slots > len(a.mySessions) {
				a.receiving = true
				a.scheduler.AddAgent(a)
			}

			if !isExistingSession {
				poolManager.MarkBusy(a.id, a.slots-len(a.mySessions))
			}
			if reqData.holdResourceOnly {
				log.Printf("Holding resources for session %s on agent %s", req, a.id)
				continue
			}
			log.Printf("Sending session %s to agent %s", req, a.id)
			resChan <- func() {
				var err error
				response := &proto.AgentUpdateResponse{}
				req.user, err = a.delegate.GrantAccessToAgent(reqData.ctx, a, req)
				if err != nil {
					log.Printf("Failed to grant access for session %s to agent %s: %v", req.Request.SessionId, a.id, err)
					req.agentResponse <- AgentSessionResponse{
						Error: err,
					}
				}
				response.SessionRequest = req.Request
				if err := stream.Send(response); err != nil {
					log.Printf("Failed to send session request %s to agent %s", req.Request.SessionId, a.id)
					req.agentResponse <- AgentSessionResponse{
						Error: err,
					}
				}
			}
		}
	}

	return finalErr
}

func (a *Agent) RequestKill(ctx context.Context, instanceId int64, sessionId string, force bool) error {
	key := SessionKey{
		InstanceId: instanceId,
		SessionId:  sessionId,
	}
	_, ok := a.mySessions[key]
	if !ok {
		return fmt.Errorf("session %s not found on agent %s", key, a.id)
	}
	a.killChan <- &proto.KillSessionAgentRequest{
		InstanceId: instanceId,
		SessionId:  sessionId,
		Force:      force,
	}
	return nil
}

func (a *Agent) handleSessionUpdate(ctx context.Context, resp *proto.AgentUpdateRequest) error {
	// TODO: Handle multi-error.
	if resp.SessionInitResponse != nil {
		if err := a.handleSessionInitResponse(resp.SessionInitResponse); err != nil {
			return err
		}
	}
	if resp.SessionCompletion != nil {
		if err := a.handleSessionCompletion(ctx, resp.SessionCompletion); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) handleSessionInitResponse(resp *proto.SessionInitResponse) error {
	sessionKey := SessionKey{
		InstanceId: resp.InstanceId,
		SessionId:  resp.SessionId,
	}
	session, ok := a.mySessions[sessionKey]
	if !ok {
		log.Printf("Session %s not found", sessionKey)
		// TODO: Cancel the session on the agent
	} else if !resp.Success {
		log.Printf("Failed to start session %s", sessionKey)
		delete(a.mySessions, sessionKey)
		session.agentResponse <- AgentSessionResponse{
			Error: fmt.Errorf("%w: %s", ErrorFailedToStartSession, a.id),
		}
		session.Complete(proto.SessionExecutionFinalState_SESSION_EXECUTION_FINAL_STATE_STARTUP_FAILURE)
		a.scheduler.AddAgent(a)
		a.scheduler.PoolManager.MarkIdle(a.id, a.slots-len(a.mySessions))
		a.receiving = true
		return nil
	} else {
		conn := &proto.ExecutionStatus_SshConnection{
			Host:    a.Host.String(),
			Port:    resp.Port,
			HostKey: resp.HostKey,
		}
		executionStatus := &proto.ExecutionStatus{
			SessionId:     resp.SessionId,
			Status:        proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: conn,
		}
		session.agentResponse <- AgentSessionResponse{
			Error:           nil,
			ExecutionStatus: executionStatus,
		}
	}
	return nil
}

func (a *Agent) handleSessionCompletion(ctx context.Context, resp *proto.SessionCompletion) error {
	sessionKey := SessionKey{
		InstanceId: resp.InstanceId,
		SessionId:  resp.SessionId,
	}
	session, ok := a.mySessions[sessionKey]
	if !ok {
		log.Printf("Session %s not found", sessionKey)
		return nil
	}
	ctx = rbac.ContextWithUser(ctx, session.user)
	if resp.Checkpointed {
		delete(a.mySessions, sessionKey)
		session.Checkpointed()
		a.scheduler.AddAgent(a)
		a.scheduler.PoolManager.MarkIdle(a.id, 1)
		a.receiving = true
	} else {
		delete(a.mySessions, sessionKey)
		// Remove session from the broker db.
		if resp.BatchTaskResult != nil {
			if err := a.delegate.UpdateTaskResult(ctx, session, resp.BatchTaskResult); err != nil {
				// TODO: How to handle this?
				log.Printf("Failed to update task result for session %s %v", sessionKey, err)
			}
		}
		session.Complete(proto.SessionExecutionFinalState_SESSION_EXECUTION_FINAL_STATE_COMPLETE)
		a.scheduler.AddAgent(a)
		a.scheduler.PoolManager.MarkIdle(a.id, a.slots-len(a.mySessions))
		a.receiving = true
	}
	err := a.delegate.RevokeAccessToAgent(ctx, a, session)
	if err != nil {
		return fmt.Errorf("failed to revoke access to agent %s for session %s: %w", a.id, sessionKey, err)
	}
	return nil
}
