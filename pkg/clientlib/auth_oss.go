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
	"net"
	"os"
	"strings"

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

type jumpConn struct {
	net.Conn
	client *ssh.Client
}

func (jc *jumpConn) Close() error {
	jc.Conn.Close()
	return jc.client.Close()
}

// dialJumpServer establishes a connection through an SSH jump server
func dialJumpServer(jumpProxy, jumpIdentityFile, targetHost string, targetPort int) (net.Conn, error) {
	// Parse jump proxy in format user@host. Use last '@' to support any '@' in user if present.
	at := strings.LastIndex(jumpProxy, "@")
	if at <= 0 || at == len(jumpProxy)-1 {
		return nil, fmt.Errorf("invalid jump-proxy format, expected user@host, got: %s", jumpProxy)
	}
	jumpUser := strings.TrimSpace(jumpProxy[:at])
	jumpHost := strings.TrimSpace(jumpProxy[at+1:])

	// Validate non-empty parts
	if jumpUser == "" {
		return nil, fmt.Errorf("invalid jump-proxy: username is empty in %s", jumpProxy)
	}
	if jumpHost == "" {
		return nil, fmt.Errorf("invalid jump-proxy: host is empty in %s", jumpProxy)
	}

	if _, _, err := net.SplitHostPort(jumpHost); err != nil {
		if !strings.Contains(err.Error(), "missing port in address") {
			return nil, fmt.Errorf("invalid jump-proxy host: %v", err)
		}
		// Add default SSH port if not specified
		jumpHost = fmt.Sprintf("%s:22", jumpHost)
	}

	// Read jump server identity file
	var authMethods []ssh.AuthMethod
	if jumpIdentityFile != "" {
		keyData, err := os.ReadFile(jumpIdentityFile)
		if err != nil {
			return nil, fmt.Errorf("error reading jump server SSH key file %s: %v", jumpIdentityFile, err)
		}

		key, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			return nil, fmt.Errorf("error parsing jump server SSH key file %s: %v", jumpIdentityFile, err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(key))
	} else {
		return nil, fmt.Errorf("jump server identity file is required")
	}

	// Configure jump server client
	jumpConfig := &ssh.ClientConfig{
		User:            jumpUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For jump server
	}

	// Connect to jump server
	DebugLog("Connecting to jump server: %s@%s", jumpUser, jumpHost)
	jumpClient, err := ssh.Dial("tcp", jumpHost, jumpConfig)
	if err != nil {
		return nil, fmt.Errorf("error dialing jump server: %v", err)
	}

	// Dial target through jump server
	targetAddr := fmt.Sprintf("%s:%d", targetHost, targetPort)
	DebugLog("Dialing target %s through jump server", targetAddr)
	conn, err := jumpClient.Dial("tcp", targetAddr)
	if err != nil {
		jumpClient.Close()
		return nil, fmt.Errorf("error dialing target through jump server: %v", err)
	}

	return &jumpConn{Conn: conn, client: jumpClient}, nil
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

	// Check if jump server is configured
	var jumpProxy, jumpIdentityFile string
	if !IsInSession() {
		jumpProxy, _ = GetFlagValue(cmd, "jump-proxy")
		jumpIdentityFile, _ = GetFlagValue(cmd, "jump-identity-file")
	}

	var client *ssh.Client
	if jumpProxy != "" {
		// Use jump server to connect
		DebugLog("Using jump server: %s", jumpProxy)
		conn, err := dialJumpServer(jumpProxy, jumpIdentityFile, sshConn.Host, int(sshConn.Port))
		if err != nil {
			return nil, err
		}

		// Create SSH client using the connection through jump server
		sshConn2, chans, reqs, err := ssh.NewClientConn(conn, fmt.Sprintf("%s:%d", sshConn.Host, sshConn.Port), clientConfig)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("Error creating SSH client connection through jump server: %v", err)
		}
		client = ssh.NewClient(sshConn2, chans, reqs)
	} else {
		// Direct connection
		client, err = ssh.Dial("tcp", fmt.Sprintf("%s:%d", sshConn.Host, sshConn.Port), clientConfig)
		if err != nil {
			return nil, fmt.Errorf("Error dialing ssh: %v", err)
		}
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
