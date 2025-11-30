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
	"log"
	"math/rand"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorWrapperUnaryInterceptor wraps unknown errors with opaque internal errors
// containing a random error ID and logs the details
func ErrorWrapperUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			err = wrapError(info.FullMethod, err)
		}
		return resp, err
	}
}

// ErrorWrapperStreamInterceptor wraps unknown errors with opaque internal errors
// containing a random error ID and logs the details for streaming RPCs
func ErrorWrapperStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, ss)
		if err != nil {
			err = wrapError(info.FullMethod, err)
		}
		return err
	}
}

// wrapError checks if the error is already a gRPC status error with a known code.
// If not (or if it's an Internal/Unknown error), it wraps it with an opaque error
// containing a random error ID and logs the original error.
func wrapError(method string, err error) error {
	if err == nil {
		return nil
	}

	// Check if error is already a gRPC status error
	st, ok := status.FromError(err)
	if ok {
		// If it's a known error code (not Internal or Unknown), return as-is
		code := st.Code()
		if code != codes.Unknown && code != codes.Internal {
			return err
		}
	}

	// Generate a random error ID
	errorID := rand.Int31()

	// Log the method name, error ID, and original error
	log.Printf("GRPC Error - Method: %s, ErrorID: %d, Error: %v", method, errorID, err)

	// Return an opaque internal error with the error ID
	return status.Errorf(codes.Internal, "Internal server error (error ID: %d)", errorID)
}
