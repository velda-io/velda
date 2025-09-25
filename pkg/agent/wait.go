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
// Collect child process state and reap zombines.
// Since the daemon is running as the init process, it is responsible for reaping zombie processes.
// Since wait4 may reap any child process that is being actived waited on,
// we use a central loop to manage all wait requests.
package agent

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/shirou/gopsutil/v3/process"
)

// os.ProcessState is private. Reimplement it here for linux only.
type ProcessState struct {
	Pid        int
	WaitStatus syscall.WaitStatus
	Rusage     *syscall.Rusage
}

type Waiter struct {
	requests    map[int]chan *ProcessState
	closeChan   chan struct{}
	lockRequest chan struct{}
	waitRequest chan *WaitRequest
}

type WaitRequest struct {
	Pid   int
	State chan *ProcessState
}

func NewWaiter() *Waiter {
	return &Waiter{
		requests:    make(map[int]chan *ProcessState),
		closeChan:   make(chan struct{}),
		lockRequest: make(chan struct{}),
		waitRequest: make(chan *WaitRequest, 1),
	}
}

// Temporarily pause the loop and add a new process.
// This prevents a process to be treated as adopted zombie while a new process
// is being added.
func (w *Waiter) AcquireLock() chan<- *WaitRequest {
	// This is unbuffered, which will pause the loop.
	w.lockRequest <- struct{}{}
	return w.waitRequest
}

func (w *Waiter) HasChildProcess() bool {
	mypid := os.Getpid()
	if procs, err := process.Processes(); err == nil {
		for _, p := range procs {
			ppid, err := p.Ppid()
			if err == nil && int(ppid) == mypid {
				return true
			}
		}
	}
	return false
}

func (w *Waiter) Run() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGCHLD)
	for {
		select {
		case <-w.closeChan:
			return
		case <-sig:
			for {
				var waitStatus syscall.WaitStatus
				var rusage syscall.Rusage
				pid, err := syscall.Wait4(-1, &waitStatus, syscall.WNOHANG, &rusage)
				if err == syscall.ECHILD {
					break
				}
				if err != nil {
					log.Println("Error waiting for child process:", err)
					break
				}
				if pid == 0 {
					break
				}
				if waitStatus.Stopped() || waitStatus.Continued() {
					continue
				}
				request, ok := w.requests[pid]
				if ok {
					delete(w.requests, pid)
				}
				if !ok {
					request, ok = w.requests[0]
					if ok {
						delete(w.requests, 0)
					}
				}
				if !ok {
					if waitStatus.Signaled() {
						log.Printf("Zombine process %d reaped, exited with signal %d", pid, waitStatus.Signal())
					} else {
						log.Printf("Zombine process %d reaped, exited with status %d", pid, waitStatus.ExitStatus())
					}
					continue
				}
				request <- &ProcessState{
					Pid:        pid,
					WaitStatus: waitStatus,
					Rusage:     &rusage,
				}
			}
		case <-w.lockRequest:
			request := <-w.waitRequest
			if request != nil {
				if request.State == nil {
					delete(w.requests, request.Pid)
				} else {
					w.requests[request.Pid] = request.State
				}
			}
		}
	}
}

func (w *Waiter) Close() {
	close(w.closeChan)
}

type WaiterPlugin struct {
	PluginBase
}

func NewWaiterPlugin() *WaiterPlugin {
	return &WaiterPlugin{}
}

func (p *WaiterPlugin) Run(ctx context.Context) error {
	waiter := NewWaiter()
	go waiter.Run()
	defer waiter.Close()
	return p.RunNext(context.WithValue(ctx, p, waiter))
}
