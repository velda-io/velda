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
package fileserver

import (
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Session represents a client session with mount information
type Session struct {
	conn    net.Conn
	mountID int32 // Mount ID for this session
	rootFd  int   // File descriptor for root directory (for OpenByHandleAt)
	fdMu    sync.Mutex
	fdNext  uint32
	fds     map[uint32]int
	onClose func()
}

// NewSession creates a new session
func NewSession(conn net.Conn) *Session {
	s := &Session{
		conn:    conn,
		mountID: -1, // Not mounted yet
		rootFd:  -1, // Not opened yet
		fdNext:  1,
		fds:     make(map[uint32]int),
	}

	// If this is a TCP connection, set TCP_NOTSENT_LOWAT so the kernel
	// doesn't buffer more than one chunk+header of unsent data before
	// causing writes to block until some of it is transmitted.
	if tc, ok := conn.(*net.TCPConn); ok {
		raw, err := tc.SyscallConn()
		if err != nil {
			log.Printf("failed to get SyscallConn for TCPConn: %v", err)
			return s
		}
		ctrlErr := raw.Control(func(fd uintptr) {
			lowat := ChunkSize + HeaderSize
			if err := unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, lowat); err != nil {
				log.Printf("failed to set TCP_NOTSENT_LOWAT=%d: %v", lowat, err)
			}
		})
		if ctrlErr != nil {
			log.Printf("error running Control on socket: %v", ctrlErr)
		}
	}

	return s
}

// Init initializes the session with mount ID and root file descriptor
func (s *Session) Init(mountID int32, rootFd int) {
	s.mountID = mountID
	s.rootFd = rootFd
}

func (s *Session) TrackFd(fd int) uint32 {
	s.fdMu.Lock()
	defer s.fdMu.Unlock()
	token := s.fdNext
	s.fdNext++
	s.fds[token] = fd
	return token
}

func (s *Session) LookupFd(token uint32) (int, bool) {
	s.fdMu.Lock()
	defer s.fdMu.Unlock()
	fd, ok := s.fds[token]
	return fd, ok
}

func (s *Session) CloseFd(token uint32) {
	s.fdMu.Lock()
	fd, ok := s.fds[token]
	if ok {
		delete(s.fds, token)
	}
	s.fdMu.Unlock()
	if ok {
		unix.Close(fd)
	}
}

func (s *Session) CloseAllFds() {
	s.fdMu.Lock()
	openFds := make([]int, 0, len(s.fds))
	for token, fd := range s.fds {
		openFds = append(openFds, fd)
		delete(s.fds, token)
	}
	s.fdMu.Unlock()
	for _, fd := range openFds {
		unix.Close(fd)
	}
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
	if s.onClose != nil {
		s.onClose()
		s.onClose = nil
	}
	s.CloseAllFds()
	if s.rootFd != -1 {
		unix.Close(s.rootFd)
		s.rootFd = -1
	}
	return s.conn.Close()
}
