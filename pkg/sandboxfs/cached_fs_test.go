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

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"golang.org/x/sys/unix"
)

func disallowOther(options *fs.Options) {
	options.MountOptions.AllowOther = false
}

func TestCacheManager(t *testing.T) {
	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	cache, err := NewDirectoryCacheManager(cacheDir)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("write and commit to cache", func(t *testing.T) {
		writer, err := cache.CreateTemp()
		if err != nil {
			t.Fatal(err)
		}

		testContent := []byte("Test content for caching")
		_, err = writer.Write(testContent)
		if err != nil {
			t.Fatal(err)
		}

		sha256sum, err := writer.Commit()
		if err != nil {
			t.Fatal(err)
		}
		if len(sha256sum) != 64 {
			t.Errorf("Expected SHA256 length 64, got %d", len(sha256sum))
		}

		// Lookup the cached file
		cachedPath, err := cache.Lookup(sha256sum)
		if err != nil {
			t.Fatal(err)
		}
		if cachedPath == "" {
			t.Error("Expected non-empty cached path")
		}

		// Verify content
		content, err := os.ReadFile(cachedPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(testContent) {
			t.Errorf("Content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("abort cache write", func(t *testing.T) {
		writer, err := cache.CreateTemp()
		if err != nil {
			t.Fatal(err)
		}

		_, err = writer.Write([]byte("This will be aborted"))
		if err != nil {
			t.Fatal(err)
		}

		err = writer.Abort()
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("lookup non-existent cache", func(t *testing.T) {
		fakeSHA := "0000000000000000000000000000000000000000000000000000000000000000"
		cachedPath, err := cache.Lookup(fakeSHA)
		if err != nil {
			t.Fatal(err)
		}
		if cachedPath != "" {
			t.Errorf("Expected empty cached path for non-existent hash, got %q", cachedPath)
		}
	})
}

func TestSequentialWriteCaching(t *testing.T) {
	cacheDir, err := os.MkdirTemp("", "velda-cache-seq-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	cache, err := NewDirectoryCacheManager(cacheDir)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("sequential writes are cached", func(t *testing.T) {
		writer, err := cache.CreateTemp()
		if err != nil {
			t.Fatal(err)
		}

		// Write sequentially
		for i := 0; i < 10; i++ {
			_, err = writer.Write([]byte("chunk"))
			if err != nil {
				t.Fatal(err)
			}
		}

		sha256sum, err := writer.Commit()
		if err != nil {
			t.Fatal(err)
		}

		// Verify cached
		cachedPath, err := cache.Lookup(sha256sum)
		if err != nil {
			t.Fatal(err)
		}
		if cachedPath == "" {
			t.Error("Expected cached file after sequential writes")
		}
	})
}

func TestCachedFilesystem(t *testing.T) {
	testDir, err := os.MkdirTemp("", "veldafs-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Test dir: %s", testDir)
	//defer os.RemoveAll(testDir)

	// Create temporary directories
	baseDir := filepath.Join(testDir, "base")
	err = os.MkdirAll(baseDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	mountDir := filepath.Join(testDir, "mount")
	err = os.MkdirAll(mountDir, 0755)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(testDir, "cache")
	err = os.MkdirAll(cacheDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create a test file in base directory
	testFile := filepath.Join(baseDir, "test.txt")
	testContent := []byte("Hello, World!")
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Mount the filesystem with cache
	server, err := MountWorkDir(baseDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	// Wait for mount to be ready
	err = server.WaitMount()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("read file and populate cache", func(t *testing.T) {
		// Read the file through the mounted filesystem
		mountedFile := filepath.Join(mountDir, "test.txt")
		content, err := os.ReadFile(mountedFile)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(testContent) {
			t.Errorf("Content mismatch: got %q, want %q", string(content), string(testContent))
		}
	})

	t.Run("write file and cache", func(t *testing.T) {
		// Write a new file through the mounted filesystem
		newFile := filepath.Join(mountDir, "newfile.txt")
		newContent := []byte("This is a new file")
		err := os.WriteFile(newFile, newContent, 0644)
		if err != nil {
			t.Fatal(err)
		}

		// Read it back
		content, err := os.ReadFile(newFile)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != string(newContent) {
			t.Errorf("Content mismatch: got %q, want %q", string(content), string(newContent))
		}
	})
}

// getCounterValue retrieves the current value of a Prometheus counter
func getCounterValue(counter prometheus.Counter) float64 {
	var m dto.Metric
	if err := counter.Write(&m); err != nil {
		return 0
	}
	return m.Counter.GetValue()
}

// TestCacheMetrics tests that cache hit/miss/not-exist metrics are recorded correctly
func TestCacheMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	// Create test file
	testFile := filepath.Join(srcDir, "testfile.txt")
	testContent := []byte("Hello, cached world!")
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Mount with cache
	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	mountedFile := filepath.Join(mountDir, "testfile.txt")

	// Record initial metric values
	initialNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
	initialHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	initialSaved := getCounterValue(GlobalCacheMetrics.CacheSaved)

	// First read - should be CacheNotExist (no xattr yet)
	content, err := os.ReadFile(mountedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(testContent) {
		t.Errorf("Content mismatch: got %q, want %q", string(content), string(testContent))
	}

	newNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
	if newNotExist != initialNotExist+1 {
		t.Errorf("Expected CacheNotExist to increase by 1, got %f -> %f", initialNotExist, newNotExist)
	}

	// Write a file to trigger cache creation
	newFile := filepath.Join(mountDir, "newcached.txt")
	newContent := []byte("This will be cached")
	err = os.WriteFile(newFile, newContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	newSaved := getCounterValue(GlobalCacheMetrics.CacheSaved)
	if newSaved != initialSaved+2 {
		t.Errorf("Expected CacheSaved to increase by 2, got %f -> %f", initialSaved, newSaved)
	}

	// Second read of the newly created file - should be CacheHit
	content, err = os.ReadFile(newFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(newContent) {
		t.Errorf("Content mismatch: got %q, want %q", string(content), string(newContent))
	}

	newHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	if newHit != initialHit+1 {
		t.Errorf("Expected CacheHit to increase by 1, got %f -> %f", initialHit, newHit)
	}
}

// TestRewriteFile tests rewriting a file with O_TRUNC
func TestRewriteFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	testFile := filepath.Join(mountDir, "rewrite.txt")

	// Write initial content
	initialContent := []byte("Initial content")
	err = os.WriteFile(testFile, initialContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Verify initial content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(initialContent) {
		t.Errorf("Initial content mismatch: got %q, want %q", string(content), string(initialContent))
	}

	// Rewrite with different content (O_TRUNC)
	newContent := []byte("New content after rewrite")
	err = os.WriteFile(testFile, newContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Verify new content
	content, err = os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(newContent) {
		t.Errorf("New content mismatch: got %q, want %q", string(content), string(newContent))
	}

	// Check that xattr was invalidated and updated
	var buf [64]byte
	srcFile := filepath.Join(srcDir, "rewrite.txt")
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

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	testFile := filepath.Join(mountDir, "append.txt")

	// Initial write
	initialContent := []byte("Initial")
	err = os.WriteFile(testFile, initialContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Reopen and append
	f, err := os.OpenFile(testFile, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}

	appendContent := []byte(" Appended")
	_, err = f.Write(appendContent)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	// Verify combined content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := "Initial Appended"
	if string(content) != expected {
		t.Errorf("Content mismatch: got %q, want %q", string(content), expected)
	}
}

// TestNonSequentialWrite tests that non-sequential writes abort caching
func TestNonSequentialWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	testFile := filepath.Join(mountDir, "nonseq.txt")

	initialAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Write at offset 0
	_, err = f.WriteAt([]byte("start"), 0)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	// Write at non-sequential offset (should abort caching)
	_, err = f.WriteAt([]byte("end"), 100)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	f.Close()

	newAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)
	if newAborted <= initialAborted {
		t.Errorf("Expected CacheAborted to increase, got %f -> %f", initialAborted, newAborted)
	}

	// Verify xattr was NOT set (caching was aborted)
	var buf [64]byte
	srcFile := filepath.Join(srcDir, "nonseq.txt")
	_, err = unix.Getxattr(srcFile, xattrCacheName, buf[:])
	if err == nil {
		t.Errorf("Expected no xattr after non-sequential write, but found one")
	}
}

// TestSeekAndWrite tests that seeking and writing aborts caching
func TestSeekAndWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	testFile := filepath.Join(mountDir, "seek.txt")

	initialAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Write sequentially
	_, err = f.Write([]byte("first part"))
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	// Seek to earlier position
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	// Write again (should abort caching due to non-sequential write)
	_, err = f.Write([]byte("overwrite"))
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	f.Close()

	newAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)
	if newAborted <= initialAborted {
		t.Errorf("Expected CacheAborted to increase after seek, got %f -> %f", initialAborted, newAborted)
	}
}

// TestMultipleFlush tests writing, flushing, then writing again
func TestMultipleFlush(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	testFile := filepath.Join(mountDir, "multiflush.txt")

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Write first content
	_, err = f.Write([]byte("First write"))
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	// Close to trigger flush
	f.Close()

	// Reopen to append more content
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Write more content (mtime changes, so old xattr becomes invalid)
	_, err = f.Write([]byte(" Second write"))
	if err != nil {
		f.Close()
		t.Fatal(err)
	}

	f.Close()

	// Verify final content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	expected := "First write Second write"
	if string(content) != expected {
		t.Errorf("Content mismatch: got %q, want %q", string(content), expected)
	}
}

// TestCacheDuplicate tests that duplicate cache entries are detected
func TestCacheDuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	initialDup := getCounterValue(GlobalCacheMetrics.CacheDup)

	// Write both files
	file1 := filepath.Join(mountDir, "dup1.txt")
	content := []byte("Duplicate content")
	err = os.WriteFile(file1, content, 0644)
	if err != nil {
		t.Fatal(err)
	}

	file2 := filepath.Join(mountDir, "dup2.txt")
	err = os.WriteFile(file2, content, 0644)
	if err != nil {
		t.Fatal(err)
	}

	newDup := getCounterValue(GlobalCacheMetrics.CacheDup)
	if newDup != initialDup+1 {
		t.Errorf("Expected CacheDup to increase by 1, got %f -> %f", initialDup, newDup)
	}

	// Clean up cache
	err = server.Cache.EraseAllCache()
	if err != nil {
		t.Fatal(err)
	}

	initialHit := getCounterValue(GlobalCacheMetrics.CacheHit)

	// Read first file - should miss cache
	_, err = os.ReadFile(file1)
	if err != nil {
		t.Fatal(err)
	}

	// Read second file - should have cache hit from first file
	_, err = os.ReadFile(file2)
	if err != nil {
		t.Fatal(err)
	}

	newHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	if newHit != initialHit+1 {
		t.Errorf("Expected CacheHit to increase by 1 after cleanup, got %f -> %f", initialHit, newHit)
	}
}

// TestReadRefillCacheKey tests cache refilling after erasure
func TestReadRefillCacheKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FUSE test in short mode")
	}

	cacheDir, err := os.MkdirTemp("", "velda-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)

	srcDir, err := os.MkdirTemp("", "velda-src-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(srcDir)

	mountDir, err := os.MkdirTemp("", "velda-mount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(mountDir)

	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	testFile := filepath.Join(mountDir, "refill.txt")
	testContent := []byte("Test content for refill")

	// 1. Write file
	err = os.WriteFile(testFile, testContent, 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Erase cache
	err = server.Cache.EraseAllCache()
	if err != nil {
		t.Fatal(err)
	}

	initialNotExist := getCounterValue(GlobalCacheMetrics.CacheMiss)
	initialHit := getCounterValue(GlobalCacheMetrics.CacheHit)

	// 2. Read file in sequence - should miss cache
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(testContent) {
		t.Errorf("Content mismatch: got %q, want %q", string(content), string(testContent))
	}

	newNotExist := getCounterValue(GlobalCacheMetrics.CacheMiss)
	if newNotExist != initialNotExist+1 {
		t.Errorf("Expected CacheNotExist to increase by 1, got %f -> %f", initialNotExist, newNotExist)
	}

	// 3. Read it again - cache should hit
	content, err = os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(testContent) {
		t.Errorf("Content mismatch on second read: got %q, want %q", string(content), string(testContent))
	}

	newHit := getCounterValue(GlobalCacheMetrics.CacheHit)
	if newHit != initialHit+1 {
		t.Errorf("Expected CacheHit to increase by 1 on second read, got %f -> %f", initialHit, newHit)
	}
}
