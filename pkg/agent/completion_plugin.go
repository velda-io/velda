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
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var TimeoutError = errors.New("Max session time reached")
var SigTermError = errors.New("Agent received SIGTERM")

type CompletionSignalPlugin struct {
	PluginBase
}

func NewCompletionSignalPlugin() *CompletionSignalPlugin {
	return &CompletionSignalPlugin{}
}

func (p *CompletionSignalPlugin) Run(ctx context.Context) error {
	h := NewCompletionHandle()
	return p.RunNext(context.WithValue(ctx, p, h))
}

// CompletionHandle represents a session completion state. It allows waiting
// for completion, retrieving the error, and completing with an error.
type CompletionHandle struct {
	mu        sync.Mutex
	done      chan struct{}
	err       error
	completed bool
}

func NewCompletionHandle() *CompletionHandle {
	return &CompletionHandle{done: make(chan struct{})}
}

// Error returns the completion error (nil if none or not completed yet).
func (h *CompletionHandle) Error() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// Complete sets the error and closes the done channel (idempotent).
func (h *CompletionHandle) Complete(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.completed {
		return
	}
	h.err = err
	h.completed = true
	close(h.done)
}

// getHandle returns the CompletionHandle stored in context, or nil.
func (p *CompletionSignalPlugin) getHandle(ctx context.Context) *CompletionHandle {
	if v := ctx.Value(p); v != nil {
		if h, ok := v.(*CompletionHandle); ok {
			return h
		}
	}
	return nil
}

// GetError returns the current completion error (non-blocking). If no
// completion handle is present, returns nil.
func (p *CompletionSignalPlugin) GetError(ctx context.Context) error {
	if h := p.getHandle(ctx); h != nil {
		return h.Error()
	}
	return nil
}

// Complete marks the session as completed with provided error.
func (p *CompletionSignalPlugin) Complete(ctx context.Context, err error) {
	if h := p.getHandle(ctx); h != nil {
		h.Complete(err)
	}
}

func (p *CompletionSignalPlugin) GetWaiterPlugin() *CompletionWaitPlugin {
	return &CompletionWaitPlugin{
		SignalPlugin: p,
	}
}

type CompletionWaitPlugin struct {
	PluginBase
	SignalPlugin *CompletionSignalPlugin
	MaxTime      time.Duration
}

func (p *CompletionWaitPlugin) Run(ctx context.Context) error {
	handle := p.SignalPlugin.getHandle(ctx)
	doneChan := handle.done
	var timeoutChan <-chan time.Time
	if p.MaxTime > 0 {
		timeoutChan = time.After(p.MaxTime)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-doneChan:
		return handle.Error()
	case <-sig:
		return SigTermError
	case <-timeoutChan:
		return TimeoutError
	}
}
