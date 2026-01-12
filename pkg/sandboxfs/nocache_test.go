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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"golang.org/x/sys/unix"
)

// TestNoCacheMode verifies that no-cache mode only sets cache keys without reading/writing cache data
func TestNoCacheMode(t *testing.T) {
	srcDir := t.TempDir()
	mountDir := t.TempDir()
	cacheDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(srcDir, "testfile.txt")
	testContent := []byte("Hello, no-cache mode!")
	if err := os.WriteFile(testFile, testContent, 0644); err != nil {
		t.Fatal(err)
	}

	disallowOther := WithFuseOption(func(opts *fs.Options) {
		opts.MountOptions.AllowOther = false
	})

	// Initialize metrics
	if GlobalCacheMetrics == nil {
		metrics := NewCacheMetrics()
		metrics.Register()
		GlobalCacheMetrics = metrics
		defer metrics.Unregister()
	}

	// Mount with no-cache mode
	server, err := MountWorkDir(srcDir, mountDir, cacheDir, disallowOther, WithNoCacheMode())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Unmount()

	// Verify NoCacheMode is enabled
	if !server.Root.mountCtx.NoCacheMode {
		t.Fatal("Expected NoCacheMode to be true")
	}

	// Capture initial metrics
	initialHits := getCounterValue(GlobalCacheMetrics.CacheHit)
	initialMisses := getCounterValue(GlobalCacheMetrics.CacheMiss)
	initialSaved := getCounterValue(GlobalCacheMetrics.CacheSaved)
	initialNotExist := getCounterValue(GlobalCacheMetrics.CacheNotExist)
	initialFetched := getCounterValue(GlobalCacheMetrics.CacheFetched)

	// Write a new file to test cache key setting
	newFile := filepath.Join(mountDir, "newfile.txt")
	newContent := []byte("Testing cache key setting in no-cache mode")
	if err := os.WriteFile(newFile, newContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Read the existing test file
	readContent, err := os.ReadFile(filepath.Join(mountDir, "testfile.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if string(readContent) != string(testContent) {
		t.Fatalf("Content mismatch: got %q, want %q", readContent, testContent)
	}

	// Read the new file back
	readNewContent, err := os.ReadFile(newFile)
	if err != nil {
		t.Fatal(err)
	}

	if string(readNewContent) != string(newContent) {
		t.Fatalf("Content mismatch: got %q, want %q", readNewContent, newContent)
	}

	// Verify the file exists in source directory
	srcNewFile := filepath.Join(srcDir, "newfile.txt")
	if _, err := os.Stat(srcNewFile); err != nil {
		t.Fatal("File should exist in source directory")
	}

	// Give some time for any async operations
	time.Sleep(100 * time.Millisecond)

	// Verify NO cache metrics were updated (no cache hits, misses, or saves in no-cache mode)
	if getCounterValue(GlobalCacheMetrics.CacheHit) != initialHits {
		t.Errorf("Expected no cache hits in no-cache mode, got %.0f new hits",
			getCounterValue(GlobalCacheMetrics.CacheHit)-initialHits)
	}
	if getCounterValue(GlobalCacheMetrics.CacheMiss) != initialMisses {
		t.Errorf("Expected no cache misses in no-cache mode, got %.0f new misses",
			getCounterValue(GlobalCacheMetrics.CacheMiss)-initialMisses)
	}
	if getCounterValue(GlobalCacheMetrics.CacheSaved) != initialSaved {
		t.Errorf("Expected no cache saves in no-cache mode, got %.0f new saves",
			getCounterValue(GlobalCacheMetrics.CacheSaved)-initialSaved)
	}
	if getCounterValue(GlobalCacheMetrics.CacheNotExist) != initialNotExist {
		t.Errorf("Expected no CacheNotExist in no-cache mode, got %.0f new",
			getCounterValue(GlobalCacheMetrics.CacheNotExist)-initialNotExist)
	}
	if getCounterValue(GlobalCacheMetrics.CacheFetched) != initialFetched {
		t.Errorf("Expected no CacheFetched in no-cache mode, got %.0f new",
			getCounterValue(GlobalCacheMetrics.CacheFetched)-initialFetched)
	}

	// Verify that cache key (xattr) WAS set for the written file
	var xattrBuf [128]byte
	sz, err := unix.Lgetxattr(srcNewFile, "user.veldafs.cache", xattrBuf[:])
	if err != nil {
		t.Fatalf("Expected cache key (xattr) to be set, got error: %v", err)
	}
	if sz <= 0 {
		t.Fatal("Expected cache key (xattr) to have content")
	}

	xattrValue := string(xattrBuf[:sz])
	t.Logf("Cache key set in no-cache mode: %s", xattrValue)

	// Unmount the no-cache filesystem
	if err := server.Unmount(); err != nil {
		t.Fatal(err)
	}

	// Wait for unmount to complete
	time.Sleep(100 * time.Millisecond)

	// Now remount with REGULAR cache mode to verify the cache key can be used
	mountDir2 := t.TempDir()
	server2, err := MountWorkDir(srcDir, mountDir2, cacheDir, disallowOther)
	if err != nil {
		t.Fatal(err)
	}
	defer server2.Unmount()

	// Verify regular cache mode (not no-cache mode)
	if server2.Root.mountCtx.NoCacheMode {
		t.Fatal("Expected NoCacheMode to be false for regular mount")
	}

	// Capture metrics before reading
	hitsBeforeRead := getCounterValue(GlobalCacheMetrics.CacheHit)
	missesBeforeRead := getCounterValue(GlobalCacheMetrics.CacheMiss)
	staleBeforeRead := getCounterValue(GlobalCacheMetrics.CacheStale)

	// Read the file that has the cache key set
	readContent2, err := os.ReadFile(filepath.Join(mountDir2, "newfile.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if string(readContent2) != string(newContent) {
		t.Fatalf("Content mismatch after remount: got %q, want %q", readContent2, newContent)
	}

	// Give some time for metrics to update
	time.Sleep(100 * time.Millisecond)

	// Verify that cache operations occurred in regular mode
	// Since the cache key exists but cache file doesn't (we only set key in no-cache mode),
	// we should see either a cache miss or stale entry
	hitsAfter := getCounterValue(GlobalCacheMetrics.CacheHit)
	missesAfter := getCounterValue(GlobalCacheMetrics.CacheMiss)
	staleAfter := getCounterValue(GlobalCacheMetrics.CacheStale)

	if (missesAfter-missesBeforeRead) == 0 && (staleAfter-staleBeforeRead) == 0 && (hitsAfter-hitsBeforeRead) == 0 {
		t.Error("Expected cache metrics to be updated after reading file with cache key in regular mode")
	}

	t.Logf("Cache metrics after remount: hits=%.0f, misses=%.0f, stale=%.0f",
		hitsAfter-hitsBeforeRead, missesAfter-missesBeforeRead, staleAfter-staleBeforeRead)

	t.Log("No-cache mode test passed: cache keys set correctly, no cache data operations")
}
