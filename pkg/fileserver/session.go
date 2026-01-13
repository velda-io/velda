// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package fileserver

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// Session represents a client session with mount information
type Session struct {
	conn    net.Conn
	mountID int32 // Mount ID for this session
	rootFd  int   // File descriptor for root directory (for OpenByHandleAt)
}

// NewSession creates a new session
func NewSession(conn net.Conn) *Session {
	return &Session{
		conn:    conn,
		mountID: -1, // Not mounted yet
		rootFd:  -1, // Not opened yet
	}
}

// Init initializes the session with mount ID and root file descriptor
func (s *Session) Init(mountID int32, rootFd int) {
	s.mountID = mountID
	s.rootFd = rootFd
}

// Write writes data to the session
func (s *Session) Write(data []byte) (int, error) {
	return s.conn.Write(data)
}

// Read reads data from the session
func (s *Session) Read(data []byte) (int, error) {
	return s.conn.Read(data)
}

// SetReadDeadline sets read deadline
func (s *Session) SetReadDeadline(t time.Time) error {
	return s.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets write deadline
func (s *Session) SetWriteDeadline(t time.Time) error {
	return s.conn.SetWriteDeadline(t)
}

// Close closes the session
func (s *Session) Close() error {
	if s.rootFd != -1 {
		unix.Close(s.rootFd)
		s.rootFd = -1
	}
	return s.conn.Close()
}
