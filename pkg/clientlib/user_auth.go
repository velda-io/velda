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
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"velda.io/velda/pkg/proto"
)

func GetAccessToken(ctx context.Context) (string, error) {
	if value, ok := ctx.Value("jwt").(string); ok {
		return value, nil
	}

	authenticator, err := GetAuthenticator()
	if err != nil {
		return "", err
	}
	token, err := authenticator.GetAccessToken(ctx)
	if err != nil {
		return "", err
	}
	return token, nil
}

func GetAuthInterceptor() grpc.UnaryClientInterceptor {
	return unaryAuthInterceptor
}
func GetStreamAuthInterceptor() grpc.StreamClientInterceptor {
	return streamAuthInterceptor
}

func unaryAuthInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if !authRequired(method) {
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	token, err := GetAccessToken(ctx)
	if errors.Is(err, NotLoginnedError) {
		// For simple auth
		if IsInSession() {
			sessionInfo := fmt.Sprintf("%d:%s:%s", agentConfig.Instance, agentConfig.Session, agentConfig.TaskId)
			ctx = metadata.AppendToOutgoingContext(ctx, "velda-session", sessionInfo)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	if err != nil {
		return err
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	return invoker(ctx, method, req, reply, cc, opts...)
}

func streamAuthInterceptor(
	ctx context.Context,
	desc *grpc.StreamDesc,
	cc *grpc.ClientConn,
	method string,
	streamer grpc.Streamer,
	opts ...grpc.CallOption,
) (grpc.ClientStream, error) {
	if !authRequired(method) {
		return streamer(ctx, desc, cc, method, opts...)
	}
	token, err := GetAccessToken(ctx)
	if errors.Is(err, NotLoginnedError) {
		// For simple auth
		if IsInSession() {
			sessionInfo := fmt.Sprintf("%d:%s:%s", agentConfig.Instance, agentConfig.Session, agentConfig.TaskId)
			ctx = metadata.AppendToOutgoingContext(ctx, "velda-session", sessionInfo)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
	if err != nil {
		return nil, err
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	return streamer(ctx, desc, cc, method, opts...)
}

func GetAuthenticator() (*Authenticator, error) {
	if IsInSession() {
		// A nil authenticator means we are in a session.
		return nil, nil
	}
	authClient, err := GetAuthServiceClient()
	if err != nil {
		return nil, err
	}
	curCfg, err := CurrentConfig()
	if err != nil {
		return nil, err
	}
	return NewAuthenticator(
		curCfg.Profile,
		GetConfigDir(),
		authClient,
	)
}

func GetAuthServiceClient() (proto.AuthServiceClient, error) {
	conn, connErr := GetApiConnection()
	if connErr != nil {
		return nil, connErr
	}
	return proto.NewAuthServiceClient(conn), nil
}

func RenameProfile(oldProfile, newProfile string) error {
	// Update token storage
	for _, tokenDb := range []string{GetConfigDir() + accessTokenDb, GetConfigDir() + refreshTokenDb} {
		if _, err := os.Stat(tokenDb); err == nil {
			db, err := sql.Open("sqlite", tokenDb)
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec("UPDATE tokens SET profile = $1 WHERE profile = $2", newProfile, oldProfile)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func DeleteProfile(profile string) error {
	for _, tokenDb := range []string{GetConfigDir() + accessTokenDb, GetConfigDir() + refreshTokenDb} {
		removeToken(tokenDb, profile)
	}
	return nil
}

func authRequired(method string) bool {
	if strings.HasPrefix(method, "/velda.AuthService/") {
		return false
	}
	switch method {
	case "/velda.BrokerService/AgentUpdate":
		return false
	}
	return true
}
