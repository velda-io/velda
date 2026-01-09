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
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func disallowOther(options *fs.Options) {
	options.MountOptions.AllowOther = false
	//options.Debug = true
}

// testEnv holds the test environment directories
type testEnv struct {
	rootDir  string
	cacheDir string
	srcDir   string
	mountDir string
}

// setupTestEnv creates a new test environment with all required directories
func setupTestEnv(t *testing.T) *testEnv {
	rootDir, err := os.MkdirTemp("", "velda-test-*")
	require.NoError(t, err)

	env := &testEnv{
		rootDir:  rootDir,
		cacheDir: filepath.Join(rootDir, "cache"),
		srcDir:   filepath.Join(rootDir, "src"),
		mountDir: filepath.Join(rootDir, "mount"),
	}

	require.NoError(t, os.MkdirAll(env.cacheDir, 0755))
	require.NoError(t, os.MkdirAll(env.srcDir, 0755))
	require.NoError(t, os.MkdirAll(env.mountDir, 0755))

	t.Cleanup(func() {
		os.RemoveAll(rootDir)
	})

	return env
}

// getCounterValue retrieves the current value of a Prometheus counter
func getCounterValue(counter prometheus.Counter) float64 {
	var m dto.Metric
	if err := counter.Write(&m); err != nil {
		return 0
	}
	return m.Counter.GetValue()
}

func TestCacheManager(t *testing.T) {
	env := setupTestEnv(t)

	cache, err := NewDirectoryCacheManager(env.cacheDir)
	require.NoError(t, err)

	t.Run("write and commit to cache", func(t *testing.T) {
		writer, err := cache.CreateTemp()
		require.NoError(t, err)

		testContent := []byte("Test content for caching")
		_, err = writer.Write(testContent)
		require.NoError(t, err)

		sha256sum, err := writer.Commit()
		require.NoError(t, err)
		assert.Len(t, sha256sum, 64)

		// Lookup the cached file
		cachedPath, err := cache.Lookup(sha256sum)
		require.NoError(t, err)
		assert.NotEmpty(t, cachedPath)

		// Verify content
		content, err := os.ReadFile(cachedPath)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("abort cache write", func(t *testing.T) {
		writer, err := cache.CreateTemp()
		require.NoError(t, err)

		_, err = writer.Write([]byte("This will be aborted"))
		require.NoError(t, err)

		err = writer.Abort()
		require.NoError(t, err)
	})

	t.Run("lookup non-existent cache", func(t *testing.T) {
		fakeSHA := "0000000000000000000000000000000000000000000000000000000000000000"
		cachedPath, err := cache.Lookup(fakeSHA)
		require.NoError(t, err)
		assert.Empty(t, cachedPath)
	})
}

func TestSequentialWriteCaching(t *testing.T) {
	env := setupTestEnv(t)

	cache, err := NewDirectoryCacheManager(env.cacheDir)
	require.NoError(t, err)

	t.Run("sequential writes are cached", func(t *testing.T) {
		writer, err := cache.CreateTemp()
		require.NoError(t, err)

		// Write sequentially
		for i := 0; i < 10; i++ {
			_, err = writer.Write([]byte("chunk"))
			require.NoError(t, err)
		}

		sha256sum, err := writer.Commit()
		require.NoError(t, err)

		// Verify cached
		cachedPath, err := cache.Lookup(sha256sum)
		require.NoError(t, err)
		assert.NotEmpty(t, cachedPath)
	})
}

func TestCachedFilesystem(t *testing.T) {
	env := setupTestEnv(t)

	// Create a test file in src directory
	testFile := filepath.Join(env.srcDir, "test.txt")
	testContent := []byte("Hello, World!")
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	// Mount the filesystem with cache
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	// Wait for mount to be ready
	require.NoError(t, server.WaitMount())

	t.Run("read file and populate cache", func(t *testing.T) {
		// Read the file through the mounted filesystem
		mountedFile := filepath.Join(env.mountDir, "test.txt")
		content, err := os.ReadFile(mountedFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)
	})

	t.Run("write file and cache", func(t *testing.T) {
		// Write a new file through the mounted filesystem
		newFile := filepath.Join(env.mountDir, "newfile.txt")
		newContent := []byte("This is a new file")
		require.NoError(t, os.WriteFile(newFile, newContent, 0644))

		// Read it back
		content, err := os.ReadFile(newFile)
		require.NoError(t, err)
		assert.Equal(t, newContent, content)
	})
}

// TestCacheMetrics tests that cache hit/miss/not-exist metrics are recorded correctly
func TestCacheMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	// Create test file
	testFile := filepath.Join(env.srcDir, "testfile.txt")
	testContent := []byte("Hello, cached world!")
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	// Mount with cache
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	mountedFile := filepath.Join(env.mountDir, "testfile.txt")

	// Record initial metric values
	initialNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
	initialHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	initialSaved := getCounterValue(GlobalCacheMetrics.CacheSaved)
	initialFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)

	// First read - should be CacheFetched (eager fetch with no xattr)
	content, err := os.ReadFile(mountedFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)

	// Wait for background eager fetch to complete
	time.Sleep(100 * time.Millisecond)

	newNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
	newFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)
	assert.Equal(t, initialNotExist+1, newNotExist, "CacheNotExist should increase by 1")
	assert.Equal(t, initialFetched+1, newFetched, "CacheFetched should increase by 1")

	// Write a file to trigger cache creation
	newFile := filepath.Join(env.mountDir, "newcached.txt")
	newContent := []byte("This will be cached")
	require.NoError(t, os.WriteFile(newFile, newContent, 0644))

	newSaved := getCounterValue(GlobalCacheMetrics.CacheSaved)
	assert.Equal(t, initialSaved+2, newSaved, "CacheSaved should increase by 2")

	// Second read of the newly created file - should be CacheHit
	content, err = os.ReadFile(newFile)
	require.NoError(t, err)
	assert.Equal(t, newContent, content)

	newHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	assert.Equal(t, initialHit+1, newHit, "CacheHit should increase by 1")
}

