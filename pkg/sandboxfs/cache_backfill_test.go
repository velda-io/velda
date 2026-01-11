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
//go:build linux

package sandboxfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestBackfillCacheKeys(t *testing.T) {
	// Create temporary directory for testing
	testDir := t.TempDir()

	// Create test files
	testFile1 := filepath.Join(testDir, "file1.txt")
	testFile2 := filepath.Join(testDir, "file2.txt")
	subDir := filepath.Join(testDir, "subdir")
	testFile3 := filepath.Join(subDir, "file3.txt")

	require.NoError(t, os.WriteFile(testFile1, []byte("test content 1"), 0644))
	require.NoError(t, os.WriteFile(testFile2, []byte("test content 2"), 0644))
	require.NoError(t, os.Mkdir(subDir, 0755))
	require.NoError(t, os.WriteFile(testFile3, []byte("test content 3"), 0644))

	xattrName := "user.veldafs.cache"

	// Track progress
	var totalProcessed, totalUpdated int
	progressCallback := func(path string, updated bool, err error) {
		totalProcessed++
		if updated {
			totalUpdated++
		}
		require.NoError(t, err, "Should not have errors processing %s", path)
	}

	// Run backfill cache keys
	err := BackfillCacheKeys(testDir, xattrName, progressCallback)
	require.NoError(t, err)

	// Verify results
	assert.Equal(t, 3, totalProcessed, "Should process 3 files")
	assert.Equal(t, 3, totalUpdated, "Should update 3 files")

	// Verify xattrs were set on all files
	for _, file := range []string{testFile1, testFile2, testFile3} {
		var buf [128]byte
		sz, err := unix.Getxattr(file, xattrName, buf[:])
		require.NoError(t, err, "xattr should be set on %s", file)
		assert.Greater(t, sz, 0, "xattr should have content")

		// Verify xattr can be decoded
		xattrValue := string(buf[:sz])
		sha256sum, mtime, size, ok := decodeCacheXattr(xattrValue)
		assert.True(t, ok, "xattr should decode successfully")
		assert.NotEmpty(t, sha256sum, "SHA256 should not be empty")
		assert.False(t, mtime.IsZero(), "mtime should not be zero")
		assert.Greater(t, size, int64(0), "size should be positive")
	}

	// Run again - should skip all files
	totalProcessed = 0
	totalUpdated = 0
	err = BackfillCacheKeys(testDir, xattrName, progressCallback)
	require.NoError(t, err)

	assert.Equal(t, 3, totalProcessed, "Should process 3 files again")
	assert.Equal(t, 0, totalUpdated, "Should skip all files (already up-to-date)")
}

func TestBackfillCacheKeyForFile(t *testing.T) {
	testDir := t.TempDir()

	testFile := filepath.Join(testDir, "test.txt")
	testContent := []byte("test content for cache")
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	xattrName := "user.veldafs.cache"

	// First call should update the xattr
	updated, err := backfillCacheKeyForFile(testFile, xattrName)
	require.NoError(t, err)
	assert.True(t, updated, "First call should update xattr")

	// Verify xattr was set
	var buf [128]byte
	sz, err := unix.Getxattr(testFile, xattrName, buf[:])
	require.NoError(t, err)
	assert.Greater(t, sz, 0)

	// Decode and verify
	xattrValue := string(buf[:sz])
	sha256sum, mtime, size, ok := decodeCacheXattr(xattrValue)
	assert.True(t, ok)
	assert.NotEmpty(t, sha256sum)
	assert.False(t, mtime.IsZero())
	assert.Equal(t, int64(len(testContent)), size)

	// Second call should skip (xattr already valid)
	updated, err = backfillCacheKeyForFile(testFile, xattrName)
	require.NoError(t, err)
	assert.False(t, updated, "Second call should skip (already up-to-date)")
}
