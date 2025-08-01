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
package broker

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/golang/protobuf/proto"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/testing/protocmp"

	"velda.io/velda/pkg/proto"
)

type AgentDelegate struct{}

func (d *AgentDelegate) GrantAccessToAgent(context.Context, *Agent, *Session) error {
	return nil
}

func (d *AgentDelegate) UpdateTaskResult(context.Context, *Session, *proto.BatchTaskResult) error {
	return nil
}

func makeRequest() *proto.SessionRequest {
	return &proto.SessionRequest{
		InstanceId: instanceID,
		Pool:       pool,
	}
}

func TestSessionSchedule(t *testing.T) {
	ctx := context.Background()
	scheduler := newScheduler(ctx)
	scheduler.SetPoolManager(NewAutoScaledPool(
		"pool", AutoScaledPoolConfig{Context: context.Background()}))
	sessionDb := NewSessionDatabase(nil)

	initialReq := &proto.AgentUpdateRequest{
		AgentIdentity: &proto.AgentIdentity{
			AgentId: "agent1",
			Pool:    pool,
		},
	}
	peer := &peer.Peer{
		Addr: &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 1234,
		},
	}

	delegate := &AgentDelegate{}
	mustAddSession := func(request *proto.SessionRequest) *Session {
		session, err := sessionDb.AddSession(request, scheduler)
		assert.Nil(t, err)
		return session
	}

	t.Run("ScheduleWithoutAgent", func(t *testing.T) {
		session := mustAddSession(makeRequest())
		ctx, cancel := context.WithCancel(context.Background())
		wg := sync.WaitGroup{}
		wg.Add(2)
		testFunc := func() {
			resp, err := session.Schedule(ctx)
			if err == nil {
				assert.Equal(t, proto.ExecutionStatus_STATUS_TERMINATED, resp.Status)
			} else {
				assert.ErrorIs(t, err, ctx.Err(), err)
			}
			wg.Done()
		}
		go testFunc()
		go testFunc()
		// wait for both goroutines to be blocked by ctx.
		time.Sleep(100 * time.Millisecond)
		cancel()
		wg.Wait()
		assert.Eventually(t, func() bool {
			return session.Status().Status == proto.ExecutionStatus_STATUS_TERMINATED
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("ScheduleWithAgentHandlingFailure", func(t *testing.T) {
		session := mustAddSession(makeRequest())
		defer sessionDb.RemoveSession(session)

		agent := newAgent(initialReq, peer, scheduler, delegate)
		scheduler.AddAgent(agent)

		defer scheduler.RemoveAgent(agent)
		expectedErr := errors.New("timeout")
		go func() {
			req := <-agent.sessionReq
			assert.True(t, req.accept)
			req.session.agentResponse <- AgentSessionResponse{
				Error: expectedErr,
			}
		}()

		_, err := session.Schedule(ctx)
		assert.ErrorIs(t, err, err)
	})

	t.Run("ScheduleSuccessfulWithConcurrentReq", func(t *testing.T) {
		session := mustAddSession(makeRequest())
		defer sessionDb.RemoveSession(session)
		ctx := context.Background()
		agent := newAgent(initialReq, peer, scheduler, delegate)
		scheduler.AddAgent(agent)
		defer scheduler.RemoveAgent(agent)

		status := &proto.ExecutionStatus{
			SessionId: session.Request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: &proto.ExecutionStatus_SshConnection{
				Host: agent.Host.String(),
				Port: 1234,
			},
		}

		wg := sync.WaitGroup{}
		wg.Add(2)

		go func() {
			resp, err := session.Schedule(ctx)
			assert.Nil(t, err)
			assert.Equal(t, "", cmp.Diff(status, resp, protocmp.Transform()))
			wg.Done()
		}()

		go func() {
			resp, err := session.Schedule(ctx)
			assert.Nil(t, err)
			assert.Equal(t, "", cmp.Diff(status, resp, protocmp.Transform()))
			wg.Done()
		}()

		go func() {
			session := <-agent.sessionReq
			assert.True(t, session.accept)
			time.Sleep(100 * time.Millisecond)
			session.session.agentResponse <- AgentSessionResponse{status, nil}
		}()
		wg.Wait()
	})

	t.Run("ScheduleSuccessfulWithOneRequestCancelled", func(t *testing.T) {
		session := mustAddSession(makeRequest())
		defer sessionDb.RemoveSession(session)
		ctx := context.Background()
		agent := newAgent(initialReq, peer, scheduler, delegate)
		scheduler.AddAgent(agent)
		defer scheduler.RemoveAgent(agent)

		status := &proto.ExecutionStatus{
			SessionId: session.Request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: &proto.ExecutionStatus_SshConnection{
				Host: agent.Host.String(),
				Port: 1234,
			},
		}

		wg := sync.WaitGroup{}
		wg.Add(2)
		group1 := make(chan struct{})

		go func() {
			ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			_, err := session.Schedule(ctx)
			assert.ErrorIs(t, err, ctx.Err())
			wg.Done()
			group1 <- struct{}{}
		}()

		go func() {
			resp, err := session.Schedule(ctx)
			assert.Nil(t, err)
			assert.Equal(t, "", cmp.Diff(status, resp, protocmp.Transform()))
			wg.Done()
		}()

		go func() {
			session := <-agent.sessionReq
			assert.True(t, session.accept)
			<-group1
			session.session.agentResponse <- AgentSessionResponse{status, nil}
		}()
		wg.Wait()
	})

	t.Run("ScheduleAndStale", func(t *testing.T) {
		session := mustAddSession(makeRequest())

		status := &proto.ExecutionStatus{
			SessionId: session.Request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: &proto.ExecutionStatus_SshConnection{
				Host: "127.0.0.1",
				Port: 1234,
			},
		}
		session.status = status
		session.staleTimeout = 100 * time.Millisecond

		session.MarkStale(time.Now())
		assert.Eventually(t, func() bool {
			return session.Status().Status == proto.ExecutionStatus_STATUS_TERMINATED
		}, 2*time.Second, 10*time.Millisecond)
		session2 := mustAddSession(makeRequest())
		defer sessionDb.RemoveSession(session2)
		assert.NotEqual(t, session, session2)
	})

	t.Run("StaleAndReconnect", func(t *testing.T) {
		session := mustAddSession(makeRequest())
		defer sessionDb.RemoveSession(session)

		status := &proto.ExecutionStatus{
			SessionId: session.Request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: &proto.ExecutionStatus_SshConnection{
				Host: "127.0.0.1",
				Port: 1234,
			},
		}
		session.status = pb.Clone(status).(*proto.ExecutionStatus)
		session.staleTimeout = 100 * time.Hour

		session.MarkStale(time.Now())

		done := make(chan struct{})
		go func() {
			// Request to reconnect
			resp, err := session.Schedule(ctx)
			assert.Nil(t, err)
			assert.Equal(t, "", cmp.Diff(status, resp, protocmp.Transform()))
			close(done)
		}()
		time.Sleep(100 * time.Millisecond)
		session.Reconnect()
		<-done
	})

	t.Run("StaleAndRestartSession", func(t *testing.T) {
		session := mustAddSession(makeRequest())

		status := &proto.ExecutionStatus{
			SessionId: session.Request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: &proto.ExecutionStatus_SshConnection{
				Host: "127.0.0.2",
				Port: 1234,
			},
		}
		session.status = pb.Clone(status).(*proto.ExecutionStatus)
		session.staleTimeout = 100 * time.Millisecond

		session.MarkStale(time.Now())

		agent := newAgent(initialReq, peer, scheduler, delegate)

		scheduler.AddAgent(agent)
		defer scheduler.RemoveAgent(agent)

		expectedStatus := &proto.ExecutionStatus{
			SessionId: session.Request.SessionId,
			Status:    proto.ExecutionStatus_STATUS_RUNNING,
			SshConnection: &proto.ExecutionStatus_SshConnection{
				Host: agent.Host.String(),
				Port: 1234,
			},
		}
		go func() {
			session := <-agent.sessionReq
			assert.True(t, session.accept)
			session.session.agentResponse <- AgentSessionResponse{expectedStatus, nil}
		}()
		// Reconnect to new agent.
		resp, err := session.Schedule(ctx)
		assert.Nil(t, err)
		assert.Equal(t, "", cmp.Diff(expectedStatus, resp, protocmp.Transform()))
	})

	t.Run("ScheduleWithPriority", func(t *testing.T) {
		ids := make([]string, 0, 5)
		priority := []int64{3, 4, 1, 2, 0}
		for i := 0; i < 5; i++ {
			request := &proto.SessionRequest{
				InstanceId: instanceID,
				Pool:       pool,
				Priority:   priority[i],
			}
			session := mustAddSession(request)
			defer sessionDb.RemoveSession(session)
			ids = append(ids, session.Request.SessionId)
			go session.Schedule(ctx)
		}

		time.Sleep(100 * time.Millisecond)

		receivedIds := make([]string, 0, 5)

		for i := 0; i < 5; i++ {
			agent := newAgent(initialReq, peer, scheduler, delegate)
			scheduler.AddAgent(agent)
			defer scheduler.RemoveAgent(agent)
			session := <-agent.sessionReq
			assert.True(t, session.accept)
			receivedIds = append(receivedIds, session.session.Request.SessionId)
			session.session.agentResponse <- AgentSessionResponse{
				&proto.ExecutionStatus{
					SessionId: session.session.Request.SessionId,
					Status:    proto.ExecutionStatus_STATUS_RUNNING,
					SshConnection: &proto.ExecutionStatus_SshConnection{
						Host: agent.Host.String(),
						Port: 1234,
					},
				},
				nil,
			}
		}
		assert.Equal(t, []string{
			ids[4], ids[2], ids[3], ids[0], ids[1],
		}, receivedIds)
	})
}
