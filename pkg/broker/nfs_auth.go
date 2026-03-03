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
package broker

import (
	"context"
	"net"

	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/storage"
)

// NfsExportAuth adapts storage.NfsAuth to the broker AuthHelper interface.
// It extracts broker-specific information (agent host, NFS server IP) from the
// Agent and Session and delegates the actual NFS grant/revoke operations to the
// underlying storage.NfsAuth implementation.
type NfsExportAuth struct {
	auth storage.NfsAuth
}

func NewNfsExportAuth(auth storage.NfsAuth) *NfsExportAuth {
	return &NfsExportAuth{auth: auth}
}

func (n *NfsExportAuth) GrantAccessToAgent(ctx context.Context, agent *Agent, session *Session) error {
	agentHost := agent.Host.String()
	nfsServer := agent.PeerInfo.LocalAddr.(*net.TCPAddr).IP.String()
	return n.auth.GrantNfsAccess(ctx, session.Request, agentHost, nfsServer)
}

func (n *NfsExportAuth) RevokeAccessToAgent(ctx context.Context, agent *Agent, session *Session) error {
	return n.auth.RevokeNfsAccess(ctx, session.Request.InstanceId, session.Request.SessionId)
}

func (n *NfsExportAuth) GrantAccessToClient(ctx context.Context, session *Session, status *proto.ExecutionStatus) error {
	return nil
}

func (n *NfsExportAuth) UpdateServerInfo(ctx context.Context, info *proto.ServerInfo) error {
	return nil
}

