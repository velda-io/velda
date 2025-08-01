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
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
)

type ossAuthProvider struct{}

func (o ossAuthProvider) GetAuthInterceptor() grpc.UnaryClientInterceptor {
	return unaryAuthInterceptor
}

func (o ossAuthProvider) GetAccessToken(ctx context.Context) (string, error) {
	return "", nil
}

func (o ossAuthProvider) RenameProfile(oldName, newName string) error {
	return nil
}

func (o ossAuthProvider) DeleteProfile(profile string) error {
	return nil
}

func (o ossAuthProvider) BindSession(ctx context.Context, session *proto.SessionRequest) context.Context {
	ctx = context.WithValue(ctx, "instanceId", session.InstanceId)
	return ctx
}

func (o ossAuthProvider) SshDial(cmd *cobra.Command, sshConn *proto.ExecutionStatus_SshConnection, user string) (*clientlib.SshClient, error) {
	hostKey, err := ssh.ParsePublicKey(sshConn.HostKey)
	if err != nil {
		return nil, fmt.Errorf("Error parsing host key: %v", err)
	}

	clientConfig := &ssh.ClientConfig{
		User: user,
		// TODO: Add support for SSH keys.
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshConn.Host, sshConn.Port), clientConfig)
	if err != nil {
		return nil, fmt.Errorf("Error dialing ssh: %v", err)
	}
	return &clientlib.SshClient{client, ""}, nil
}

func unaryAuthInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return invoker(ctx, method, req, reply, cc, opts...)
}

func init() {
	clientlib.SetAuthProvider(ossAuthProvider{})
}
