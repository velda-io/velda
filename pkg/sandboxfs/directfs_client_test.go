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
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"kernel.org/pub/linux/libs/security/libcap/cap"
	"velda.io/velda/pkg/fileserver"
)

var cacheKey = xattrCacheName

// checkCapSysAdmin checks if the current process has CAP_SYS_ADMIN capability
func checkCapSysAdmin(t *testing.T) {
	caps := cap.GetProc()
	if caps == nil {
		t.Skip("Unable to get process capabilities")
	}

	if ok, err := caps.GetFlag(cap.Effective, cap.SYS_ADMIN); err != nil || !ok {
		t.Skip("Test requires CAP_SYS_ADMIN capability. Run with: ./test_snapshot.sh")
	}
}

// setupTestServerClient starts a fileserver on a random port and returns the
// server, client, veldaServer, cache manager and a cleanup function.
func setupTestServerClient(t *testing.T, srcDir, cacheDir, mountDir string, workers int) (*fileserver.FileServer, *DirectFSClient, *VeldaServer, *DirectoryCacheManager) {
	server := fileserver.NewFileServer(srcDir, workers)
	require.NoError(t, server.Start("localhost:0"))

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	cache, err := NewDirectoryCacheManager(cacheDir)
	require.NoError(t, err)

	client := NewDirectFSClient(server.Addr().String(), cache, false)

	veldaServer, err := client.Mount(mountDir)
	require.NoError(t, err)
	require.NoError(t, veldaServer.WaitMount())
	// Give mount time to stabilize
	time.Sleep(200 * time.Millisecond)

	t.Cleanup(func() {
		require.NoError(t, client.Unmount())
		client.Stop()
		server.Stop()
	})
	return server, client, veldaServer, cache
}

// setupTestServerClientWithCache is like setupTestServerClient but reuses an
// existing cache manager (useful when tests pre-populate cache before server).
func setupTestServerClientWithCache(t *testing.T, srcDir string, cache *DirectoryCacheManager, mountDir string, workers int) (*fileserver.FileServer, *DirectFSClient, *VeldaServer) {
	server := fileserver.NewFileServer(srcDir, workers)
	require.NoError(t, server.Start("localhost:0"))

	time.Sleep(100 * time.Millisecond)

	client := NewDirectFSClient(server.Addr().String(), cache, false)

	veldaServer, err := client.Mount(mountDir)
	require.NoError(t, err)
	require.NoError(t, veldaServer.WaitMount())
	time.Sleep(200 * time.Millisecond)

	t.Cleanup(func() {
		require.NoError(t, client.Unmount())
		client.Stop()
		server.Stop()
	})
	return server, client, veldaServer
}

