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
package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"

	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/term"
	"velda.io/velda/pkg/utils"
)

// Similar to os.user, however Shell is not available in os/user.User
type User struct {
	UserName   string
	Password   string
	Uid        string
	Gid        string
	Name       string
	HomeDir    string
	Shell      string
	Credential *syscall.Credential
}

func (u *User) String() string {
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s:%s", u.UserName, u.Password, u.Uid, u.Gid, u.Name, u.HomeDir, u.Shell)
}

func (u *User) FromString(s string) error {
	items := strings.Split(strings.Trim(s, "\n"), ":")
	if len(items) != 7 {
		return fmt.Errorf("Invalid user info: %s", s)
	}
	u.UserName = items[0]
	u.Password = items[1]
	u.Uid = items[2]
	u.Gid = items[3]
	u.Name = items[4]
	u.HomeDir = items[5]
	u.Shell = items[6]
	return nil
}

type SshdAuthenticator interface {
	GetConfig() (*ssh.ServerConfig, error)
}

type SSHD struct {
	authenticator            SshdAuthenticator
	UserId                   int64
	hostPrivateKey           *ecdsa.PrivateKey
	listener                 net.Listener
	HostPublicKey            ssh.PublicKey
	IdleTimeout              time.Duration
	AgentName                string
	InitialConnectionTimeout time.Duration
	idleTimer                *time.Timer
	waiter                   *Waiter
	AppArmorProfile          string
	OnShutdown               func()
	CommandModifier          func(*exec.Cmd) *exec.Cmd

	OnIdle             proto.SessionRequest_ConnectionFinishAction
	connections        map[*ssh.ServerConn]struct{}
	mu                 sync.Mutex
	childProcessWaiter chan struct{}
}

func NewSSHD(authenticator SshdAuthenticator, waiter *Waiter) *SSHD {
	return &SSHD{
		authenticator: authenticator,
		connections:   make(map[*ssh.ServerConn]struct{}),
		waiter:        waiter,
	}
}

func (s *SSHD) Start() (net.Addr, error) {
	sshConfig, err := s.authenticator.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("Failed to get SSH config: %w", err)
	}
	if s.OnShutdown != nil {
		s.idleTimer = time.AfterFunc(s.InitialConnectionTimeout, s.OnShutdown)
	}
	hostPrivateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("Failed to generate host key: %w", err)
	}
	s.hostPrivateKey = hostPrivateKey
	signer, err := ssh.NewSignerFromKey(hostPrivateKey)

	if err != nil {
		return nil, fmt.Errorf("Failed to create signer: %w", err)
	}
	sshConfig.AddHostKey(signer)

	publicKey, err := ssh.NewPublicKey(&hostPrivateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to create public key: %w", err)
	}
	s.HostPublicKey = publicKey

	listener, err := net.Listen("tcp", ":2222")
	if errors.Is(err, syscall.EADDRINUSE) {
		listener, err = net.Listen("tcp", ":")
		log.Printf("Port 2222 is in use, using random port: %v", listener.Addr())
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to listen: %w", err)
	}

	s.listener = listener
	go s.waiter.Run()
	go func() {
		for {
			conn, err := listener.Accept()
			// abort if err is closed
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if err != nil {
				log.Printf("failed to accept incoming connection: %v", err)
				continue
			}

			// Perform SSH handshake
			sshConn, chans, reqs, err := ssh.NewServerConn(conn, sshConfig)
			if err != nil {
				log.Printf("failed to handshake: %v", err)
				continue
			}
			user, err := s.lookupUser(sshConn.User())
			if err != nil {
				log.Printf("failed to lookup user: %v", err)
				sshConn.Close()
				continue
			}

			func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				s.connections[sshConn] = struct{}{}
				if s.childProcessWaiter != nil {
					close(s.childProcessWaiter)
					s.childProcessWaiter = nil
				}
				s.idleTimer.Stop()
			}()
			log.Printf("new SSH connection from %s (%s)", sshConn.RemoteAddr(), sshConn.ClientVersion())

			// Discard global requests
			go ssh.DiscardRequests(reqs)

			// Handle channels
			go s.handleChannels(chans, user)

			// TODO: Add heartbeats

			go func() {
				err := sshConn.Wait()
				s.mu.Lock()
				defer s.mu.Unlock()
				delete(s.connections, sshConn)
				log.Printf("Connection from %s closed with err %v. Active connections: %d", sshConn.RemoteAddr(), err, len(s.connections))
				if s.idleTimer != nil && len(s.connections) == 0 {
					log.Printf("No more connections, starting idle timer")
					if s.OnIdle == proto.SessionRequest_CONNECTION_FINISH_ACTION_KEEP_ALIVE {
						s.waitForChildren()
					} else if s.OnIdle == proto.SessionRequest_CONNECTION_FINISH_ACTION_CHECKPOINT {
						w := make(chan struct{})
						go func() {
							s.waitForChildren()
							close(w)
						}()
						select {
						case <-w:
						case <-time.After(s.IdleTimeout):
							err := s.checkpointSelf(context.Background())
							if err != nil {
								log.Printf("Failed to checkpoint: %v", err)
							}
							<-w
						}
					} else {
						s.idleTimer.Reset(s.IdleTimeout)
					}
				}
			}()
		}
	}()
	return listener.Addr(), nil
}

