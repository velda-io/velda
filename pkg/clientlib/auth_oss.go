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
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"velda.io/velda/pkg/proto"
)

type OssAuthProvider struct{}

func (o OssAuthProvider) GetAuthInterceptor() grpc.UnaryClientInterceptor {
	return unaryAuthInterceptor
}

func (o OssAuthProvider) GetStreamAuthInterceptor() grpc.StreamClientInterceptor {
	return streamAuthInterceptor
}

func (o OssAuthProvider) GetAccessToken(ctx context.Context) (string, error) {
	return "", nil
}

func (o OssAuthProvider) RenameProfile(oldName, newName string) error {
	return nil
}

func (o OssAuthProvider) DeleteProfile(profile string) error {
	return nil
}

func (o OssAuthProvider) BindSession(ctx context.Context, session *proto.SessionRequest) context.Context {
	ctx = context.WithValue(ctx, "instanceId", session.InstanceId)
	return ctx
}

func (o OssAuthProvider) SshDial(cmd *cobra.Command, sshConn *proto.ExecutionStatus_SshConnection, user string) (*SshClient, error) {
	hostKey, err := ssh.ParsePublicKey(sshConn.HostKey)
	if err != nil {
		return nil, fmt.Errorf("Error parsing host key: %v", err)
	}

	authMethods := []ssh.AuthMethod{}
	var keyPath string
	if IsInSession() {
		keyPath = "/.velda/velda_key"
	} else {
		keyPath, err = GetFlagValue(cmd, "identity-file")
		if err != nil {
			return nil, fmt.Errorf("Error getting identity file flag: %v", err)
		}
	}

	DebugLog("Using SSH key file: %s", keyPath)
	if keyPath != "" {
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("Error reading SSH key file %s: %v", keyPath, err)
		}

		key, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("Error parsing SSH key file %s: %v", keyPath, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(key))
	}
	clientConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.FixedHostKey(hostKey),
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshConn.Host, sshConn.Port), clientConfig)
	if err != nil {
		return nil, fmt.Errorf("Error dialing ssh: %v", err)
	}
	return &SshClient{Client: client, ShutdownMessage: ""}, nil
}

func (o OssAuthProvider) HandleServerInfo(ctx context.Context, info *proto.ServerInfo) error {
	return nil
}

func unaryAuthInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if IsInSession() {
		sessionInfo := fmt.Sprintf("%d:%s:%s", agentConfig.Instance, agentConfig.Session, agentConfig.TaskId)
		ctx = metadata.AppendToOutgoingContext(ctx, "velda-session", sessionInfo)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func streamAuthInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if IsInSession() {
		sessionInfo := fmt.Sprintf("%d:%s:%s", agentConfig.Instance, agentConfig.Session, agentConfig.TaskId)
		ctx = metadata.AppendToOutgoingContext(ctx, "velda-session", sessionInfo)
	}
	return streamer(ctx, desc, cc, method, opts...)
}
