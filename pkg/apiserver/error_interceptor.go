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
	"errors"
	"log"
	"math/rand/v2"

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

	// Search the error chain for an underlying gRPC status error and keep
	// the deepest (most specific) one we find. When returning a status
	// error we always return a fresh status error (no wrapped chain) so
	// callers don't see internal context added elsewhere in the chain.
	var deepest *status.Status
	for e := err; e != nil; e = errors.Unwrap(e) {
		if st, ok := status.FromError(e); ok {
			deepest = st
		}
	}
	if deepest != nil {
		code := deepest.Code()
		// If it's a known error code (not Internal or Unknown), return a
		// clean status error with the same code and message (no chain).
		if code != codes.Unknown && code != codes.Internal {
			return status.Error(code, deepest.Message())
		}
		// If the deepest status is Internal/Unknown, fall through and let
		// the wrapper produce an opaque Internal error ID instead of
		// returning the chained error.
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, context.Canceled.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	}

	// Generate a random error ID
	errorID := rand.Uint32()

	// Log the method name, error ID, and original error
	log.Printf("GRPC Error - Method: %s, ErrorID: %d, Error: %v", method, errorID, err)

	// Return an opaque internal error with the error ID
	return status.Errorf(codes.Internal, "Internal server error (error ID: %d)", errorID)
}