// TestEagerFetch tests that eager fetching works correctly for cache misses and stale cache
func TestEagerFetch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	// Mount with cache
	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	t.Run("eager fetch on cache miss (no xattr)", func(t *testing.T) {
		// Create test file
		testFile := filepath.Join(env.srcDir, "eager1.txt")
		testContent := []byte("Eager fetch content 1")
		require.NoError(t, os.WriteFile(testFile, testContent, 0644))

		mountedFile := filepath.Join(env.mountDir, "eager1.txt")

		initialNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
		initialFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)

		// Read file - should trigger eager fetch
		content, err := os.ReadFile(mountedFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)

		// Wait a bit for background worker to complete
		time.Sleep(100 * time.Millisecond)

		// Verify metrics
		newNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
		newFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)
		assert.Equal(t, initialNotExist+1, newNotExist, "CacheNotExist should increase")
		assert.Equal(t, initialFetched+1, newFetched, "CacheFetched should increase for eager fetch")

		// Verify xattr was set
		var buf [128]byte
		sz, err := unix.Getxattr(testFile, xattrCacheName, buf[:])
		require.NoError(t, err)
		assert.Greater(t, sz, 0, "xattr should be set after eager fetch")
	})

	t.Run("eager fetch on missing cache-key with old mtime", func(t *testing.T) {
		// Create test file with old mtime (2 days ago)
		testFile := filepath.Join(env.srcDir, "eager2.txt")
		testContent := []byte("Eager fetch content 2")
		require.NoError(t, os.WriteFile(testFile, testContent, 0644))

		// Set old mtime
		oldTime := time.Now().Add(-48 * time.Hour)
		require.NoError(t, os.Chtimes(testFile, oldTime, oldTime))

		// Manually set xattr with valid SHA but missing cache file
		fakeSHA := "1234567890123456789012345678901234567890123456789012345678901234"
		xattrValue := encodeCacheXattr(fakeSHA, oldTime, int64(len(testContent)))
		require.NoError(t, unix.Setxattr(testFile, xattrCacheName, []byte(xattrValue), 0))

		mountedFile := filepath.Join(env.mountDir, "eager2.txt")

		initialMiss := getCounterValue(GlobalCacheMetrics.CacheMiss)
		initialFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)

		// Read file - should trigger eager fetch because mtime is old
		content, err := os.ReadFile(mountedFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)

		// Wait a bit for background worker to complete
		time.Sleep(100 * time.Millisecond)

		// Verify metrics
		newMiss := getCounterValue(GlobalCacheMetrics.CacheMiss)
		newFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)
		assert.Equal(t, initialMiss+1, newMiss, "CacheMiss should increase")
		assert.Equal(t, initialFetched+1, newFetched, "CacheFetched should increase for eager fetch")

		// Verify xattr was updated with real SHA
		var buf [128]byte
		sz, err := unix.Getxattr(testFile, xattrCacheName, buf[:])
		require.NoError(t, err)
		xattrStr := string(buf[:sz])
		assert.NotContains(t, xattrStr, fakeSHA, "xattr should be updated with real SHA")
	})

	t.Run("no eager fetch on recent file with missing cache", func(t *testing.T) {
		// Create a subdirectory to isolate this test from prefetching sibling files
		testDir := filepath.Join(env.srcDir, "eager3-dir")
		require.NoError(t, os.Mkdir(testDir, 0755))

		// Create test file with recent mtime (1 hour ago)
		testFile := filepath.Join(testDir, "eager3.txt")
		testContent := []byte("Eager fetch content 3")
		require.NoError(t, os.WriteFile(testFile, testContent, 0644))

		// Set recent mtime
		recentTime := time.Now().Add(-1 * time.Hour)
		require.NoError(t, os.Chtimes(testFile, recentTime, recentTime))

		// Manually set xattr with valid SHA but missing cache file
		fakeSHA := "9876543210987654321098765432109876543210987654321098765432109876"
		xattrValue := encodeCacheXattr(fakeSHA, recentTime, int64(len(testContent)))
		require.NoError(t, unix.Setxattr(testFile, xattrCacheName, []byte(xattrValue), 0))

		mountedFile := filepath.Join(env.mountDir, "eager3-dir", "eager3.txt")

		initialMiss := getCounterValue(GlobalCacheMetrics.CacheMiss)
		initialFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)

		// Read file - should NOT trigger eager fetch because mtime is recent
		content, err := os.ReadFile(mountedFile)
		require.NoError(t, err)
		assert.Equal(t, testContent, content)

		// Verify metrics - no eager fetch should occur
		newMiss := getCounterValue(GlobalCacheMetrics.CacheMiss)
		newFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)
		assert.Equal(t, initialMiss+1, newMiss, "CacheMiss should increase")
		assert.Equal(t, initialFetched, newFetched, "CacheFetched should NOT increase for recent files")
	})
}

