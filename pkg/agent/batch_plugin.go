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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/proto"
)

type BatchPlugin struct {
	PluginBase
	waiterPlugin     any // *WaiterPlugin
	requestPlugin    any // *SessionRequestPlugin
	completionSignal any // *CompletionSignalPlugin
}

func NewBatchPlugin(waiterPlugin any, requestPlugin any, completionSignal any) *BatchPlugin {
	return &BatchPlugin{
		waiterPlugin:     waiterPlugin,
		requestPlugin:    requestPlugin,
		completionSignal: completionSignal,
	}
}

func (p *BatchPlugin) Run(ctx context.Context) error {
	sessionReq := ctx.Value(p.requestPlugin).(*proto.SessionRequest)
	if sessionReq.Workload != nil {
		completionChan := ctx.Value(p.completionSignal).(chan error)
		waiter := ctx.Value(p.waiterPlugin).(*Waiter)
		go func() {
			err := p.run(waiter, sessionReq)
			if err != nil {
				completionChan <- err
			}
			close(completionChan)
		}()
	}
	return p.RunNext(ctx)
}

func (p *BatchPlugin) run(waiter *Waiter, sessionReq *proto.SessionRequest) error {
	workload := sessionReq.Workload
	wait := waiter.AcquireLock()
	commandPath := workload.Command
	if filepath.Base(commandPath) == commandPath {
		var err error
		commandPath, err = exec.LookPath(workload.Command)
		if err != nil {
			return fmt.Errorf("Command not found in PATH: %w", err)
		}
	}
	taskId := sessionReq.TaskId
	err := os.MkdirAll(fmt.Sprintf("/.velda_tasks/%s", filepath.Dir(taskId)), 0755)
	if err != nil {
		return fmt.Errorf("Failed to create task directories %s: %w", taskId, err)
	}
	stdin, err := os.Open("/dev/null")
	if err != nil {
		return fmt.Errorf("Failed to open /dev/null: %w", err)
	}
	stdout, err := os.Create(fmt.Sprintf("/.velda_tasks/%s.stdout", taskId))
	if err != nil {
		return fmt.Errorf("Failed to open stdout file %s: %w", taskId, err)
	}
	stderr, err := os.Create(fmt.Sprintf("/.velda_tasks/%s.stderr", taskId))
	if err != nil {
		return fmt.Errorf("Failed to open stderr file %s: %w", taskId, err)
	}
	command, err := os.StartProcess(commandPath, append([]string{workload.Command}, workload.Args...), &os.ProcAttr{
		Env:   workload.Environs,
		Dir:   workload.WorkingDir,
		Files: []*os.File{stdin, stdout, stderr},
		Sys: &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid:    workload.Uid,
				Gid:    workload.Gid,
				Groups: workload.Groups,
			},
		},
	})
	stdin.Close()
	stdout.Close()
	stderr.Close()
	if err != nil {
		return fmt.Errorf("Failed to start process: %w", err)
	}
	// TODO: Return result.
	processStateChan := make(chan *ProcessState)
	wait <- &WaitRequest{
		Pid:   command.Pid,
		State: processStateChan,
	}
	processState := <-processStateChan
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
