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
	"syscall"
	"time"

	"velda.io/velda/pkg/proto"
)

type SshdPlugin struct {
	PluginBase

	AgentName         string
	authDecoderPlugin any // *AuthPlugin
	waiter            any // *WaiterPlugin
	request           any // *SessionRequestPlugin
	completionSignal  any // *CompletionSignalPlugin
	commandModifier   CommandModifier
}

func NewSshdPlugin(agentName string, authDecoderPlugin any, waiter any, request any, completionSignal any, commandModifier CommandModifier) *SshdPlugin {
	return &SshdPlugin{
		AgentName:         agentName,
		authDecoderPlugin: authDecoderPlugin,
		waiter:            waiter,
		request:           request,
		completionSignal:  completionSignal,
		commandModifier:   commandModifier,
	}
}

func (p *SshdPlugin) Run(ctx context.Context) (err error) {
	auth := ctx.Value(p.authDecoderPlugin).(SshdAuthenticator)
	waiter := ctx.Value(p.waiter).(*Waiter)
	req := ctx.Value(p.request).(*proto.SessionRequest)

	syscall.Sethostname([]byte(req.SessionId))

	if err != nil {
		return fmt.Errorf("Parse token: %w", err)
	}
	sshd := NewSSHD(auth, waiter)

	initTimeout := req.InitTimeout.AsDuration()
	if initTimeout == 0 {
		initTimeout = 60 * time.Second
	}
	sshd.InitialConnectionTimeout = initTimeout
	sshd.IdleTimeout = req.IdleTimeout.AsDuration()
	sshd.AgentName = p.AgentName
	sshd.OnIdle = req.ConnectionFinishAction
	sshd.CommandModifier = p.commandModifier
	batch := req.Workload != nil
	if !batch {
		completion := ctx.Value(p.completionSignal).(chan error)
		sshd.OnShutdown = func() {
			log.Printf("no more connections, stopping sandbox")
			close(completion)
		}
	}
	_, err = sshd.Start()
	defer sshd.Stop()
	if err != nil {
		return fmt.Errorf("Start SSHD: %w", err)
	}
	defer func() {
		if err != nil {
			sshd.Shutdown(err.Error())
		}
	}()
	return p.RunNext(context.WithValue(ctx, p, sshd))
}
