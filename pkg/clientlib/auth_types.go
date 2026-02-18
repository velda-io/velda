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

	"velda.io/velda/pkg/proto"
)

type serverAuthHandler interface {
	BindSession(ctx context.Context, session *proto.SessionRequest) context.Context
	HandleServerInfo(ctx context.Context, info *proto.ServerInfo) error
}

var (
	theServerAuthProvider serverAuthHandler
)

func HandleServerInfo(ctx context.Context, info *proto.ServerInfo) error {
	return theServerAuthProvider.HandleServerInfo(ctx, info)
}

func BindSession(ctx context.Context, session *proto.SessionRequest) context.Context {
	return theServerAuthProvider.BindSession(ctx, session)
}

func SetAuthProvider(provider serverAuthHandler) {
	if theServerAuthProvider != nil {
		panic("AuthProvider is already set")
	}
	theServerAuthProvider = provider
}

type OssAuthProvider struct{}

func (o OssAuthProvider) BindSession(ctx context.Context, session *proto.SessionRequest) context.Context {
	ctx = context.WithValue(ctx, "instanceId", session.InstanceId)
	return ctx
}

func (o OssAuthProvider) HandleServerInfo(ctx context.Context, info *proto.ServerInfo) error {
	return nil
}
