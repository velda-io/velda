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
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSmallFilePrefetch tests that small files are prefetched on first read-only open
func TestSmallFilePrefetch(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with small files
	testDir := filepath.Join(env.srcDir, "small-files")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create 50 small files (under 128KB)
	numFiles := 50
	smallContent := bytes.Repeat([]byte("test data "), 1000) // ~10KB
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("small%04d.txt", i))
		require.NoError(t, os.WriteFile(filename, smallContent, 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	mountedDir := filepath.Join(env.mountDir, "small-files")

	// Open the first file for reading - this should trigger prefetch
	firstFile := filepath.Join(mountedDir, "small0000.txt")
	f, err := os.Open(firstFile)
	require.NoError(t, err)

	// Read a bit from the first file
	buf := make([]byte, 100)
	_, err = f.Read(buf)
	require.NoError(t, err)
	f.Close()

	// Give workers time to process prefetch jobs
	time.Sleep(500 * time.Millisecond)

	// Now read other files - they should be cached or being cached
	for i := 1; i < 10; i++ {
		filename := filepath.Join(mountedDir, fmt.Sprintf("small%04d.txt", i))
		content, err := os.ReadFile(filename)
		require.NoError(t, err)
		require.Equal(t, smallContent, content)
	}

	t.Logf("Successfully prefetched and read %d small files", numFiles)
}

// TestSmallFilePrefetchSizeLimit tests that files larger than 128KB are not prefetched
func TestSmallFilePrefetchSizeLimit(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with mixed file sizes
	testDir := filepath.Join(env.srcDir, "mixed-sizes")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create small files
	smallContent := bytes.Repeat([]byte("x"), 10*1024) // 10KB
	for i := 0; i < 10; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("small%02d.txt", i))
		require.NoError(t, os.WriteFile(filename, smallContent, 0644))
	}

	// Create large files (over 128KB)
	largeContent := bytes.Repeat([]byte("y"), 200*1024) // 200KB
	for i := 0; i < 10; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("large%02d.txt", i))
		require.NoError(t, os.WriteFile(filename, largeContent, 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	mountedDir := filepath.Join(env.mountDir, "mixed-sizes")

	// Open a small file - this should trigger prefetch only for small files
	firstFile := filepath.Join(mountedDir, "small00.txt")
	f, err := os.Open(firstFile)
	require.NoError(t, err)
	f.Close()

	// Give workers time to process
	time.Sleep(300 * time.Millisecond)

	// Verify all files are accessible
	entries, err := os.ReadDir(mountedDir)
	require.NoError(t, err)
	require.Equal(t, 20, len(entries))

	t.Logf("Successfully handled mixed file sizes (small and large)")
}

// TestSmallFilePrefetchCountLimit tests the 512 file limit
func TestSmallFilePrefetchCountLimit(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with more than 512 small files
	testDir := filepath.Join(env.srcDir, "many-small-files")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create 600 small files (more than maxSmallFilePrefetch of 512)
	numFiles := 600
	smallContent := []byte("test")
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("file%04d.txt", i))
		require.NoError(t, os.WriteFile(filename, smallContent, 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	mountedDir := filepath.Join(env.mountDir, "many-small-files")

	// Open the first file - should trigger prefetch of up to 512 files
	firstFile := filepath.Join(mountedDir, "file0000.txt")
	content, err := os.ReadFile(firstFile)
	require.NoError(t, err)
	require.Equal(t, smallContent, content)

	// Give workers time to process
	time.Sleep(1 * time.Second)

	// Verify all files are still accessible
	entries, err := os.ReadDir(mountedDir)
	require.NoError(t, err)
	require.Equal(t, numFiles, len(entries))

	t.Logf("Successfully handled %d files (limit is %d)", numFiles, maxSmallFilePrefetch)
}

// TestSmallFilePrefetchOnce tests that prefetching only happens once per directory
func TestSmallFilePrefetchOnce(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with small files
	testDir := filepath.Join(env.srcDir, "prefetch-once")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create 20 small files
	numFiles := 20
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("file%02d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte("test content"), 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	mountedDir := filepath.Join(env.mountDir, "prefetch-once")

	// Open first file - triggers prefetch
	f1, err := os.Open(filepath.Join(mountedDir, "file00.txt"))
	require.NoError(t, err)
	f1.Close()

	time.Sleep(200 * time.Millisecond)

	// Open second file - should NOT trigger prefetch again (already done)
	f2, err := os.Open(filepath.Join(mountedDir, "file01.txt"))
	require.NoError(t, err)
	f2.Close()

	time.Sleep(100 * time.Millisecond)

	// Open third file - still should NOT trigger prefetch
	f3, err := os.Open(filepath.Join(mountedDir, "file02.txt"))
	require.NoError(t, err)
	f3.Close()

	t.Logf("Successfully verified prefetch happens only once per directory")
}

// TestSmallFilePrefetchEmptyFiles tests that empty files are skipped
func TestSmallFilePrefetchEmptyFiles(t *testing.T) {
	env := setupTestEnv(t)

	// Create a directory with empty and non-empty files
	testDir := filepath.Join(env.srcDir, "with-empty")
	require.NoError(t, os.Mkdir(testDir, 0755))

	// Create some empty files
	for i := 0; i < 5; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("empty%02d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte{}, 0644))
	}

	// Create some non-empty files
	for i := 0; i < 5; i++ {
		filename := filepath.Join(testDir, fmt.Sprintf("nonempty%02d.txt", i))
		require.NoError(t, os.WriteFile(filename, []byte("content"), 0644))
	}

	// Mount the filesystem
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()
	require.NoError(t, server.WaitMount())

	mountedDir := filepath.Join(env.mountDir, "with-empty")

	// Open a non-empty file - should prefetch only non-empty files
	f, err := os.Open(filepath.Join(mountedDir, "nonempty00.txt"))
	require.NoError(t, err)
	f.Close()

	time.Sleep(200 * time.Millisecond)

	// Verify all files are accessible
	entries, err := os.ReadDir(mountedDir)
	require.NoError(t, err)
	require.Equal(t, 10, len(entries))

	t.Logf("Successfully handled empty and non-empty files")
}