// TestRewriteFile tests rewriting a file with O_TRUNC
func TestRewriteFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	testFile := filepath.Join(env.mountDir, "rewrite.txt")

	// Write initial content
	initialContent := []byte("Initial content")
	require.NoError(t, os.WriteFile(testFile, initialContent, 0644))

	// Verify initial content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, initialContent, content)

	// Rewrite with different content (O_TRUNC)
	newContent := []byte("New content after rewrite")
	require.NoError(t, os.WriteFile(testFile, newContent, 0644))

	// Verify new content
	content, err = os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, newContent, content)

	// Check that xattr was invalidated and updated
	var buf [64]byte
	srcFile := filepath.Join(env.srcDir, "rewrite.txt")
	sz, err := unix.Getxattr(srcFile, xattrCacheName, buf[:])
	if err != nil {
		t.Logf("No xattr found after rewrite (expected if invalidated): %v", err)
	} else if sz == 64 {
		t.Logf("Xattr after rewrite: %s", string(buf[:64]))
	}
}

// TestReopenWithoutTruncate tests opening an existing file without O_TRUNC and appending
func TestReopenWithoutTruncate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	testFile := filepath.Join(env.mountDir, "append.txt")

	// Initial write
	initialContent := []byte("Initial")
	require.NoError(t, os.WriteFile(testFile, initialContent, 0644))

	// Reopen and append
	f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_APPEND, 0644)
	require.NoError(t, err)

	appendContent := []byte(" Appended")
	_, err = f.Write(appendContent)
	require.NoError(t, err)
	f.Close()

	// Verify combined content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "Initial Appended", string(content))
}