// TestSnapshotClientE2E tests the full end-to-end flow of server and client
func TestSnapshotClientE2E(t *testing.T) {
	checkCapSysAdmin(t)
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Create test directories
	testDir := t.TempDir()
	srcDir := filepath.Join(testDir, "src")
	cacheDir := filepath.Join(testDir, "cache")
	mountDir := filepath.Join(testDir, "mount")

	var cache *DirectoryCacheManager

	require.NoError(t, os.MkdirAll(srcDir, 0755))
	require.NoError(t, os.MkdirAll(cacheDir, 0755))
	require.NoError(t, os.MkdirAll(mountDir, 0755))

	GlobalCacheMetrics = NewCacheMetrics()
	// Create test files in source directory
	testContent1 := []byte("Hello, World!")
	testFile1 := filepath.Join(srcDir, "test1.txt")
	require.NoError(t, os.WriteFile(testFile1, testContent1, 0644))

	// Compute SHA256 for test file 1
	hash1 := sha256.Sum256(testContent1)
	sha256Hex1 := hex.EncodeToString(hash1[:])

	testContent2 := []byte("Second test file with different content")
	testFile2 := filepath.Join(srcDir, "test2.txt")
	require.NoError(t, os.WriteFile(testFile2, testContent2, 0644))

	// Create a subdirectory with a file
	subDir := filepath.Join(srcDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	testContent3 := []byte("File in subdirectory")
	testFile3 := filepath.Join(subDir, "test3.txt")
	require.NoError(t, os.WriteFile(testFile3, testContent3, 0644))

	hash3 := sha256.Sum256(testContent3)
	sha256Hex3 := hex.EncodeToString(hash3[:])

	require.NoError(t, BackfillCacheKeys(srcDir, cacheKey, func(path string, updated bool, err error) {
		log.Printf("Backfill progress: %s (updated: %t, error: %v)\n", path, updated, err)
	}))

	// Start server and client using helper (random port)
	_, _, _, cache = setupTestServerClient(t, srcDir, cacheDir, mountDir, 4)

	t.Run("read file from mount", func(t *testing.T) {
		// Read the first file through the mount
		mountedFile1 := filepath.Join(mountDir, "test1.txt")
		content, err := os.ReadFile(mountedFile1)
		require.NoError(t, err, "Failed to read file from mount")
		assert.Equal(t, testContent1, content, "File content mismatch")
	})

	t.Run("verify file is cached", func(t *testing.T) {
		// Check that file is now in cache
		cachedPath, err := cache.Lookup(sha256Hex1)
		require.NoError(t, err)

		// Wait a bit for async cache write to complete
		time.Sleep(100 * time.Millisecond)

		// Try again
		cachedPath, err = cache.Lookup(sha256Hex1)
		require.NoError(t, err)
		assert.NotEmpty(t, cachedPath, "File should be cached")

		// Read from cache and verify content
		cachedContent, err := os.ReadFile(cachedPath)
		require.NoError(t, err)
		assert.Equal(t, testContent1, cachedContent, "Cached content mismatch")
	})

	t.Run("read second file", func(t *testing.T) {
		mountedFile2 := filepath.Join(mountDir, "test2.txt")
		content, err := os.ReadFile(mountedFile2)
		require.NoError(t, err)
		assert.Equal(t, testContent2, content)
	})

	t.Run("list directory", func(t *testing.T) {
		entries, err := os.ReadDir(mountDir)
		require.NoError(t, err)
		assert.Len(t, entries, 3, "Should have 3 entries (2 files + 1 dir)")

		names := make(map[string]bool)
		for _, entry := range entries {
			names[entry.Name()] = true
		}

		assert.True(t, names["test1.txt"], "test1.txt should be in directory")
		assert.True(t, names["test2.txt"], "test2.txt should be in directory")
		assert.True(t, names["subdir"], "subdir should be in directory")
	})

	t.Run("read file from subdirectory", func(t *testing.T) {
		mountedFile3 := filepath.Join(mountDir, "subdir", "test3.txt")
		content, err := os.ReadFile(mountedFile3)
		require.NoError(t, err)
		assert.Equal(t, testContent3, content)

		// Verify it gets cached too
		time.Sleep(100 * time.Millisecond)
		cachedPath, err := cache.Lookup(sha256Hex3)
		require.NoError(t, err)
		assert.NotEmpty(t, cachedPath, "Subdirectory file should be cached")
	})

	t.Run("second read from cache", func(t *testing.T) {
		// Read test1.txt again - should come from cache
		initialCacheHits := getCounterValue(GlobalCacheMetrics.CacheHit)

		mountedFile1 := filepath.Join(mountDir, "test1.txt")
		content, err := os.ReadFile(mountedFile1)
		require.NoError(t, err)
		assert.Equal(t, testContent1, content)

		// Check cache hit metric increased
		newCacheHits := getCounterValue(GlobalCacheMetrics.CacheHit)
		assert.Greater(t, newCacheHits, initialCacheHits, "Cache hit should have increased")
	})

	t.Run("verify file attributes", func(t *testing.T) {
		mountedFile1 := filepath.Join(mountDir, "test1.txt")
		info, err := os.Stat(mountedFile1)
		require.NoError(t, err)

		assert.Equal(t, int64(len(testContent1)), info.Size(), "File size mismatch")
		assert.False(t, info.IsDir(), "File should not be a directory")
		assert.Equal(t, os.FileMode(0644), info.Mode().Perm(), "File permissions mismatch")
	})

	t.Run("write operations return EROFS", func(t *testing.T) {
		// Try to write to a file
		mountedFile1 := filepath.Join(mountDir, "test1.txt")
		err := os.WriteFile(mountedFile1, []byte("new content"), 0644)
		assert.Error(t, err)
		assert.ErrorIs(t, err, syscall.EROFS, "Write should return EROFS")

		// Try to create a new file
		newFile := filepath.Join(mountDir, "newfile.txt")
		err = os.WriteFile(newFile, []byte("content"), 0644)
		assert.Error(t, err)
		assert.ErrorIs(t, err, syscall.EROFS, "Create should return EROFS")

		// Try to mkdir
		newDir := filepath.Join(mountDir, "newdir")
		err = os.Mkdir(newDir, 0755)
		assert.Error(t, err)
		assert.ErrorIs(t, err, syscall.EROFS, "Mkdir should return EROFS")

		// Try to remove a file
		err = os.Remove(mountedFile1)
		assert.Error(t, err)
		assert.ErrorIs(t, err, syscall.EROFS, "Remove should return EROFS")

		// Try to chmod
		err = os.Chmod(mountedFile1, 0777)
		assert.Error(t, err)
		assert.ErrorIs(t, err, syscall.EROFS, "Chmod should return EROFS")
	})

	t.Run("cache metrics", func(t *testing.T) {
		// Verify cache metrics are being tracked
		cacheHits := getCounterValue(GlobalCacheMetrics.CacheHit)
		cacheSaved := getCounterValue(GlobalCacheMetrics.CacheSaved)

		assert.Greater(t, cacheHits, float64(0), "Should have cache hits")
		assert.Greater(t, cacheSaved, float64(0), "Should have saved to cache")
	})
}

// TestSnapshotClientCachePassthrough tests that cached files use passthrough mode
func TestSnapshotClientCachePassthrough(t *testing.T) {
	checkCapSysAdmin(t)

	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Create test directories
	testDir := t.TempDir()
	srcDir := filepath.Join(testDir, "src")
	cacheDir := filepath.Join(testDir, "cache")
	mountDir := filepath.Join(testDir, "mount")

	require.NoError(t, os.MkdirAll(srcDir, 0755))
	require.NoError(t, os.MkdirAll(cacheDir, 0755))
	require.NoError(t, os.MkdirAll(mountDir, 0755))

	// Create a test file with SHA256
	testContent := []byte("Cached content for passthrough test")
	testFile := filepath.Join(srcDir, "cached.txt")
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	hash := sha256.Sum256(testContent)
	sha256Hex := hex.EncodeToString(hash[:])
	_, err := backfillCacheKeyForFile(testFile, cacheKey)
	require.NoError(t, err)

	// Pre-populate the cache
	cache, err := NewDirectoryCacheManager(cacheDir)
	require.NoError(t, err)

	writer, err := cache.CreateTemp()
	require.NoError(t, err)
	_, err = writer.Write(testContent)
	require.NoError(t, err)
	savedHash, err := writer.Commit()
	require.NoError(t, err)
	assert.Equal(t, sha256Hex, savedHash)

	// Start server and client (reusing existing pre-populated cache)
	setupTestServerClientWithCache(t, srcDir, cache, mountDir, 2)

	t.Run("open cached file uses passthrough", func(t *testing.T) {
		initialCacheHits := getCounterValue(GlobalCacheMetrics.CacheHit)

		// Open and read the file - should use passthrough since it's already cached
		mountedFile := filepath.Join(mountDir, "cached.txt")
		content, err := os.ReadFile(mountedFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)

		// Cache hit should have occurred during open
		newCacheHits := getCounterValue(GlobalCacheMetrics.CacheHit)
		assert.Greater(t, newCacheHits, initialCacheHits, "Should have cache hit from passthrough open")
	})
}

// TestSnapshotClientTimestamps tests that nanosecond timestamps are preserved
func TestSnapshotClientTimestamps(t *testing.T) {
	checkCapSysAdmin(t)

	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Create test directories
	testDir := t.TempDir()
	srcDir := filepath.Join(testDir, "src")
	cacheDir := filepath.Join(testDir, "cache")
	mountDir := filepath.Join(testDir, "mount")

	require.NoError(t, os.MkdirAll(srcDir, 0755))
	require.NoError(t, os.MkdirAll(cacheDir, 0755))
	require.NoError(t, os.MkdirAll(mountDir, 0755))

	// Create a test file
	testContent := []byte("Timestamp test")
	testFile := filepath.Join(srcDir, "timestamp.txt")
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	// Get the original stat
	var srcStat unix.Stat_t
	require.NoError(t, unix.Stat(testFile, &srcStat))

	// Start server
	server := fileserver.NewFileServer(srcDir, 2)
	serverAddr := "localhost:0"
	err := server.Start(serverAddr)
	require.NoError(t, err)
	defer server.Stop()
	time.Sleep(100 * time.Millisecond)

	// Create cache and client
	cache, err := NewDirectoryCacheManager(cacheDir)
	require.NoError(t, err)

	client := NewDirectFSClient(server.Addr().String(), cache, false)

	veldaServer, err := client.Mount(mountDir)
	defer client.Stop()
	require.NoError(t, err)
	defer client.Unmount()
	require.NoError(t, veldaServer.WaitMount())
	time.Sleep(200 * time.Millisecond)

	t.Run("timestamps with nanoseconds preserved", func(t *testing.T) {
		mountedFile := filepath.Join(mountDir, "timestamp.txt")
		var mountStat unix.Stat_t
		require.NoError(t, unix.Stat(mountedFile, &mountStat))

		// Compare timestamps (seconds)
		assert.Equal(t, srcStat.Mtim.Sec, mountStat.Mtim.Sec, "Mtime seconds mismatch")
		assert.Equal(t, srcStat.Ctim.Sec, mountStat.Ctim.Sec, "Ctime seconds mismatch")

		// Compare nanoseconds
		assert.Equal(t, srcStat.Mtim.Nsec, mountStat.Mtim.Nsec, "Mtime nanoseconds mismatch")
		assert.Equal(t, srcStat.Ctim.Nsec, mountStat.Ctim.Nsec, "Ctime nanoseconds mismatch")
	})
}

// TestSnapshotClientReadlink tests readlink functionality
func TestSnapshotClientReadlink(t *testing.T) {
	checkCapSysAdmin(t)

	// Setup test directories
	srcDir := t.TempDir()
	cacheDir := t.TempDir()
	mountDir := t.TempDir()

	// Create a test file and symlinks
	testFile := filepath.Join(srcDir, "target.txt")
	testContent := []byte("target content")
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	// Create absolute symlink
	absSymlink := filepath.Join(srcDir, "abs_link")
	require.NoError(t, os.Symlink("/tmp/absolute_target", absSymlink))

	// Create relative symlink
	relSymlink := filepath.Join(srcDir, "rel_link")
	require.NoError(t, os.Symlink("target.txt", relSymlink))

	// Create nested directory with symlink
	nestedDir := filepath.Join(srcDir, "nested")
	require.NoError(t, os.Mkdir(nestedDir, 0755))
	nestedSymlink := filepath.Join(nestedDir, "nested_link")
	require.NoError(t, os.Symlink("../target.txt", nestedSymlink))

	// Start server and client
	setupTestServerClient(t, srcDir, cacheDir, mountDir, 2)

	t.Run("absolute symlink", func(t *testing.T) {
		mountedLink := filepath.Join(mountDir, "abs_link")
		target, err := os.Readlink(mountedLink)
		require.NoError(t, err)
		assert.Equal(t, "/tmp/absolute_target", target)

		// Verify lstat shows it's a symlink
		fi, err := os.Lstat(mountedLink)
		require.NoError(t, err)
		assert.Equal(t, os.ModeSymlink, fi.Mode()&os.ModeSymlink)
	})

	t.Run("relative symlink", func(t *testing.T) {
		mountedLink := filepath.Join(mountDir, "rel_link")
		target, err := os.Readlink(mountedLink)
		require.NoError(t, err)
		assert.Equal(t, "target.txt", target)

		// Verify the symlink can be followed
		content, err := os.ReadFile(mountedLink)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("nested symlink", func(t *testing.T) {
		mountedLink := filepath.Join(mountDir, "nested", "nested_link")
		target, err := os.Readlink(mountedLink)
		require.NoError(t, err)
		assert.Equal(t, "../target.txt", target)

		// Verify the symlink can be followed
		content, err := os.ReadFile(mountedLink)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("readlink on regular file fails", func(t *testing.T) {
		mountedFile := filepath.Join(mountDir, "target.txt")
		_, err := os.Readlink(mountedFile)
		assert.Error(t, err)
		assert.ErrorIs(t, err, syscall.EINVAL)
	})
}
