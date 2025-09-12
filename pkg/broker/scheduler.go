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

	"github.com/emirpasic/gods/sets/treeset"
)

var UnknownPoolError = errors.New("pool not found")

type SchedulerSet struct {
	AllowCreateNewPool bool
	mu                 sync.Mutex
	agents             map[string]*Scheduler
	ctx                context.Context
}

func NewSchedulerSet(ctx context.Context) *SchedulerSet {
	return &SchedulerSet{
		agents:             make(map[string]*Scheduler),
		ctx:                ctx,
		AllowCreateNewPool: true,
	}
}

func (s *SchedulerSet) GetPool(pool string) (*Scheduler, error) {
	return s.getOrCreatePool(pool, s.AllowCreateNewPool)
}

func (s *SchedulerSet) GetOrCreatePool(pool string) (*Scheduler, error) {
	return s.getOrCreatePool(pool, true)
}

func (s *SchedulerSet) getOrCreatePool(pool string, createAllowed bool) (*Scheduler, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.agents[pool]
	if !ok {
		if createAllowed {
			p = newScheduler(s.ctx)
			if !strings.Contains(pool, ":") {
				p.SetPoolManager(NewAutoScaledPool(pool, AutoScaledPoolConfig{
					Context: s.ctx,
				}))
			}
			s.agents[pool] = p
		} else {
			return nil, fmt.Errorf("%w: %s", UnknownPoolError, pool)
		}
	}
	return p, nil
}

func (s *SchedulerSet) GetPools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, 0, len(s.agents))
	for k := range s.agents {
		// Skip all labeled subpools.
		if !strings.Contains(k, ":") {
			result = append(result, k)
		}
	}
	return result
}

type Scheduler struct {
	PoolManager *AutoScaledPool
	agents      map[string]*Agent
	sessions    treeset.Set
	ctx         context.Context

	addAgent    chan *Agent
	removeAgent chan *Agent
	// sessions are stored as an abstract type so implementations can be replaced
	addSession    chan SessionLike
	removeSession chan SessionLike
}

// SessionLike is the minimal interface a session must implement to be
// scheduled. Extracted here so the scheduler can operate on different
// implementations.
type SessionLike interface {
	ID() string
	Priority() int64
	AgentChan() chan *Agent
	CancelConfirmChan() chan struct{}
}

func sessionComparator(ap, bp interface{}) int {
	a, b := ap.(SessionLike), bp.(SessionLike)
	if a.Priority() < b.Priority() {
		return -1
	}
	if a.Priority() > b.Priority() {
		return 1
	}
	if a.ID() < b.ID() {
		return -1
	}
	if a.ID() > b.ID() {
		return 1
	}
	return 0
}

func newScheduler(ctx context.Context) *Scheduler {
	s := &Scheduler{
		agents:        make(map[string]*Agent),
		sessions:      *treeset.NewWith(sessionComparator),
		ctx:           ctx,
		addAgent:      make(chan *Agent, 1),
		removeAgent:   make(chan *Agent, 1),
		addSession:    make(chan SessionLike, 1),
		removeSession: make(chan SessionLike, 1),
	}
	go s.Run()
	return s
}

func (s *Scheduler) SetPoolManager(pm *AutoScaledPool) {
	s.PoolManager = pm
}

func (p *Scheduler) Run() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered in Scheduler.Run: %v", r)
		}
	}()
	shuttingDown := false
	for {
		if shuttingDown && len(p.agents) == 0 {
			return
		}
		if !shuttingDown && len(p.agents) > 0 && p.sessions.Size() > 0 {
			var a *Agent
			for _, v := range p.agents {
				a = v
				break
			}
			var s SessionLike
			it := p.sessions.Iterator()
			if it.First() {
				s = it.Value().(SessionLike)
			} else {
				panic(fmt.Errorf("session set is not empty (size %d) but iterator is empty", p.sessions.Size()))
			}
			p.sessions.Remove(s)
			delete(p.agents, a.id)
			s.AgentChan() <- a
		}

		cancelled := p.ctx.Done()
		if shuttingDown {
			cancelled = nil
		}
		select {
		case a := <-p.addAgent:
			p.agents[a.id] = a
		case a := <-p.removeAgent:
			if _, ok := p.agents[a.id]; !ok {
				continue
			}
			delete(p.agents, a.id)
			a.cancelConfirm <- struct{}{}
		case s := <-p.addSession:
			p.sessions.Add(s)
		case s := <-p.removeSession:
			if !p.sessions.Contains(s) {
				continue
			}
			p.sessions.Remove(s)
			s.CancelConfirmChan() <- struct{}{}
		case <-cancelled:
			shuttingDown = true
		}
	}
}

func (p *Scheduler) AddAgent(a *Agent) {
	p.addAgent <- a
}

func (p *Scheduler) RemoveAgent(a *Agent) {
	p.removeAgent <- a
}

func (p *Scheduler) AddSession(s SessionLike) {
	p.addSession <- s
}

func (p *Scheduler) RemoveSession(s SessionLike) {
	p.removeSession <- s
}
