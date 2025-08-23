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
package clientlib

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/term"
)

type SshSession struct {
	// We use raw channel for our custom commands.
	channel  ssh.Channel
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	usePty   bool
	ttyFd    uintptr
	started  bool
	oldState *term.State
}

func NewSshSession(channel ssh.Channel) *SshSession {
	return &SshSession{
		channel: channel,
	}
}

func (s *SshSession) Close() error {
	if err := s.RestoreTty(); err != nil {
		log.Printf("Failed to restore tty: %v", err)
	}
	return s.channel.Close()
}

func (s *SshSession) RequestPty(force bool) error {
	var request struct {
		Term    string
		Columns uint32
		Rows    uint32
		Width   uint32
		Height  uint32
		Modes   string
	}

	termFile, ok := s.Stdin.(*os.File)
	if ok && term.IsTerminal(uintptr(termFile.Fd())) {
		s.ttyFd = uintptr(termFile.Fd())
		size, err := term.GetWinsize(s.ttyFd)
		if err != nil {
			return errors.New("failed to get terminal size")
		}
		request.Columns = uint32(size.Col)
		request.Rows = uint32(size.Row)
		request.Width = uint32(size.Xpixel)
		request.Height = uint32(size.Ypixel)
		request.Term = os.Getenv("TERM")
		if request.Term == "" {
			request.Term = "xterm"
		}
	} else {
		if !force {
			return errors.New("stdin is not a terminal")
		}
		// Provide some default config
		request.Term = "xterm"
		request.Columns = 80
		request.Rows = 24
		request.Width = 640
		request.Height = 480
	}

	ok, err := s.channel.SendRequest("pty-req", true, ssh.Marshal(&request))
	if err != nil {
		return fmt.Errorf("failed to request pty: %v", err)
	}
	if !ok {
		return errors.New("failed to request pty")
	}
	s.usePty = true
	s.oldState, err = term.MakeRaw(int(s.ttyFd))
	if err != nil {
		return fmt.Errorf("failed to make raw: %v", err)
	}
	return nil
}

func (s *SshSession) RestoreTty() error {
	if s.oldState != nil {
		oldState := s.oldState
		s.oldState = nil
		return term.Restore(int(s.ttyFd), oldState)
	}
	return nil
}

func (s *SshSession) Shell() error {
	return s.run("shell", nil)
}

func (s *SshSession) Exec(command string) error {
	var request struct {
		Command string
	}
	request.Command = command
	return s.run("exec", ssh.Marshal(&request))
}

func (s *SshSession) ExecVelda(workload *proto.Workload) error {
	payload, err := pb.Marshal(workload)
	if err != nil {
		return fmt.Errorf("failed to marshal workload: %v", err)
	}
	return s.run("exec-velda", payload)
}

func (s *SshSession) run(commandType string, payload []byte) error {
	if s.started {
		return errors.New("session already started")
	}
	s.started = true

	ok, err := s.channel.SendRequest(commandType, true, payload)
	if err != nil {
		return fmt.Errorf("failed to execute command %s: %v", commandType, err)
	}
	if !ok {
		return fmt.Errorf("failed to execute command %s: rejected by server", commandType)
	}
	if s.Stdin != nil {
		// Ignore SIGTTIN
		inFile, ok := s.Stdin.(*os.File)
		isTerminal := ok && term.IsTerminal(inFile.Fd())
		if isTerminal {
			signal.Ignore(syscall.SIGTTIN)
		}
		go func() {
			defer s.channel.CloseWrite()
			for {
				buf := make([]byte, 4096)
				n, err := s.Stdin.Read(buf)
				// Ignore input/output error, which may happen if the process is in background.
				if err != nil && isTerminal && strings.Contains(err.Error(), "input/output error") {
					time.Sleep(1000 * time.Millisecond)
					continue
				}
				if err == nil {
					_, err = s.channel.Write(buf[:n])
				}
				if err != nil {
					if err != io.EOF {
						log.Printf("StdIn Error: %v", err)
					}
					break
				}
			}
		}()
	}
	if s.Stdout != nil {
		go io.Copy(s.Stdout, s.channel)
	}
	if s.Stderr != nil {
		go io.Copy(s.Stderr, s.channel.Stderr())
	}
	go s.forwardSignals()
	if s.usePty {
		// Check window change.
		go func() {
			sigwinch := make(chan os.Signal, 1)
			signal.Notify(sigwinch, syscall.SIGWINCH)
			defer signal.Stop(sigwinch)
			for range sigwinch {
				winsize, err := term.GetWinsize(s.ttyFd)
				if err != nil {
					log.Printf("Failed to get window size: %v", err)
					continue
				}

				var payload struct {
					Cols   uint32
					Rows   uint32
					XPixel uint32
					YPixel uint32
				}
				payload.Cols = uint32(winsize.Col)
				payload.Rows = uint32(winsize.Row)
				payload.XPixel = uint32(winsize.Xpixel)
				payload.YPixel = uint32(winsize.Ypixel)

				s.channel.SendRequest("window-change", false, ssh.Marshal(&payload))
			}
		}()
	}
	return nil
}

var forwardedSignals = map[os.Signal]ssh.Signal{
	syscall.SIGABRT: ssh.SIGABRT,
	syscall.SIGALRM: ssh.SIGALRM,
	syscall.SIGFPE:  ssh.SIGFPE,
	syscall.SIGHUP:  ssh.SIGHUP,
	syscall.SIGILL:  ssh.SIGILL,
	syscall.SIGINT:  ssh.SIGINT,
	syscall.SIGKILL: ssh.SIGKILL,
	syscall.SIGPIPE: ssh.SIGPIPE,
	syscall.SIGQUIT: ssh.SIGQUIT,
	syscall.SIGSEGV: ssh.SIGSEGV,
	syscall.SIGTERM: ssh.SIGTERM,
	syscall.SIGUSR1: ssh.SIGUSR1,
	syscall.SIGUSR2: ssh.SIGUSR2,
}

func (s *SshSession) forwardSignals() {
	sig := make(chan os.Signal, 1)
	osSignals := make([]os.Signal, 0, len(forwardedSignals))
	for sig := range forwardedSignals {
		osSignals = append(osSignals, sig)
	}
	signal.Notify(sig, osSignals...)
	defer signal.Stop(sig)
	for si := range sig {
		msg := struct {
			Signal string
		}{
			Signal: string(forwardedSignals[si]),
		}
		s.channel.SendRequest("signal", false, ssh.Marshal(&msg))
	}
}
