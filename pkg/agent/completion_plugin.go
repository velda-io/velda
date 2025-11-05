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
	completion := make(chan error)
	return p.RunNext(context.WithValue(ctx, p, completion))
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
	completion := ctx.Value(p.SignalPlugin).(chan error)
	var timeoutChan <-chan time.Time
	if p.MaxTime > 0 {
		timeoutChan = time.After(p.MaxTime)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-completion:
		return err
	case <-sig:
		return SigTermError
	case <-timeoutChan:
		return TimeoutError
	}
}
