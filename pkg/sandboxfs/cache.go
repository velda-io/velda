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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
)

// CacheManager manages a content-addressable cache of files using SHA256 hashing
type CacheManager interface {
	// Lookup checks if a file with the given SHA256 exists in cache
	// Returns the absolute path to the cached file if it exists, empty string otherwise
	Lookup(sha256sum string) (string, error)

	// CreateTemp creates a temporary file for writing
	// Returns a CacheWriter that can be committed or aborted
	CreateTemp() (CacheWriter, error)

	// Remove removes a file from the cache by its SHA256
	Remove(sha256sum string) error
}

// CacheWriter is an interface for writing to a temporary cache file
type CacheWriter interface {
	io.Writer

	// Commit finalizes the write and stores the file in cache under its SHA256
	// Returns the SHA256 hash of the written content
	Commit() (sha256sum string, err error)

	// Abort discards the temporary file
	Abort() error
}

// DirectoryCacheManager implements CacheManager using a directory-based structure
type DirectoryCacheManager struct {
	cacheDir string
	mu       sync.RWMutex
}

// NewDirectoryCacheManager creates a new directory-based cache manager
func NewDirectoryCacheManager(cacheDir string) (*DirectoryCacheManager, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &DirectoryCacheManager{
		cacheDir: cacheDir,
	}, nil
}

// Lookup implements CacheManager.Lookup
func (c *DirectoryCacheManager) Lookup(sha256sum string) (string, error) {
	if len(sha256sum) != 64 {
		return "", fmt.Errorf("invalid SHA256 hash length: %d", len(sha256sum))
	}

	// Store files in subdirectories based on first 2 characters of hash
	// This prevents too many files in a single directory
	prefix := sha256sum[:2]
	cachePath := filepath.Join(c.cacheDir, prefix, sha256sum)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if _, err := os.Stat(cachePath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	return cachePath, nil
}

// CreateTemp implements CacheManager.CreateTemp
func (c *DirectoryCacheManager) CreateTemp() (CacheWriter, error) {
	tempDir := filepath.Join(c.cacheDir, ".tmp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(tempDir, "cache-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	return &dirCacheWriter{
		file:     tmpFile,
		cacheDir: c.cacheDir,
		mu:       &c.mu,
		hasher:   sha256.New(),
	}, nil
}

// Remove implements CacheManager.Remove
func (c *DirectoryCacheManager) Remove(sha256sum string) error {
	if len(sha256sum) != 64 {
		return fmt.Errorf("invalid SHA256 hash length: %d", len(sha256sum))
	}

	prefix := sha256sum[:2]
	cachePath := filepath.Join(c.cacheDir, prefix, sha256sum)

	c.mu.Lock()
	defer c.mu.Unlock()

	err := os.Remove(cachePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}

// EraseAllCache removes all cached files from the cache directory
// This is for testing purposes only
func (c *DirectoryCacheManager) EraseAllCache() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove all subdirectories (except .tmp)
	entries, err := os.ReadDir(c.cacheDir)
	if err != nil {
		return fmt.Errorf("failed to read cache directory: %w", err)
	}

	for _, entry := range entries {
		if entry.Name() == ".tmp" {
			continue
		}
		path := filepath.Join(c.cacheDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	return nil
}

// dirCacheWriter implements CacheWriter
type dirCacheWriter struct {
	file     *os.File
	cacheDir string
	mu       *sync.RWMutex
	hasher   io.Writer
	written  atomic.Int64
	closed   bool
}

// Write implements io.Writer
func (w *dirCacheWriter) Write(p []byte) (n int, err error) {
	if w.closed {
		return 0, syscall.EBADF
	}

	n, err = w.file.Write(p)
	if err != nil {
		return n, err
	}

	// Also write to hasher
	w.hasher.Write(p[:n])

	// Update written counter atomically
	w.written.Add(int64(n))

	return n, nil
}

// GetSHA256 implements CacheWriter.GetSHA256
func (w *dirCacheWriter) GetSHA256() string {
	if w.closed {
		return ""
	}
	hashBytes := w.hasher.(interface{ Sum([]byte) []byte }).Sum(nil)
	return hex.EncodeToString(hashBytes)
}

// Commit implements CacheWriter.Commit
func (w *dirCacheWriter) Commit() (string, error) {
	if w.closed {
		return "", syscall.EBADF
	}
	defer os.Remove(w.file.Name())

	w.closed = true

	// Get the SHA256 hash
	hashBytes := w.hasher.(interface{ Sum([]byte) []byte }).Sum(nil)
	sha256sum := hex.EncodeToString(hashBytes)

	// Close the file
	if err := w.file.Close(); err != nil {
		return "", err
	}

	// Move to final location
	prefix := sha256sum[:2]
	targetDir := filepath.Join(w.cacheDir, prefix)
	targetPath := filepath.Join(targetDir, sha256sum)

	// Create target directory
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	// Rename temp file to final location
	// If target already exists, just remove the temp file
	err := os.Link(w.file.Name(), targetPath)
	if err != nil {
		if os.IsExist(err) {
			// Duplicate, just remove temp file
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheDup.Inc()
			}
			//log.Printf("Cache duplicate for %s (%d bytes)", sha256sum, w.written)
			return sha256sum, nil
		}
		return "", fmt.Errorf("failed to move to cache: %w", err)
	}
	if GlobalCacheMetrics != nil {
		GlobalCacheMetrics.CacheSaved.Inc()
	}

	//log.Printf("Cached file %s (%d bytes)", sha256sum, w.written)
	return sha256sum, nil
}

// Abort implements CacheWriter.Abort
func (w *dirCacheWriter) Abort() error {
	if w.closed {
		return nil
	}

	w.closed = true
	w.file.Close()
	return os.Remove(w.file.Name())
}

// ReadAt reads len(p) bytes from the cached file at offset off
// Returns an error if the requested range hasn't been cached yet
func (w *dirCacheWriter) ReadAt(p []byte, off int64) (n int, err error) {
	if w.closed {
		return 0, syscall.EBADF
	}

	// Check if the requested range has been written
	currentWritten := w.written.Load()

	// If the requested range extends beyond what's been written, return error
	// This ensures we don't return partial data for chunks that haven't been fetched yet
	if off+int64(len(p)) > currentWritten {
		return 0, io.ErrUnexpectedEOF
	}

	// Read from the file using ReadAt (which doesn't modify file offset)
	return w.file.ReadAt(p, off)
}
