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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWrapError_UnwrapsDeepestStatusAndRemovesChain(t *testing.T) {
	t.Run("returns clean status for deepest known code", func(t *testing.T) {
		deep := status.Error(codes.NotFound, "resource missing")
		wrapped := fmt.Errorf("level1: %w", fmt.Errorf("level2: %w", deep))

		got := wrapError("/svc.Svc/Method", wrapped)

		st, ok := status.FromError(got)
		require.True(t, ok, "expected a status error, got: %v", got)
		require.Equal(t, codes.NotFound, st.Code())
		require.Equal(t, "resource missing", st.Message())
		require.Nil(t, errors.Unwrap(got))
	})

	t.Run("internal deepest status yields opaque internal without original message", func(t *testing.T) {
		deep := status.Error(codes.Internal, "db boom")
		wrapped := fmt.Errorf("wrap: %w", deep)

		got := wrapError("/svc.Svc/Method", wrapped)

		st, ok := status.FromError(got)
		require.True(t, ok, "expected a status error, got: %v", got)
		require.Equal(t, codes.Internal, st.Code())
		// Should be our opaque internal message, not the original "db boom" text
		assert.NotContains(t, st.Message(), "db boom", "opaque internal message leaked original message: %q", st.Message())
		assert.True(t, strings.HasPrefix(st.Message(), "Internal server error (error ID:"), "expected opaque internal message, got %q", st.Message())
		require.Nil(t, errors.Unwrap(got))
	})

	t.Run("Unknown error shall be internalized", func(t *testing.T) {
		deep := errors.New("db boom")
		wrapped := fmt.Errorf("wrap: %w", deep)

		got := wrapError("/svc.Svc/Method", wrapped)

		st, ok := status.FromError(got)
		require.True(t, ok, "expected a status error, got: %v", got)
		require.Equal(t, codes.Internal, st.Code())
		// Should be our opaque internal message, not the original "db boom" text
		assert.NotContains(t, st.Message(), "db boom", "opaque internal message leaked original message: %q", st.Message())
		assert.True(t, strings.HasPrefix(st.Message(), "Internal server error (error ID:"), "expected opaque internal message, got %q", st.Message())
		require.Nil(t, errors.Unwrap(got))
	})

	t.Run("preserves context.Canceled when wrapped", func(t *testing.T) {
		wrapped := fmt.Errorf("wrapper: %w", context.Canceled)

		got := wrapError("/svc.Svc/Method", wrapped)
		st, ok := status.FromError(got)
		require.True(t, ok, "expected a status error, got: %v", got)
		require.Equal(t, codes.Canceled, st.Code())
		require.Equal(t, context.Canceled.Error(), st.Message())
	})
}