func (s *SSHD) Shutdown(message string) {
	s.listener.Close()
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for conn := range s.connections {
			conn.SendRequest("close@velda", false, ssh.Marshal(struct{ Message string }{Message: message}))
			//conn.Close()
		}
	}()
}

func (s *SSHD) lookupUser(username string) (*User, error) {
	user, err := s.lookupUserInfo(username)
	if err != nil {
		return nil, err
	}

	groupCmd := exec.Command("id", "-G", username)
	output, err := groupCmd.Output()
	var groups []uint32
	if err == nil {
		groupstr := strings.Split(strings.Trim(string(output), "\n"), " ")
		for _, g := range groupstr {
			gid, err := strconv.Atoi(g)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse group id: %w", err)
			}
			groups = append(groups, uint32(gid))
		}
	} else {
		// Try to parse /etc/group
		groupfile, err := os.ReadFile("/etc/group")
		if err != nil {
			return nil, fmt.Errorf("Failed to read /etc/group: %w", err)
		}
		groupstr := strings.Split(string(groupfile), "\n")
		for _, g := range groupstr {
			componenets := strings.Split(g, ":")
			if len(componenets) < 4 {
				log.Printf("Invalid group entry: %s", g)
				continue
			}
			members := strings.Split(componenets[3], ",")
			for _, m := range members {
				if m == username {
					groupId, err := strconv.Atoi(componenets[2])
					if err != nil {
						return nil, fmt.Errorf("Failed to parse group id: %w", err)
					}
					groups = append(groups, uint32(groupId))
					break
				}
			}
		}
	}
	uid, err := strconv.Atoi(user.Uid)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse user id: %w", err)
	}
	gid, err := strconv.Atoi(user.Gid)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse group id: %w", err)
	}
	if len(groups) == 0 {
		groups = append(groups, uint32(gid))
	}
	user.Credential = &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    uint32(gid),
		Groups: groups,
	}
	return user, nil
}

func (s *SSHD) lookupUserInfo(username string) (*User, error) {
	// First, try to use "getent" command to lookup the user.
	user := &User{}
	cmd := exec.Command("getent", "passwd", username)
	output, err := cmd.Output()
	if err == nil {
		if err := user.FromString(string(output)); err != nil {
			log.Printf("Failed to parse user info from getent: %v", err)
		} else {
			return user, nil
		}
	}

	// If getent failed, tyr to parse from "/etc/passwd".
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, fmt.Errorf("Failed to open /etc/passwd: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Failed to read /etc/passwd: %w", err)
		}
		if strings.HasPrefix(line, username+":") {
			if err := user.FromString(line); err != nil {
				return nil, fmt.Errorf("Failed to parse user info: %w", err)
			}
			return user, nil
		}
	}

	return nil, fmt.Errorf("User not found: %s", username)
}

func (s *sshSession) loadDefaultEnv() []string {
	envfile, err := os.ReadFile("/etc/environment")
	result := []string{}
	if err != nil {
		return result
	}

	lines := strings.Split(string(envfile), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.Trim(parts[1], "\"")
		result = append(result, fmt.Sprintf("%s=%s", parts[0], value))
	}
	return result
}

