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
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

const CheckPointedExitCode = 100

type Runner struct {
	proto.UnimplementedAgentDaemonServer
	executable  string
	workDirBase string
	baseCgroup  string
	dnsServer   *DnsServer
	mu          sync.Mutex               // protects processes map
	processes   map[SessionKey]*exec.Cmd // sessionId -> cmd
	MaxTime     time.Duration
	nd          *NetworkDaemon
}

type SandboxConnection struct {
	Port    int
	HostKey []byte
}

type SessionCompletion struct {
	InstanceId   int64
	SessionId    string
	Error        error
	Checkpointed bool
	BatchResult  *proto.BatchTaskResult
}

func NewRunner(workDirBase string, nd *NetworkDaemon) *Runner {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	r := &Runner{
		executable:  executable,
		workDirBase: workDirBase,
		processes:   make(map[SessionKey]*exec.Cmd),
		nd:          nd,
	}
	if err := r.initCgroup(); err != nil {
		panic(err)
	}
	brokerClient, err := clientlib.GetBrokerClient()
	if err != nil {
		panic(err)
	}
	dnsServer := NewDnsServer("localhost:53", "udp", brokerClient)
	r.dnsServer = dnsServer
	go dnsServer.Run()
	return r
}

func (r *Runner) Run(agentaName string, session *proto.SessionRequest, completion chan *SessionCompletion) (*proto.SessionInitResponse, error) {
	// Run the work item
	workDir := path.Join(r.workDirBase, fmt.Sprintf("%d-%s", session.InstanceId, session.SessionId))
	if err := os.Mkdir(workDir, 0755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("Mkdir workDir: %w", err)
	}

	if err := os.Mkdir(path.Join(workDir, "velda"), 0755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("Mkdir velda: %w", err)
	}

	cgroupPath, err := r.setupCgroup(session.SessionId)
	if err != nil {
		return nil, fmt.Errorf("SetupCgroup: %w", err)
	}
	needCleanup := true
	defer func() {
		if needCleanup {
			r.cleanupCgroup(cgroupPath)
		}
	}()

	cgroupFd, err := syscall.Open(cgroupPath, syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("Open cgroup: %w", err)
	}
	defer syscall.Close(cgroupFd)

	dnsCtx := clientlib.BindSession(context.Background(), session)
	r.dnsServer.SetContext(dnsCtx)

	nh := r.nd.Get()
	l, _ := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", nh))
	log.Printf("Network namespace link: %s\n", l)
	defer func() {
		if needCleanup {
			r.nd.Put(nh)
		}
	}()

	netfdChild := 3
	if session.Workload != nil {
		netfdChild = 4
	}

	args := []string{
		"agent",
		"sandbox",
		"--agent-name",
		agentaName,
		"--workdir",
		workDir,
	}
	if nh.NetNamespace > 0 {
		args = append(args, "--netfd", strconv.Itoa(netfdChild))
	}
	cmd := exec.Command(r.executable, args...)

	var batch = false
	var batchOutput, batchIn *os.File
	rawPayload, err := pb.Marshal(session)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = bytes.NewReader(rawPayload)

	if session.Workload != nil {
		batch = true
		batchOutput, batchIn, err = os.Pipe()
		cmd.ExtraFiles = append(cmd.ExtraFiles, batchIn)
	}
	if nh.NetNamespace > 0 {
		netfdF := os.NewFile(uintptr(nh.NetNamespace), "netns")
		cmd.ExtraFiles = append(cmd.ExtraFiles, netfdF)
	}

	stdout, err := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err != nil {
		return nil, fmt.Errorf("StdoutPipe: %w", err)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:  syscall.CLONE_NEWNS,
		Setsid:      true,
		UseCgroupFD: true,
		CgroupFD:    cgroupFd,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if batch {
		batchIn.Close()
	}
	var sandboxConnection SandboxConnection
	dec := gob.NewDecoder(stdout)
	if err := dec.Decode(&sandboxConnection); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("Decode: %w", err)
	}
	key := SessionKey{
		InstanceId: session.InstanceId,
		SessionId:  session.SessionId,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processes[key] = cmd
	needCleanup = false
	go func() {
		defer r.nd.Put(nh)
		defer r.cleanupCgroup(cgroupPath)
		defer r.dnsServer.Reset()
		defer func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			delete(r.processes, key)
		}()

		result := &SessionCompletion{
			InstanceId: session.InstanceId,
			SessionId:  session.SessionId,
		}
		if batch {
			result.BatchResult, result.Error = func() (*proto.BatchTaskResult, error) {
				defer batchOutput.Close()
				batchResultRaw, err := io.ReadAll(batchOutput)
				if err != nil {
					return nil, fmt.Errorf("ReadBatchOutput: %w", err)
				}
				batchResult := &proto.BatchTaskResult{}
				if err := pb.Unmarshal(batchResultRaw, batchResult); err != nil {
					return nil, fmt.Errorf("UnmarshalBatchResult: %w", err)
				}
				return batchResult, nil
			}()
		}
		cmdResult := cmd.Wait()
		if cmdResult != nil && cmdResult.(*exec.ExitError) != nil && cmdResult.(*exec.ExitError).ExitCode() == CheckPointedExitCode {
			result.Checkpointed = true
		} else if cmdResult != nil {
			result.Error = cmdResult
		}
		completion <- result
	}()
	port := nh.AgentPort
	if port == 0 {
		port = sandboxConnection.Port
	}

	return &proto.SessionInitResponse{
		InstanceId: session.InstanceId,
		SessionId:  session.SessionId,
		Port:       int32(port),
		HostKey:    sandboxConnection.HostKey,
	}, nil
}

