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
	"strconv"
	"sync"
	"testing"
)

func BenchmarkScheduler(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg := &sync.WaitGroup{}
	scheduler := newScheduler(ctx)
	nAgent := 1000
	nSession := b.N
	nShard := 10
	wg.Add(nSession)
	for i := 0; i < nAgent; i++ {
		agent := &Agent{
			id:         "agent-" + strconv.Itoa(i),
			scheduler:  scheduler,
			sessionReq: make(chan SessionRequest, 1),
		}
		go func() {
			scheduler.AddAgent(agent)
			for {
				select {
				case <-scheduler.ctx.Done():
					scheduler.RemoveAgent(agent)
				case <-agent.sessionReq:
					wg.Done()
					scheduler.AddAgent(agent)
				case <-agent.cancelConfirm:
					return
				}
			}
		}()
	}
	for i := 0; i < nShard; i++ {
		for j := 0; j < nSession/nShard; j++ {
			session := &Session{
				id:        "session-" + strconv.Itoa(i*nSession/nShard+j),
				agentChan: make(chan *Agent, 1),
			}
			scheduler.AddSession(session)
			go func(s *Session) {
				for {
					select {
					case <-scheduler.ctx.Done():
						return
					case a := <-s.agentChan:
						a.sessionReq <- SessionRequest{
							session: s,
							accept:  true,
						}
					}
				}
			}(session)
		}
	}
}