func (s *SSHD) handleChannels(chans <-chan ssh.NewChannel, user *User) {
	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "session":
			session := &sshSession{
				user:            user,
				AppArmorProfile: s.AppArmorProfile,
				waiter:          s.waiter,
				agentName:       s.AgentName,
				CommandModifier: s.CommandModifier}
			go session.handleSessionChannel(newChannel)
		case "direct-tcpip":
			go s.handleDirectTCPIP(newChannel)
		default:
			log.Printf("Unknown channel type: %s", newChannel.ChannelType())
			newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

type sshSession struct {
	user         *User
	requestedPTY bool
	stdin        io.WriteCloser
	stdout       io.Reader
	stderr       io.Reader
	waiter       *Waiter
	agentName    string

	masterPty *os.File
	slavePty  *os.File

	AppArmorProfile string

	command *exec.Cmd

	envVars         map[string]string
	execCompletion  chan *ProcessState
	CommandModifier func(*exec.Cmd) *exec.Cmd
}

func (s *sshSession) handleSessionChannel(newChannel ssh.NewChannel) {
	// Accept the channel
	channel, requests, err := newChannel.Accept()
	s.envVars = make(map[string]string)
	s.envVars["AGENT_NAME"] = s.agentName
	if s.user.UserName != "root" {
		s.envVars["USER"] = s.user.UserName
		s.envVars["LOGNAME"] = s.user.UserName
		s.envVars["HOME"] = s.user.HomeDir
		s.envVars["SHELL"] = s.user.Shell
	}

	s.execCompletion = make(chan *ProcessState)
	if err != nil {
		log.Printf("Failed to accept session channel: %v", err)
		return
	}
	defer channel.Close()
	defer s.Close()

	// Handle requests like "shell", "exec", "subsystem"
	for {
		select {
		case req, ok := <-requests:
			if !ok {
				log.Printf("Channel requests closed")
				if s.command != nil {
					// Cancel waiter. Treat it as Zombie process.
					select {
					case <-s.execCompletion:
					case s.waiter.lockRequest <- struct{}{}:
						l := s.waiter.waitRequest
						l <- &WaitRequest{
							Pid: s.command.Process.Pid,
						}
					}
				}
				return
			}
			if req == nil {
				log.Printf("nil request")
			}
			switch req.Type {
			case "shell":
				s.handleShell(channel, req)
			case "exec":
				s.handleExec(channel, req)
			case "exec-velda":
				fallthrough
			case "exec-nova":
				s.handleVeldaExec(channel, req)
			case "pty-req":
				s.handlePTYRequest(req)
			case "window-change":
				s.handleWindowChangeRequest(req)
			case "env":
				s.handleEnvRequest(req)
			case "subsystem":
				s.handleSubsystem(channel, req)
			case "signal":
				s.handleSignalRequest(req)
			default:
				log.Printf("Unknown request type: %s", req.Type)
				req.Reply(false, nil)
			}
		case state := <-s.execCompletion:
			s.handleProcessCompletion(channel, state)
			return
		}
	}
}

func (s *sshSession) handleProcessCompletion(channel ssh.Channel, state *ProcessState) {
	var exitStatus int
	waitStatus := state.WaitStatus
	if waitStatus.Signaled() {
		signalInfo := struct {
			Signal string
			Core   bool
			Msg    string
			Lang   string
		}{
			Signal: waitStatus.Signal().String(),
			Core:   waitStatus.CoreDump(),
			Msg:    waitStatus.Signal().String(),
			Lang:   "en",
		}
		log.Printf("Command execution terminated by signal: %v", signalInfo.Signal)
		_, err := channel.SendRequest("exit-signal", false, ssh.Marshal(signalInfo))
		if err != nil {
			log.Printf("Failed to send exit signal: %v", err)
		}
		return
	} else if waitStatus.Exited() {
		exitStatus = waitStatus.ExitStatus()
	} else {
		log.Printf("Unexpected exit status: %v", waitStatus)
		exitStatus = 255
	}
	log.Printf("Command execution finished with exit status: %d", exitStatus)
	_, err := channel.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{C: uint32(exitStatus)}))
	if err != nil {
		log.Printf("Failed to send exit status: %v", err)
	}
	// The process should already be released, but still
	// invoke Wait to cleanup other resources.
	s.command.Wait()
	s.command = nil
}

