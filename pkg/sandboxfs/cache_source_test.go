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

package sandboxfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPCacheSourceNotFound tests that 404 errors are handled correctly
func TestHTTPCache(t *testing.T) {
	if os.Getenv("BACKEND_TESTING") == "" {
		t.Skip("backend integration tests disabled; enable with -backend-testing or set BACKEND_TESTING=1")
	}
	baseURL := "https://cas.velda.io?min_size=1048576"

	cacheSource := NewHTTPCacheSource(baseURL)
	ctx := context.Background()

	reader, _, err := cacheSource.Fetch(ctx, 4439912, "787c955dce49091ead850e4536666594095ea9f92a8a08879d8ddad466674657")
	require.NoError(t, err)
	defer func() {
		if c, ok := reader.(interface{ Close() error }); ok {
			require.NoError(t, c.Close())
		}
	}()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	expected := "787c955dce49091ead850e4536666594095ea9f92a8a08879d8ddad466674657"
	assert.Equal(t, expected, got)
}
