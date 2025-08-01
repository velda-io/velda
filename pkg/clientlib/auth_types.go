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
package clientlib

import (
	"context"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"velda.io/velda/pkg/proto"
)

type authProvider interface {
	GetAccessToken(ctx context.Context) (string, error)
	BindSession(ctx context.Context, session *proto.SessionRequest) context.Context
	GetAuthInterceptor() grpc.UnaryClientInterceptor
	RenameProfile(oldName, newName string) error
	DeleteProfile(profile string) error
	SshDial(cmd *cobra.Command, sshConn *proto.ExecutionStatus_SshConnection, user string) (*SshClient, error)
}

var (
	theAuthProvider authProvider
)

func GetAccessToken(ctx context.Context) (string, error) {
	return theAuthProvider.GetAccessToken(ctx)
}
func BindSession(ctx context.Context, session *proto.SessionRequest) context.Context {
	return theAuthProvider.BindSession(ctx, session)
}

func GetAuthInterceptor() grpc.UnaryClientInterceptor {
	return theAuthProvider.GetAuthInterceptor()
}

func RenameProfile(oldName, newName string) error {
	return theAuthProvider.RenameProfile(oldName, newName)
}

func DeleteProfile(profile string) error {
	return theAuthProvider.DeleteProfile(profile)
}

func SshConnect(cmd *cobra.Command, sshConn *proto.ExecutionStatus_SshConnection, user string) (*SshClient, error) {
	return theAuthProvider.SshDial(cmd, sshConn, user)
}

func SetAuthProvider(provider authProvider) {
	if theAuthProvider != nil {
		panic("AuthProvider is already set")
	}
	theAuthProvider = provider
}
