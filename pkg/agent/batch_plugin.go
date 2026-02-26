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
package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

type BatchPlugin struct {
	PluginBase
	waiterPlugin     any // *WaiterPlugin
	requestPlugin    any // *SessionRequestPlugin
	completionSignal any // *CompletionSignalPlugin
	commandModifier  CommandModifier
}

func NewBatchPlugin(waiterPlugin any, requestPlugin any, completionSignal any, commandModifier CommandModifier) *BatchPlugin {
	return &BatchPlugin{
		waiterPlugin:     waiterPlugin,
		requestPlugin:    requestPlugin,
		completionSignal: completionSignal,
		commandModifier:  commandModifier,
	}
}

func (p *BatchPlugin) Run(ctx context.Context) error {
	sessionReq := ctx.Value(p.requestPlugin).(*proto.SessionRequest)
	if sessionReq.Workload != nil {
		completionChan := ctx.Value(p.completionSignal).(chan error)
		waiter := ctx.Value(p.waiterPlugin).(*Waiter)
		wait := waiter.AcquireLock()
		cmd, logsDone, err := p.start(ctx, sessionReq)
		if err != nil {
			wait <- nil
			return err
		}
		processStateChan := make(chan *ProcessState)
		wait <- &WaitRequest{
			Pid:   cmd.Process.Pid,
			State: processStateChan,
		}
		go func() {
			processState := <-processStateChan
			// Release resources managed by exec.Cmd
			cmd.Wait()
			// Wait for all log streaming goroutines to finish.
			logsDone.Wait()
			err := p.handleResult(processState)
			if err != nil {
				completionChan <- err
			}
			close(completionChan)
		}()
	}
	return p.RunNext(ctx)
}

func (p *BatchPlugin) start(ctx context.Context, sessionReq *proto.SessionRequest) (*exec.Cmd, *sync.WaitGroup, error) {
	workload := sessionReq.Workload
	taskId := sessionReq.TaskId
	stdin, err := os.Open("/dev/null")
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to open /dev/null: %w", err)
	}

	// Create pipes so we can tee the output to both local files and the server.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return nil, nil, fmt.Errorf("Failed to create stderr pipe: %w", err)
	}

	var user *User
	if workload.LoginUser != "" {
		// If a login user is specified, look up that user and use their environment
		user, err = LookupUser(workload.LoginUser)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to lookup user %s: %w", workload.LoginUser, err)
		}
		if workload.WorkingDir == "" {
			workload.WorkingDir = user.HomeDir
		}
	}
	commandPath := workload.CommandPath
	if commandPath == "" {
		commandPath = workload.Command
	}
	if strings.Contains(commandPath, "/") && commandPath[0] != '/' {
		// Convert to absolute dir first, because current working dir can be different.
		commandPath = filepath.Join(workload.WorkingDir, workload.Command)
	}
	commandPath, err = exec.LookPath(commandPath)
	if err != nil {
		return nil, nil, fmt.Errorf("Command not found in PATH: %w", err)
	}
	cmd := exec.Command(commandPath, workload.Args...)
	cmd.Dir = workload.WorkingDir
	cmd.Env = workload.Environs
	// Determine user credentials and environment
	var credential *syscall.Credential
	if user != nil {
		credential = user.Credential
		// Update working directory if using user home directory
		// Load default environment and append explicit environs
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, workload.Environs...)
	} else {
		// Use credentials from workload
		credential = &syscall.Credential{
			Uid:    workload.Uid,
			Gid:    workload.Gid,
			Groups: workload.Groups,
		}
		cmd.Env = workload.Environs
	}

	cmd.Stdin = stdin
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: credential,
	}
	if p.commandModifier != nil {
		p.commandModifier(cmd)
	}
	err = cmd.Start()
	// Close the write ends in the parent process; the child owns them now.
	stdin.Close()
	stdoutW.Close()
	stderrW.Close()
	if err != nil {
		stdoutR.Close()
		stderrR.Close()
		return nil, nil, fmt.Errorf("Failed to start process: %w", err)
	}
	log.Printf("Started batch task %s with PID %d", taskId, cmd.Process.Pid)

	// Open PushLogs stream to the server (best-effort; non-fatal on failure).
	pushStream, pushErr := openPushLogsStream(ctx, taskId)
	if pushErr != nil {
		log.Printf("PushLogs: could not open stream for task %s: %v", taskId, pushErr)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	finalWg := &sync.WaitGroup{}
	finalWg.Add(1)
	streamLog := func(r *os.File, streamType proto.LogTaskResponse_Stream) {
		defer wg.Done()
		defer r.Close()
		buf := make([]byte, 32*1024)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				data := buf[:n]
				if pushStream != nil {
					chunk := make([]byte, n)
					copy(chunk, data)
					if serr := pushStream.Send(&proto.PushLogsRequest{
						TaskId: taskId,
						Stream: streamType,
						Data:   chunk,
					}); serr != nil {
						log.Printf("PushLogs: send error for task %s: %v", taskId, serr)
						pushStream = nil // stop streaming, but continue writing locally
					}
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				log.Printf("PushLogs: read error for task %s stream %v: %v", taskId, streamType, readErr)
				break
			}
		}
	}

	go streamLog(stdoutR, proto.LogTaskResponse_STREAM_STDOUT)
	go streamLog(stderrR, proto.LogTaskResponse_STREAM_STDERR)

	// Close the push stream after both goroutines finish.
	go func() {
		wg.Wait()
		if pushStream != nil {
			if _, err := pushStream.CloseAndRecv(); err != nil && err != io.EOF {
				log.Printf("PushLogs: CloseAndRecv error for task %s: %v", taskId, err)
			}
		}
		finalWg.Done()
	}()

	return cmd, finalWg, nil
}

// openPushLogsStream opens a client streaming gRPC call to TaskLogService.PushLogs.
// Returns nil on failure so callers can degrade gracefully.
func openPushLogsStream(ctx context.Context, taskId string) (proto.TaskLogService_PushLogsClient, error) {
	clientlib.InitConfig()
	conn, err := clientlib.GetApiConnection()
	if err != nil {
		return nil, fmt.Errorf("get api connection: %w", err)
	}
	stream, err := proto.NewTaskLogServiceClient(conn).PushLogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("PushLogs RPC: %w", err)
	}
	return stream, nil
}

func (p *BatchPlugin) handleResult(processState *ProcessState) error {
	resultPb := &proto.BatchTaskResult{}
	sysState := processState.WaitStatus
	if sysState.Exited() {
		exitCode := int32(sysState.ExitStatus())
		resultPb.ExitCode = &exitCode
	} else if sysState.Signaled() {
		resultPb.TerminatedSignal = int32(sysState.Signal())
		resultPb.ExitCode = nil
	} else {
		log.Printf("Unknown exit status: %v", sysState)
		exitCode := int32(255)
		resultPb.ExitCode = &exitCode
	}
	resultBytes, err := pb.Marshal(resultPb)
	if err != nil {
		return fmt.Errorf("Marshal batch result: %w", err)
	}
	outfile := os.NewFile(3, "/proc/self/fd/3")
	defer outfile.Close()
	if _, err := outfile.Write(resultBytes); err != nil {
		return fmt.Errorf("Write batch result: %w", err)
	}
	return nil
}