func (r *Runner) Cleanup(key SessionKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cmd, ok := r.processes[key]; ok {
		if cmd.Process != nil {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				log.Printf("Failed to kill process for session %s: %v", key.SessionId, err)
			} else {
				log.Printf("Killed process for session %s", key.SessionId)
			}
		}
		delete(r.processes, key)
	} else {
		log.Printf("No process found for session %s", key.SessionId)
	}
}

func (r *Runner) initCgroup() error {
	currentCgroupB, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return fmt.Errorf("ReadFile /proc/self/cgroup: %w", err)
	}
	currentCgroup := string(currentCgroupB)
	currentCgroup = strings.TrimSpace(currentCgroup)
	segments := strings.Split(currentCgroup, ":")
	if segments[1] != "" {
		return fmt.Errorf("Cgroup v1 is not supported")
	}
	// Expecting it to start with "/"
	if segments[2] == "" || segments[2][0] != '/' {
		return fmt.Errorf("Invalid cgroup path")
	}
	r.baseCgroup = segments[2]

	// Move current process to "daemon" cgroup
	if err := os.Mkdir(path.Join("/sys/fs/cgroup", r.baseCgroup[1:], "daemon"), 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("Mkdir daemon: %w", err)
	}
	if err := os.WriteFile(path.Join("/sys/fs/cgroup", r.baseCgroup[1:], "daemon/cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0400); err != nil {
		return fmt.Errorf("WriteFile cgroup.procs: %w", err)
	}

	// Enable all sub-controllers for the root
	subControllersBytes, err := os.ReadFile(path.Join("/sys/fs/cgroup", r.baseCgroup[1:], "cgroup.controllers"))
	if err != nil {
		return fmt.Errorf("ReadFile cgroup.controllers: %w", err)
	}
	subControllers := strings.Split(string(subControllersBytes), " ")
	enableCommand := ""
	for _, controller := range subControllers {
		enableCommand += " +" + controller
	}
	if enableCommand == "" {
		return nil
	}
	err = os.WriteFile(path.Join("/sys/fs/cgroup", r.baseCgroup[1:], "cgroup.subtree_control"), []byte(enableCommand[1:]), 0400)
	if err != nil {
		return fmt.Errorf("WriteFile cgroup.subtree_control: %w", err)
	}
	err = os.WriteFile(path.Join("/sys/fs/cgroup", r.baseCgroup[1:], "memory.oom.group"), []byte("0"), 0400)
	if err != nil {
		return fmt.Errorf("WriteFile memory.oom.group: %w", err)
	}
	log.Printf("Completed cgroup initialization. Enabled cgroup controllers: %s", enableCommand[1:])
	return nil
}

func (r *Runner) setupCgroup(session string) (string, error) {
	cgroupPath := path.Join("/sys/fs/cgroup", r.baseCgroup[1:], session)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return cgroupPath, fmt.Errorf("Mkdir cgroup: %w", err)
	}

	return cgroupPath, nil
}

func (r *Runner) cleanupCgroup(cgroupPath string) error {
	if err := os.Remove(cgroupPath); err != nil {
		return fmt.Errorf("RemoveAll cgroup: %w", err)
	}
	return nil
}
