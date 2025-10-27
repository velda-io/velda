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
package apiserver

import (
	"context"
	"fmt"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

type BrokerAuth struct {
	nfsAuth *broker.NfsExportAuth
}

func NewBrokerAuth(nfsAuth *broker.NfsExportAuth) *BrokerAuth {
	return &BrokerAuth{
		nfsAuth: nfsAuth,
	}
}

func (n *BrokerAuth) GrantAccessToAgent(ctx context.Context, agent *broker.Agent, session *broker.Session) (rbac.User, error) {
	if err := n.nfsAuth.GrantAccessToAgent(ctx, agent, session); err != nil {
		return nil, fmt.Errorf("failed to grant NFS access: %w", err)
	}
	return &sessionUser{
		EmptyUser: rbac.EmptyUser{},
		taskId:    session.Request.TaskId,
	}, nil
}

func (n *BrokerAuth) ReGrantAccessToAgent(ctx context.Context, agent *broker.Agent, session *broker.Session) (rbac.User, error) {
	return &sessionUser{
		EmptyUser: rbac.EmptyUser{},
		taskId:    session.Request.TaskId,
	}, nil
}

func (n *BrokerAuth) RevokeAccessToAgent(ctx context.Context, agent *broker.Agent, session *broker.Session) error {
	return n.nfsAuth.RevokeAccessToAgent(ctx, agent, session)
}

func (n *BrokerAuth) GrantAccessToClient(ctx context.Context, session *broker.Session, status *proto.ExecutionStatus) error {
	return nil
}

func (n *BrokerAuth) UpdateServerInfo(ctx context.Context, info *proto.ServerInfo) error {
	return nil
}