// TestNonSequentialWrite tests that non-sequential writes abort caching
func TestNonSequentialWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	testFile := filepath.Join(env.mountDir, "nonseq.txt")

	initialAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)

	// Write at offset 0
	_, err = f.WriteAt([]byte("start"), 0)
	require.NoError(t, err)

	// Write at non-sequential offset (should abort caching)
	_, err = f.WriteAt([]byte("end"), 100)
	require.NoError(t, err)
	f.Close()

	newAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)
	assert.Greater(t, newAborted, initialAborted, "CacheAborted should increase")

	// Verify xattr was NOT set (caching was aborted)
	var buf [64]byte
	srcFile := filepath.Join(env.srcDir, "nonseq.txt")
	_, err = unix.Getxattr(srcFile, xattrCacheName, buf[:])
	assert.Error(t, err, "Expected no xattr after non-sequential write")
}

// TestSeekAndWrite tests that seeking and writing aborts caching
func TestSeekAndWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	testFile := filepath.Join(env.mountDir, "seek.txt")

	initialAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)

	// Write sequentially
	_, err = f.Write([]byte("first part"))
	require.NoError(t, err)

	// Seek to earlier position
	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)

	// Write again (should abort caching due to non-sequential write)
	_, err = f.Write([]byte("overwrite"))
	require.NoError(t, err)
	f.Close()

	newAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)
	assert.Greater(t, newAborted, initialAborted, "CacheAborted should increase after seek")
}

// TestMultipleFlush tests writing, flushing, then writing again
func TestMultipleFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	testFile := filepath.Join(env.mountDir, "multiflush.txt")

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)

	// Write first content
	_, err = f.Write([]byte("First write"))
	require.NoError(t, err)
	f.Close()

	// Reopen to append more content
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_APPEND, 0644)
	require.NoError(t, err)

	// Write more content (mtime changes, so old xattr becomes invalid)
	_, err = f.Write([]byte(" Second write"))
	require.NoError(t, err)
	f.Close()

	// Verify final content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "First write Second write", string(content))
}

// TestCacheDuplicate tests that duplicate cache entries are detected
func TestCacheDuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	initialDup := getCounterValue(GlobalCacheMetrics.CacheDup)

	// Write both files
	file1 := filepath.Join(env.mountDir, "dup1.txt")
	content := []byte("Duplicate content")
	require.NoError(t, os.WriteFile(file1, content, 0644))

	file2 := filepath.Join(env.mountDir, "dup2.txt")
	require.NoError(t, os.WriteFile(file2, content, 0644))

	newDup := getCounterValue(GlobalCacheMetrics.CacheDup)
	assert.Equal(t, initialDup+1, newDup, "CacheDup should increase by 1")

	// Clean up cache
	require.NoError(t, server.Cache.EraseAllCache())

	initialHit := getCounterValue(GlobalCacheMetrics.CacheHit)

	// Read first file - should miss cache
	_, err = os.ReadFile(file1)
	require.NoError(t, err)

	// Read second file - should have cache hit from first file
	_, err = os.ReadFile(file2)
	require.NoError(t, err)

	newHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	assert.Equal(t, initialHit+1, newHit, "CacheHit should increase by 1 after cleanup")
}

