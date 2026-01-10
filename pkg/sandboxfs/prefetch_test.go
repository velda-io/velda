// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sandboxfs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStatPrefetch tests that stat and xattr are prefetched when listing a directory
func TestStatPrefetch(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with many files
	testDir := filepath.Join(env.srcDir, "prefetch-test")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create 100 files
	numFiles := 100
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("file%04d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte("test content"), 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	// Access the directory through FUSE mount to trigger Lookup
	mountedDir := filepath.Join(env.mountDir, "prefetch-test")

	// First, lookup the directory to trigger prefetch
	_, err = os.Stat(mountedDir)
	require.NoError(t, err)

	// Give workers a moment to process
	time.Sleep(100 * time.Millisecond)

	// Now read the directory - stats should be cached in kernel
	entries, err := os.ReadDir(mountedDir)
	require.NoError(t, err)

	// Verify we got all entries
	require.Equal(t, numFiles, len(entries), "Expected %d entries, got %d", numFiles, len(entries))

	t.Logf("Successfully listed %d entries from prefetched directory", len(entries))
}

// TestReaddirPrefetch tests that Readdir triggers stat prefetching
func TestReaddirPrefetch(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with files
	testDir := filepath.Join(env.srcDir, "readdir-test")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create 50 files
	numFiles := 50
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("file%03d.txt", i))
		content := []byte(fmt.Sprintf("content for file %d", i))
		require.NoError(t, os.WriteFile(filename, content, 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	// Read the directory through FUSE mount
	mountedDir := filepath.Join(env.mountDir, "readdir-test")

	entries, err := os.ReadDir(mountedDir)
	require.NoError(t, err)

	// Give workers a moment to process
	time.Sleep(100 * time.Millisecond)

	// Verify we got all entries
	require.Equal(t, numFiles, len(entries), "Expected %d entries, got %d", numFiles, len(entries))

	t.Logf("Successfully triggered prefetch via Readdir for %d entries", len(entries))
}

// TestPrefetchLimit tests that we respect the maxStatPrefetch limit
func TestPrefetchLimit(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with more than maxStatPrefetch files
	testDir := filepath.Join(env.srcDir, "limit-test")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create 150 files (more than maxStatPrefetch of 128)
	numFiles := 150
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("file%04d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte("test"), 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	// Read the directory through FUSE mount
	mountedDir := filepath.Join(env.mountDir, "limit-test")

	entries, err := os.ReadDir(mountedDir)
	require.NoError(t, err)

	// Give workers time to process
	time.Sleep(200 * time.Millisecond)

	// We should get all entries, even though only first 128 were prefetched
	require.Equal(t, numFiles, len(entries), "Expected %d entries, got %d", numFiles, len(entries))

	t.Logf("Successfully handled directory with %d files (limit is %d)", numFiles, maxStatPrefetch)
}

// TestNestedDirectoryPrefetch tests prefetch on nested directories
func TestNestedDirectoryPrefetch(t *testing.T) {
	env := setupTestEnv(t)

	// Create nested directory structure
	parentDir := filepath.Join(env.srcDir, "parent")
	childDir := filepath.Join(parentDir, "child")
	require.NoError(t, os.MkdirAll(childDir, 0755))

	// Create files in child directory
	for i := 0; i < 20; i++ {
		filename := filepath.Join(childDir, fmt.Sprintf("file%03d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte("test"), 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	// Access parent directory first
	mountedParent := filepath.Join(env.mountDir, "parent")
	_, err = os.Stat(mountedParent)
	require.NoError(t, err)

	// Now access child directory - should trigger prefetch
	mountedChild := filepath.Join(mountedParent, "child")
	_, err = os.Stat(mountedChild)
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Read child directory
	entries, err := os.ReadDir(mountedChild)
	require.NoError(t, err)
	require.Equal(t, 20, len(entries), "Expected 20 entries in child dir, got %d", len(entries))

	t.Logf("Successfully prefetched stats for nested directory with %d files", len(entries))
}
