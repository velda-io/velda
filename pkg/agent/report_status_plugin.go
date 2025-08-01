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
	"context"
	"encoding/gob"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"velda.io/velda/pkg/agentd"
)

type ReportStatusPlugin struct {
	PluginBase

	sshdPlugin any
}

func NewReportStatusPlugin(sshdPlugin any) *ReportStatusPlugin {
	return &ReportStatusPlugin{
		sshdPlugin: sshdPlugin,
	}
}

func (p *ReportStatusPlugin) Run(ctx context.Context) error {
	sshd := ctx.Value(p.sshdPlugin).(*SSHD)
	go func() {
		for {
			addr := sshd.listener.Addr()
			port := addr.(*net.TCPAddr).Port
			sandboxConnection := agentd.SandboxConnection{
				Port:    port,
				HostKey: sshd.HostPublicKey.Marshal(),
			}
			err := gob.NewEncoder(os.Stdout).Encode(sandboxConnection)
			if err != nil {
				log.Fatal("Failed to encode sandbox connection:", err)
			}
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGCONT)
			<-sig
		}
	}()
	return p.RunNext(ctx)
}
