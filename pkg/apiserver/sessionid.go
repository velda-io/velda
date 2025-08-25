package apiserver

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"velda.io/velda/pkg/rbac"
)

func sessionInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	user, err := userFromGrpc(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	ctx = rbac.ContextWithUser(ctx, user)
	return handler(ctx, req)
}

func userFromGrpc(ctx context.Context) (rbac.User, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, errors.New("no metadata")
	}
	sid, ok := md["velda-session"]
	if !ok || len(sid) == 0 {
		return rbac.EmptyUser{}, nil
	}
	parts := strings.SplitN(sid[0], ":", 3)
	if len(parts) < 3 {
		return nil, errors.New("invalid session ID")
	}
	// Currently only task ID is used.
	return &sessionUser{
		EmptyUser: rbac.EmptyUser{},
		taskId:    parts[2],
	}, nil
}

type sessionUser struct {
	rbac.EmptyUser
	taskId string
}
