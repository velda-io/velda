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
package agentd

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/google/uuid"

	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
	"velda.io/velda/pkg/utils"
)

type SessionKey utils.SessionKey

type Agent struct {
	pool         string
	id           string
	runner       *Runner
	daemonConfig *agentpb.DaemonConfig
	brokerClient proto.BrokerServiceClient
	sessions     map[SessionKey]*proto.CurrentExecution
	completion   chan *SessionCompletion
}

func NewAgent(brokerClient proto.BrokerServiceClient,
	runner *Runner,
	pool string,
	daemonConfig *agentpb.DaemonConfig) *Agent {
	if daemonConfig == nil {
		daemonConfig = &agentpb.DaemonConfig{}
	}
	agentId := os.Getenv("AGENT_NAME")
	if agentId == "" {
		hostname, err := os.Hostname()

		shortHostname := strings.SplitN(hostname, ".", 2)[0]
		if err != nil {
			agentId = uuid.New().String()
		} else {
			agentId = shortHostname
		}
	}
	if daemonConfig.MaxSessions <= 0 {
		daemonConfig.MaxSessions = 1
	}
	return &Agent{
		pool:         pool,
		id:           agentId,
		runner:       runner,
		brokerClient: brokerClient,
		daemonConfig: daemonConfig,
		sessions:     make(map[SessionKey]*proto.CurrentExecution),
		completion:   make(chan *SessionCompletion, 3),
	}
}

func (a *Agent) Run(ctx context.Context) error {
	op := func() (struct{}, error) {
		return struct{}{}, a.run(ctx)
	}
	notifier := func(err error, duration time.Duration) {
		log.Printf("Agent %s failed: %v. Retrying in %v", a.id, err, duration)
	}
	_, err := backoff.Retry(
		ctx,
		op,
		backoff.WithBackOff(backoff.NewExponentialBackOff()),
		backoff.WithNotify(notifier),
		// No limit on retries
		backoff.WithMaxElapsedTime(time.Hour*24*365),
		backoff.WithMaxTries(0),
	)
	return err
}

func (a *Agent) run(ctx context.Context) error {
	if ctx.Err() != nil && len(a.sessions) == 0 {
		log.Printf("Agent %s stopped with no running sessions. Exiting", a.id)
		return nil
	}
	identity, err := a.getIdentity()
	if err != nil {
		return err
	}
	msgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := a.brokerClient.AgentUpdate(msgCtx)
	if err != nil {
		return err
	}
	defer stream.CloseSend()
	initialRequest := &proto.AgentUpdateRequest{
		AgentIdentity: identity,
		Slots:         a.daemonConfig.MaxSessions,
	}
	for _, session := range a.sessions {
		initialRequest.CurrentExecutions = append(initialRequest.CurrentExecutions, session)
	}
	if err := stream.Send(initialRequest); err != nil {
		return err
	}
	chanReq := make(chan *proto.AgentUpdateResponse)
	chanReqErr := make(chan error)
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				chanReqErr <- err
				return
			}
			chanReq <- req
		}
	}()
	stopped := false
	log.Printf("Agent %s connected to server", a.id)
	for {
		cancelSig := ctx.Done()
		if stopped {
			cancelSig = nil
		}
		select {
		case <-cancelSig:
			stopped = true
			log.Printf("Agent %s stopping", a.id)
			if len(a.sessions) == 0 {
				stream.CloseSend()
			}
			for key := range a.sessions {
				a.runner.Cleanup(key)
			}
			// Still wait until all sessions are completed
		case resp := <-chanReq:
			req := &proto.AgentUpdateRequest{}
			if resp.SessionRequest != nil {
				session := resp.SessionRequest
				var resp *proto.SessionInitResponse
				if stopped {
					log.Printf("Received session request %s after agent stopped, ignoring", session.SessionId)
					resp = &proto.SessionInitResponse{
						InstanceId: session.InstanceId,
						SessionId:  session.SessionId,
						Success:    false,
					}
				} else {
					resp, err = a.runner.Run(a.id, session, a.completion)
					if err != nil {
						log.Printf("Failed to run session %s: %v", session.SessionId, err)
						resp = &proto.SessionInitResponse{
							InstanceId: session.InstanceId,
							SessionId:  session.SessionId,
							Success:    false,
						}
					} else {
						resp.Success = true
						log.Printf("Requested session %s", session.SessionId)
						key := SessionKey{
							InstanceId: session.InstanceId,
							SessionId:  session.SessionId,
						}
						a.sessions[key] = &proto.CurrentExecution{
							Request:  session,
							Response: resp,
						}
					}
				}
				req.SessionInitResponse = resp
				if err := stream.Send(req); err != nil {
					return err
				}
			}
			if resp.KillSessionRequest != nil {
				killReq := resp.KillSessionRequest
				sessionKey := SessionKey{
					InstanceId: killReq.InstanceId,
					SessionId:  killReq.SessionId,
				}
				log.Printf("Received kill request for session %v", sessionKey)
				a.runner.Kill(sessionKey, killReq.Force)
			}
		case err := <-chanReqErr:
			if errors.Is(err, io.EOF) {
				log.Printf("Server closed connection")
				if stopped && len(a.sessions) == 0 {
					return nil
				}
				// Reset backoff to 1s after any successful connection
				return backoff.RetryAfter(1)
			}
			log.Printf("Error receiving request: %v", err)
			return err
		case comp := <-a.completion:
			req := &proto.AgentUpdateRequest{
				SessionCompletion: &proto.SessionCompletion{
					InstanceId:      comp.InstanceId,
					SessionId:       comp.SessionId,
					BatchTaskResult: comp.BatchResult,
					Checkpointed:    comp.Checkpointed,
				},
			}
			delete(a.sessions, SessionKey{
				InstanceId: comp.InstanceId,
				SessionId:  comp.SessionId,
			})
			if comp.Error != nil {
				log.Printf("Session %s completed with error: %v", comp.SessionId, comp.Error)
			}
			log.Printf("Session %s completed", comp.SessionId)
			if err := stream.Send(req); err != nil {
				return err
			}
			if stopped && len(a.sessions) == 0 {
				log.Printf("All sessions completed, closing stream")
				stream.CloseSend()
			}
		case <-time.After(30 * time.Second):
			// Heartbeat
			if err := stream.Send(&proto.AgentUpdateRequest{}); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) getIdentity() (*proto.AgentIdentity, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	return &proto.AgentIdentity{
		AgentId:  a.id,
		Hostname: hostname,
		Pool:     a.pool,
	}, nil
}
