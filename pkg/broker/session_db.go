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
	"crypto/rand"
	"errors"
	"fmt"
	"sync"

	"velda.io/velda/pkg/proto"
)

type SessionCompletionWatcher interface {
	NotifySessionCompletion(ctx context.Context, record *proto.SessionExecutionRecord) error
}
type SessionHelper interface {
	SessionCompletionWatcher
	NotifyStateChange(instanceId int64, sessionId string, state *proto.ExecutionStatus)
	NotifyTaskChange(taskId string, state *proto.ExecutionStatus)
}

type NullCompletionWatcher struct{}

func (db *NullCompletionWatcher) NotifySessionCompletion(ctx context.Context, data *proto.SessionExecutionRecord) error {
	return nil
}

var AlreadyExistsError = errors.New("session already exists")

type SessionDatabase struct {
	mu        sync.Mutex
	instances map[int64]*Instance
	helpers   SessionHelper
}

type Instance struct {
	id       int64
	mu       sync.Mutex
	sessions map[string]*Session
	// svc name -> set<sessionId>
	services map[string]map[string]bool
}

func NewSessionDatabase(helpers SessionHelper) *SessionDatabase {
	return &SessionDatabase{
		instances: make(map[int64]*Instance),
		helpers:   helpers,
	}
}

func (db *SessionDatabase) getInstance(instanceID int64) *Instance {
	db.mu.Lock()
	defer db.mu.Unlock()
	instance, ok := db.instances[instanceID]
	if !ok {
		instance = &Instance{
			id:       instanceID,
			sessions: make(map[string]*Session),
			services: make(map[string]map[string]bool),
		}
		db.instances[instanceID] = instance
	}
	return instance
}

func (db *SessionDatabase) ListSessions(instanceID int64) ([]*Session, error) {
	instance := db.getInstance(instanceID)
	instance.mu.Lock()
	defer instance.mu.Unlock()
	sessionList := []*Session{}
	for _, value := range instance.sessions {
		sessionList = append(sessionList, value)
	}
	return sessionList, nil
}

func (db *SessionDatabase) ListSessionForService(instanceId int64, serviceName string) ([]*Session, error) {
	instance := db.getInstance(instanceId)
	instance.mu.Lock()
	defer instance.mu.Unlock()
	if sessions, ok := instance.services[serviceName]; ok {
		result := make([]*Session, 0, len(sessions))
		for id := range sessions {
			if session, ok := instance.sessions[id]; ok {
				result = append(result, session)
			}
		}
		return result, nil
	}
	return nil, nil
}

func (db *SessionDatabase) GetSession(instanceID int64, sessionID string) (*Session, error) {
	instance := db.getInstance(instanceID)
	instance.mu.Lock()
	defer instance.mu.Unlock()
	session, ok := instance.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	return session, nil
}

type SessionUpdater func(*Session) error

func (db *SessionDatabase) RegisterSession(sessionReq *proto.SessionRequest, scheduler *Scheduler, updater ...SessionUpdater) (*Session, error) {
	if sessionReq.SessionId == "" {
		return nil, fmt.Errorf("session id is required")
	}
	instanceID := sessionReq.InstanceId
	instance := db.getInstance(instanceID)

	instance.mu.Lock()
	defer instance.mu.Unlock()
	return db.registerSession(instance, sessionReq, scheduler, updater...)
}

func (db *SessionDatabase) registerSession(instance *Instance, sessionReq *proto.SessionRequest, scheduler *Scheduler, updater ...SessionUpdater) (*Session, error) {
	sessionID := sessionReq.SessionId

	session := NewSession(sessionReq, scheduler, db.helpers)
	session.AddCompletion(func() {
		db.RemoveSession(session)
	})

	if sessionReq.ServiceName != "" {
		var sessions map[string]bool
		var ok bool
		// Try to find existing session that match the service name.
		if sessions, ok = instance.services[sessionReq.ServiceName]; !ok {
			sessions = make(map[string]bool)
			instance.services[sessionReq.ServiceName] = sessions
		}
		sessions[sessionID] = true
	}
	instance.sessions[sessionID] = session
	for _, u := range updater {
		if err := u(session); err != nil {
			return session, err
		}
	}
	return session, nil
}

func (db *SessionDatabase) AddSession(sessionReq *proto.SessionRequest, scheduler *Scheduler, updater ...SessionUpdater) (*Session, error) {
	instanceID := sessionReq.InstanceId
	instance := db.getInstance(instanceID)

	instance.mu.Lock()
	defer instance.mu.Unlock()

	// Specific session is requested.
	if sessionReq.SessionId != "" {
		if session, ok := instance.sessions[sessionReq.SessionId]; ok {
			return session, AlreadyExistsError
		}
		return nil, fmt.Errorf("session not found")
	}
	if sessionReq.ServiceName != "" && !sessionReq.ForceNewSession {
		// Try to find existing session that match the service name.
		if sessions, ok := instance.services[sessionReq.ServiceName]; ok {
			for id := range sessions {
				if session, ok := instance.sessions[id]; ok {
					return session, AlreadyExistsError
				}
			}
		}
	}
	if scheduler == nil {
		return nil, fmt.Errorf("No session exists and scheduler is not provided")
	}
	sessionReq.SessionId = newSessionId()
	for _, ok := instance.sessions[sessionReq.SessionId]; ok; _, ok = instance.sessions[sessionReq.SessionId] {
		sessionReq.SessionId = newSessionId()
	}
	return db.registerSession(instance, sessionReq, scheduler, updater...)
}

func (db *SessionDatabase) RemoveSession(session *Session) error {
	instanceID := session.Request.InstanceId
	sessionID := session.Request.SessionId
	svcName := session.Request.ServiceName
	instance := db.getInstance(instanceID)
	instance.mu.Lock()
	defer instance.mu.Unlock()
	delete(instance.sessions, sessionID)
	if svcName != "" {
		delete(instance.services[svcName], sessionID)
		if len(instance.services[svcName]) == 0 {
			delete(instance.services, svcName)
		}
	}
	return nil
}

type SessionKey struct {
	InstanceId int64
	SessionId  string
}

func (k SessionKey) String() string {
	return fmt.Sprintf("%d:%s", k.InstanceId, k.SessionId)
}

func newSessionId() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