func (s *sshSession) Close() {
	if s.masterPty != nil {
		s.masterPty.Close()
	}
	if s.slavePty != nil {
		s.slavePty.Close()
	}
}

func (s *sshSession) handlePTYRequest(req *ssh.Request) {
	// Parse the pty-req payload (format: terminal type, dimensions, etc.)
	var payload struct {
		Term   string
		Cols   uint32
		Rows   uint32
		XPixel uint32
		YPixel uint32
		Modes  string
	}
	if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
		log.Printf("Failed to parse pty-req payload: %v", err)
		req.Reply(false, nil)
		return
	}

	master, slave, err := term.OpenPTY()
	if err != nil {
		log.Printf("Failed to open PTY: %v", err)
		req.Reply(false, nil)
		return
	}

	// ioctl to set window size
	if err := term.SetWinsize(slave.Fd(), int(payload.Cols), int(payload.Rows), int(payload.XPixel), int(payload.YPixel)); err != nil {
		log.Printf("Failed to set window size: %v", err)
	}

	s.envVars["TERM"] = payload.Term
	req.Reply(true, nil)
	s.requestedPTY = true
	s.masterPty = master
	s.slavePty = slave
}

func (s *sshSession) handleWindowChangeRequest(req *ssh.Request) {
	// Parse the window-change payload (format: [cols][rows][width][height])
	var payload struct {
		Cols   uint32
		Rows   uint32
		XPixel uint32
		YPixel uint32
	}
	if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
		log.Printf("Failed to parse window-change payload: %v", err)
		req.Reply(false, nil)
	}
	if s.slavePty == nil {
		log.Printf("Window change requested without a PTY")
		req.Reply(false, nil)
		return
	}
	if err := term.SetWinsize(s.slavePty.Fd(), int(payload.Cols), int(payload.Rows), int(payload.XPixel), int(payload.YPixel)); err != nil {
		log.Printf("Failed to set window size: %v", err)
		req.Reply(false, nil)
		return
	}
	log.Printf("Window size changed: Cols=%d Rows=%d", payload.Cols, payload.Rows)
	req.Reply(true, nil)
}

func (s *sshSession) handleEnvRequest(req *ssh.Request) {
	// Parse the env request payload (format: [name_length][name][value_length][value])
	var payload struct {
		Name  string
		Value string
	}
	if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
		log.Printf("Failed to parse env request payload: %v", err)
		req.Reply(false, nil)
	}

	log.Printf("Environment variable set: %s=%s", payload.Name, payload.Value)
	s.envVars[payload.Name] = payload.Value
	req.Reply(true, nil)
}

func (s *sshSession) handleShell(channel ssh.Channel, req *ssh.Request) {
	// Prepare the command
	cmd := exec.Command(s.user.Shell)
	// Make it a login shell.
	cmd.Args = []string{"-" + path.Base(s.user.Shell)}
	cmd.Env = s.loadDefaultEnv()
	cmd.Dir = s.user.HomeDir
	for k, v := range s.envVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: s.user.Credential,
	}
	s.handleCommand(channel, req, cmd)
}

func (s *sshSession) handleVeldaExec(channel ssh.Channel, req *ssh.Request) {
	workload := &proto.Workload{}
	if err := pb.Unmarshal(req.Payload, workload); err != nil {
		log.Printf("Failed to parse velda exec request: %v", err)
		req.Reply(false, nil)
	}
	var command *exec.Cmd
	if workload.Command == "" {
		// Login shell.
		command = exec.Command(s.user.Shell)
		command.Args = []string{"-" + path.Base(s.user.Shell)}
	} else if workload.Shell {
		if len(workload.Args) > 0 {
			log.Printf("Ignoring arguments for shell command: %s %v", workload.Command, workload.Args)
		}
		command = exec.Command(s.user.Shell, "-c", workload.Command)
	} else {
		command = &exec.Cmd{
			Path: workload.Command,
			Args: append([]string{workload.Command}, workload.Args...),
		}
	}
	if workload.CommandPath != "" {
		command.Path = workload.CommandPath
	}
	command.Env = workload.Environs
	command.Env = append(command.Env, fmt.Sprintf("AGENT_NAME=%s", s.agentName))
	command.Dir = workload.WorkingDir

	command.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    workload.Uid,
			Gid:    workload.Gid,
			Groups: workload.Groups,
		},
	}
	s.handleCommand(channel, req, command)
}