// TestReadRefillCacheKey tests cache refilling after erasure
func TestReadRefillCacheKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	testFile := filepath.Join(env.mountDir, "refill.txt")
	testContent := []byte("Test content for refill")

	// 1. Write file
	require.NoError(t, os.WriteFile(testFile, testContent, 0644))

	// Erase cache
	require.NoError(t, server.Cache.EraseAllCache())

	initialMiss := getCounterValue(GlobalCacheMetrics.CacheMiss)
	initialHit := getCounterValue(GlobalCacheMetrics.CacheHit)

	// 2. Read file in sequence - should miss cache
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)

	newMiss := getCounterValue(GlobalCacheMetrics.CacheMiss)
	assert.Equal(t, initialMiss+1, newMiss, "CacheMiss should increase by 1")

	// 3. Read it again - cache should hit
	content, err = os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)

	newHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	assert.Equal(t, initialHit+1, newHit, "CacheHit should increase by 1 on second read")
}

// TestListDirectories tests directory listing functionality
func TestListDirectories(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	env := setupTestEnv(t)

	// Create test directory structure
	subDir := filepath.Join(env.srcDir, "subdir")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Create some files in root
	require.NoError(t, os.WriteFile(filepath.Join(env.srcDir, "file1.txt"), []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(env.srcDir, "file2.txt"), []byte("content2"), 0644))

	// Create files in subdirectory
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "subfile1.txt"), []byte("subcontent1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "subfile2.txt"), []byte("subcontent2"), 0644))

	server, err := MountWorkDir(env.srcDir, env.mountDir, env.cacheDir, disallowOther)
	require.NoError(t, err)
	defer server.Unmount()

	t.Run("list root directory", func(t *testing.T) {
		entries, err := os.ReadDir(env.mountDir)
		require.NoError(t, err)
		require.Len(t, entries, 3, "Should have 2 files and 1 directory")

		names := make(map[string]bool)
		for _, entry := range entries {
			names[entry.Name()] = entry.IsDir()
		}

		isDir, exists := names["file1.txt"]
		assert.True(t, exists, "Should contain file1.txt")
		assert.False(t, isDir, "file1.txt should not be a directory")

		isDir, exists = names["file2.txt"]
		assert.True(t, exists, "Should contain file2.txt")
		assert.False(t, isDir, "file2.txt should not be a directory")

		isDir, exists = names["subdir"]
		assert.True(t, exists, "Should contain subdir")
		assert.True(t, isDir, "subdir should be a directory")
	})

	t.Run("list subdirectory", func(t *testing.T) {
		entries, err := os.ReadDir(filepath.Join(env.mountDir, "subdir"))
		require.NoError(t, err)
		require.Len(t, entries, 2, "Should have 2 files in subdirectory")

		names := make(map[string]bool)
		for _, entry := range entries {
			names[entry.Name()] = entry.IsDir()
		}

		isDir, exists := names["subfile1.txt"]
		assert.True(t, exists, "Should contain subfile1.txt")
		assert.False(t, isDir, "subfile1.txt should not be a directory")

		isDir, exists = names["subfile2.txt"]
		assert.True(t, exists, "Should contain subfile2.txt")
		assert.False(t, isDir, "subfile2.txt should not be a directory")
	})

	t.Run("list empty directory", func(t *testing.T) {
		emptyDir := filepath.Join(env.mountDir, "emptydir")
		require.NoError(t, os.Mkdir(emptyDir, 0755))

		entries, err := os.ReadDir(emptyDir)
		require.NoError(t, err)
		assert.Len(t, entries, 0, "Empty directory should have no entries")
	})

	t.Run("list non-existent directory", func(t *testing.T) {
		_, err := os.ReadDir(filepath.Join(env.mountDir, "nonexistent"))
		assert.Error(t, err, "Should return error for non-existent directory")
		assert.True(t, os.IsNotExist(err), "Error should be os.ErrNotExist")
	})
}
