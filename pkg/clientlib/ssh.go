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
	"context"
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"

	"velda.io/velda/pkg/utils"
)

type SshClient struct {
	*ssh.Client
	ShutdownMessage string
}

func (c *SshClient) PortForward(ctx context.Context, lis net.Listener, port int) error {
	remoteAddr := fmt.Sprintf("%s:%d", "localhost", port)
	for {
		localConn, err := lis.Accept()
		if errors.Is(err, net.ErrClosed) {
			break
		}
		if err != nil {
			return fmt.Errorf("Error accepting connection: %v", err)
		}

		remoteConn, err := c.Dial("tcp", remoteAddr)
		if err != nil {
			return fmt.Errorf("Error dialing remote connection: %v", err)
		}
		go utils.CopyConnections(localConn.(*net.TCPConn), remoteConn.(ssh.Channel))
	}
	return nil

}

func (c *SshClient) HandleGlobalRequests(reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.Type == "close@velda" {
			payload := struct {
				Message string
			}{}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				req.Reply(false, nil)
			}
			c.ShutdownMessage = payload.Message
			c.Close()
		} else {
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func (c *SshClient) NewSession() (*SshSession, <-chan *ssh.Request, error) {
	rawSession, reqs, err := c.Conn.OpenChannel("session", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("Error opening channel: %v", err)
	}
	return NewSshSession(rawSession), reqs, nil
}