func (s *sshSession) handleExec(channel ssh.Channel, req *ssh.Request) {
	// Parse the command from the payload
	var payload struct {
		Command string
	}
	if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
		log.Printf("Failed to parse exec request: %v", err)
		req.Reply(false, nil)
	}

	cmd := payload.Command

	log.Printf("Command execution requested: %s", cmd)
	// Execute the command
	command := exec.Command(s.user.Shell, "-c", cmd)
	command.Env = s.loadDefaultEnv()
	command.Dir = s.user.HomeDir
	for k, v := range s.envVars {
		command.Env = append(command.Env, fmt.Sprintf("%s=%s", k, v))
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		Credential: s.user.Credential,
	}
	s.handleCommand(channel, req, command)
}

func (s *sshSession) handleSftpSubsystem(channel ssh.Channel, req *ssh.Request) {
	// This is the fixed path when mounted in the sandbox.
	executable := "/run/velda/velda"
	cmd := exec.Command(executable, "agent", "sftp")
	// Make it a login shell.
	cmd.Env = s.loadDefaultEnv()
	cmd.Dir = s.user.HomeDir
	for k, v := range s.envVars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: s.user.Credential,
	}
	s.handleCommand(channel, req, cmd)
}

func (s *sshSession) handleCommand(channel ssh.Channel, req *ssh.Request, command *exec.Cmd) {
	if s.CommandModifier != nil {
		command = s.CommandModifier(command)
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Setsid = true
	// Set up PTY if requested
	if s.requestedPTY {
		command.Stdin = s.slavePty
		command.Stdout = s.slavePty
		command.Stderr = s.slavePty

		command.SysProcAttr.Setctty = true
		command.SysProcAttr.Setsid = true

		go io.Copy(channel, s.masterPty)
		go io.Copy(s.masterPty, channel)
	} else {
		stdin, err := command.StdinPipe()
		if err != nil {
			log.Printf("Failed to get stdin pipe: %v", err)
		} else {
			go func() {
				defer stdin.Close()
				io.Copy(stdin, channel)
			}()
		}

		stdout, err := command.StdoutPipe()
		if err != nil {
			log.Printf("Failed to get stdout pipe: %v", err)
		} else {
			go func() {
				defer channel.CloseWrite()
				io.Copy(channel, stdout)
			}()
		}

		stderr, err := command.StderrPipe()
		if err != nil {
			log.Printf("Failed to get stderr pipe: %v", err)
		} else {
			go io.Copy(channel.Stderr(), stderr)
		}
	}

	wait := s.waiter.AcquireLock()
	if err := command.Start(); err != nil {
		wait <- nil
		log.Printf("Failed to start command: %v", err)
		req.Reply(false, nil)
		return
	}
	s.command = command
	wait <- &WaitRequest{
		Pid:   command.Process.Pid,
		State: s.execCompletion,
	}
	req.Reply(true, nil)
}

func (s *sshSession) handleSubsystem(channel ssh.Channel, req *ssh.Request) {
	var request struct {
		Subsystem string
	}
	if err := ssh.Unmarshal(req.Payload, &request); err != nil {
		log.Printf("Failed to parse subsystem request: %v", err)
		req.Reply(false, nil)
		return
	}
	if request.Subsystem == "sftp" {
		s.handleSftpSubsystem(channel, req)
	} else {
		log.Printf("Unknown subsystem: %s", request.Subsystem)
		req.Reply(false, nil)
	}
}

var signalMap = map[ssh.Signal]syscall.Signal{
	ssh.SIGABRT: syscall.SIGABRT,
	ssh.SIGALRM: syscall.SIGALRM,
	ssh.SIGFPE:  syscall.SIGFPE,
	ssh.SIGHUP:  syscall.SIGHUP,
	ssh.SIGILL:  syscall.SIGILL,
	ssh.SIGINT:  syscall.SIGINT,
	ssh.SIGKILL: syscall.SIGKILL,
	ssh.SIGPIPE: syscall.SIGPIPE,
	ssh.SIGQUIT: syscall.SIGQUIT,
	ssh.SIGSEGV: syscall.SIGSEGV,
	ssh.SIGTERM: syscall.SIGTERM,
	ssh.SIGUSR1: syscall.SIGUSR1,
	ssh.SIGUSR2: syscall.SIGUSR2,
}

func (s *sshSession) handleSignalRequest(req *ssh.Request) {
	signalMsg := struct {
		Signal string
	}{}
	if err := ssh.Unmarshal(req.Payload, &signalMsg); err != nil {
		log.Printf("Failed to parse signal request: %v", err)
		return
	}
	if s.command == nil {
		log.Printf("No command to send signal to")
		return
	}
	signal, ok := signalMap[ssh.Signal(signalMsg.Signal)]
	if !ok {
		log.Printf("Unknown signal: %s", signalMsg.Signal)
		return
	}
	if err := s.command.Process.Signal(signal); err != nil {
		log.Printf("Failed to send signal: %v", err)
	}
}

func (s *SSHD) handleDirectTCPIP(newChannel ssh.NewChannel) {
	// Parse the payload
	var payload struct {
		DestinationHost string
		DestinationPort uint32
		OriginatorHost  string
		OriginatorPort  uint32
	}
	if err := ssh.Unmarshal(newChannel.ExtraData(), &payload); err != nil {
		log.Printf("Failed to parse direct-tcpip payload: %v", err)
		newChannel.Reject(ssh.ConnectionFailed, "invalid payload")
		return
	}

	log.Printf("direct-tcpip request: %s:%d -> %s:%d",
		payload.OriginatorHost, payload.OriginatorPort,
		payload.DestinationHost, payload.DestinationPort)

	// Connect to the destination
	target := fmt.Sprintf("%s:%d", payload.DestinationHost, payload.DestinationPort)
	targetConn, err := net.Dial("tcp", target)
	if err != nil {
		log.Printf("Failed to connect to target: %v", err)
		newChannel.Reject(ssh.ConnectionFailed, "connection failed")
		return
	}
	defer targetConn.Close()

	// Accept the channel
	channel, _, err := newChannel.Accept()
	if err != nil {
		log.Printf("Failed to accept channel: %v", err)
		return
	}

	utils.CopyConnections(channel, targetConn.(*net.TCPConn))
}

func (s *SSHD) Stop() error {
	if s.listener != nil {
		s.listener.Close()
	}
	return nil
}

func (s *SSHD) waitForChildren() {
	l := s.waiter.AcquireLock()
	for {
		if !s.waiter.HasChildProcess() {
			log.Printf("No child processes to wait for")
			l <- nil
			break
		}
		// Another goroutine might have already set the waiter. This should not happen
		// given it's guarded by s.mu
		if s.childProcessWaiter != nil {
			log.Printf("Child process waiter already set")
			l <- nil
			return
		}
		log.Printf("Waiting for child processes to finish")
		ch := make(chan *ProcessState, 1)
		s.childProcessWaiter = make(chan struct{})
		waiter := s.childProcessWaiter
		s.mu.Unlock()
		l <- &WaitRequest{
			Pid:   0, // Wait for any child process
			State: ch,
		}
		select {
		case state := <-ch: // Some process finished
			s.mu.Lock()
			log.Printf("Orphaned process finished with PID %d, exit status %d", state.Pid, state.WaitStatus.ExitStatus())
			l = s.waiter.AcquireLock()
			// Check again in next loop
			continue
		case <-waiter: // cancelled.
			log.Printf("Cancelled waiting for child processes")
			s.waiter.AcquireLock() <- &WaitRequest{
				Pid:   0,
				State: nil,
			}
			s.mu.Lock()
			return
		}
	}
	s.idleTimer.Reset(s.IdleTimeout)
}

func (s *SSHD) checkpointSelf(ctx context.Context) error {
	log.Printf("Checkpointing current session")
	conn, err := grpc.NewClient("unix:///run/velda/agent.sock", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := proto.NewAgentDaemonClient(conn)
	_, err = client.CheckPoint(ctx, &proto.CheckPointRequest{})
	if err != nil {
		return err
	}
	return nil
}
