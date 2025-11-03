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
	"testing"

	"github.com/stretchr/testify/assert"

	"velda.io/velda/pkg/proto"
)

const (
	instanceID = 1
	pool       = "cpu"

	otherInstanceID = 2
	otherPool       = "gpu"
)

func TestSessionDatabase(t *testing.T) {
	db := NewSessionDatabase(nil)

	makeRequest := func() *proto.SessionRequest {
		return &proto.SessionRequest{
			InstanceId: instanceID,
			Pool:       pool,
		}
	}
	scheduler := &Scheduler{}
	var session *Session
	t.Run("AddSession", func(t *testing.T) {
		newsession, err := db.AddSession(makeRequest(), scheduler)
		session = newsession
		assert.Nil(t, err)
		assert.NotNil(t, session)
	})
	sessionID := session.Request.SessionId

	t.Run("GetSession", func(t *testing.T) {
		s, err := db.GetSession(instanceID, sessionID)
		assert.Nil(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, s, session)
	})

	t.Run("NotExistSession", func(t *testing.T) {
		_, err := db.GetSession(instanceID, "notexist")
		assert.NotNil(t, err)
	})

	t.Run("ListSessions", func(t *testing.T) {
		sessions, err := db.ListSessions(instanceID)
		assert.Nil(t, err)
		assert.Len(t, sessions, 1)
		assert.Equal(t, sessions[0], session)
	})

	t.Run("ListSessionForOtherInstance", func(t *testing.T) {
		otherSessions, err := db.ListSessions(otherInstanceID)
		assert.Nil(t, err)
		assert.Len(t, otherSessions, 0)
	})

	t.Run("RemoveSession", func(t *testing.T) {
		err := db.RemoveSession(session)
		assert.Nil(t, err)
		s, err := db.GetSession(instanceID, sessionID)
		assert.NotNil(t, err)
		assert.Nil(t, s)
		sessions, err := db.ListSessions(instanceID)
		assert.Nil(t, err)
		assert.Len(t, sessions, 0)
	})
}

func TestSessionDatabaseBySvcName(t *testing.T) {
	db := NewSessionDatabase(nil)
	serviceName := "svc1"
	makeRequest := func() *proto.SessionRequest {
		return &proto.SessionRequest{
			InstanceId:  instanceID,
			Pool:        pool,
			ServiceName: serviceName,
		}
	}
	scheduler := &Scheduler{}

	t.Run("NotExistWithoutScheduler", func(t *testing.T) {
		_, err := db.AddSession(makeRequest(), nil, nil)
		assert.ErrorContains(t, err, "No session exists and scheduler is not provided")
	})

	var session *Session
	t.Run("AddSession", func(t *testing.T) {
		newsession, err := db.AddSession(makeRequest(), scheduler)
		session = newsession
		assert.Nil(t, err)
		assert.NotNil(t, session)
	})

	t.Run("AddSessionTwice", func(t *testing.T) {
		// Should return same session.
		// Test without scheduler since it's not needed.
		newsession, err := db.AddSession(makeRequest(), nil)
		assert.ErrorIs(t, err, AlreadyExistsError)
		assert.NotNil(t, newsession)
		assert.Equal(t, newsession, session)
	})

	var session2 *Session
	t.Run("AddSessionTwiceForceAddSession", func(t *testing.T) {
		// Should return same session.
		request := makeRequest()
		request.ForceNewSession = true

		newsession, err := db.AddSession(request, scheduler)
		assert.NoError(t, err)
		assert.NotNil(t, newsession)
		assert.NotEqual(t, newsession, session)
		session2 = newsession
	})

	t.Run("ListSessions", func(t *testing.T) {
		sessions, err := db.ListSessions(instanceID)
		assert.Nil(t, err)
		assert.Len(t, sessions, 2)
		assert.ElementsMatch(t, sessions, []*Session{session, session2})
	})

	t.Run("RemoveSession", func(t *testing.T) {
		err := db.RemoveSession(session)
		assert.Nil(t, err)
		s, err := db.GetSession(instanceID, session.Request.SessionId)
		assert.NotNil(t, err)
		assert.Nil(t, s)
		err = db.RemoveSession(session2)
		assert.Nil(t, err)
		sessions, err := db.ListSessions(instanceID)
		assert.Nil(t, err)
		assert.Len(t, sessions, 0)
	})

	t.Run("AddSessionAfterRemove", func(t *testing.T) {
		newsession, err := db.AddSession(makeRequest(), scheduler)
		assert.Nil(t, err)
		assert.NotNil(t, newsession)
		assert.NotEqual(t, newsession, session)
	})

}

func TestSessionDatabaseDisallowNewSession(t *testing.T) {
	db := NewSessionDatabase(nil)
	serviceName := "svc-test"
	sessionID := "session-test"
	makeRequest := func() *proto.SessionRequest {
		return &proto.SessionRequest{
			InstanceId:  instanceID,
			Pool:        pool,
			ServiceName: serviceName,
		}
	}
	scheduler := &Scheduler{}

	t.Run("DisallowNewSessionWithoutExisting", func(t *testing.T) {
		request := makeRequest()
		request.DisallowNewSession = true
		_, err := db.AddSession(request, scheduler)
		assert.ErrorContains(t, err, "new session creation is disallowed")
	})

	var session *Session
	t.Run("CreateSession", func(t *testing.T) {
		newsession, err := db.AddSession(makeRequest(), scheduler)
		session = newsession
		assert.Nil(t, err)
		assert.NotNil(t, session)
	})

	t.Run("DisallowNewSessionWithExistingByServiceName", func(t *testing.T) {
		request := makeRequest()
		request.DisallowNewSession = true
		newsession, err := db.AddSession(request, scheduler)
		assert.ErrorIs(t, err, AlreadyExistsError)
		assert.NotNil(t, newsession)
		assert.Equal(t, newsession, session)
	})

	t.Run("RegisterSessionByID", func(t *testing.T) {
		request := makeRequest()
		request.SessionId = sessionID
		request.ServiceName = ""
		newsession, err := db.RegisterSession(request, scheduler)
		assert.Nil(t, err)
		assert.NotNil(t, newsession)
	})

	t.Run("DisallowNewSessionWithExistingBySessionID", func(t *testing.T) {
		request := makeRequest()
		request.SessionId = sessionID
		request.ServiceName = ""
		request.DisallowNewSession = true
		newsession, err := db.AddSession(request, scheduler)
		assert.ErrorIs(t, err, AlreadyExistsError)
		assert.NotNil(t, newsession)
	})

	t.Run("DisallowNewSessionWithNonExistingSessionID", func(t *testing.T) {
		request := makeRequest()
		request.SessionId = "non-existing-session"
		request.ServiceName = ""
		request.DisallowNewSession = true
		_, err := db.AddSession(request, scheduler)
		assert.ErrorContains(t, err, "new session creation is disallowed")
	})
}
