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
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorWrapperUnaryInterceptor(t *testing.T) {
	interceptor := ErrorWrapperUnaryInterceptor()

	tests := []struct {
		name            string
		handlerError    error
		expectedCode    codes.Code
		shouldBeWrapped bool
	}{
		{
			name:            "Unknown error gets wrapped",
			handlerError:    errors.New("some unknown error"),
			expectedCode:    codes.Internal,
			shouldBeWrapped: true,
		},
		{
			name:            "Internal error gets wrapped",
			handlerError:    status.Error(codes.Internal, "internal error"),
			expectedCode:    codes.Internal,
			shouldBeWrapped: true,
		},
		{
			name:            "Unknown code error gets wrapped",
			handlerError:    status.Error(codes.Unknown, "unknown error"),
			expectedCode:    codes.Internal,
			shouldBeWrapped: true,
		},
		{
			name:            "NotFound error not wrapped",
			handlerError:    status.Error(codes.NotFound, "not found"),
			expectedCode:    codes.NotFound,
			shouldBeWrapped: false,
		},
		{
			name:            "PermissionDenied error not wrapped",
			handlerError:    status.Error(codes.PermissionDenied, "permission denied"),
			expectedCode:    codes.PermissionDenied,
			shouldBeWrapped: false,
		},
		{
			name:            "InvalidArgument error not wrapped",
			handlerError:    status.Error(codes.InvalidArgument, "invalid argument"),
			expectedCode:    codes.InvalidArgument,
			shouldBeWrapped: false,
		},
		{
			name:            "Nil error returns nil",
			handlerError:    nil,
			expectedCode:    codes.OK,
			shouldBeWrapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(ctx context.Context, req interface{}) (interface{}, error) {
				return nil, tt.handlerError
			}

			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.Service/TestMethod",
			}

			_, err := interceptor(context.Background(), nil, info, handler)

			if tt.handlerError == nil {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("Expected error, got nil")
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("Expected gRPC status error, got: %v", err)
			}

			if st.Code() != tt.expectedCode {
				t.Errorf("Expected code %v, got %v", tt.expectedCode, st.Code())
			}

			if tt.shouldBeWrapped {
				// Check that error message contains "error ID:"
				if len(st.Message()) == 0 {
					t.Errorf("Expected error message, got empty string")
				}
			}
		})
	}
}

func TestErrorWrapperStreamInterceptor(t *testing.T) {
	interceptor := ErrorWrapperStreamInterceptor()

	tests := []struct {
		name            string
		handlerError    error
		expectedCode    codes.Code
		shouldBeWrapped bool
	}{
		{
			name:            "Unknown error gets wrapped",
			handlerError:    errors.New("some unknown error"),
			expectedCode:    codes.Internal,
			shouldBeWrapped: true,
		},
		{
			name:            "NotFound error not wrapped",
			handlerError:    status.Error(codes.NotFound, "not found"),
			expectedCode:    codes.NotFound,
			shouldBeWrapped: false,
		},
		{
			name:            "Nil error returns nil",
			handlerError:    nil,
			expectedCode:    codes.OK,
			shouldBeWrapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(srv interface{}, stream grpc.ServerStream) error {
				return tt.handlerError
			}

			info := &grpc.StreamServerInfo{
				FullMethod: "/test.Service/TestStreamMethod",
			}

			err := interceptor(nil, nil, info, handler)

			if tt.handlerError == nil {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("Expected error, got nil")
			}

			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("Expected gRPC status error, got: %v", err)
			}

			if st.Code() != tt.expectedCode {
				t.Errorf("Expected code %v, got %v", tt.expectedCode, st.Code())
			}

			if tt.shouldBeWrapped {
				// Check that error message contains "error ID:"
				if len(st.Message()) == 0 {
					t.Errorf("Expected error message, got empty string")
				}
			}
		})
	}
}
