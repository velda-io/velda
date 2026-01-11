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
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// BackfillCacheKeys recursively walks through a directory and adds xattr cache keys
// for all files where the key doesn't exist or is outdated. This only computes
// the SHA256 hash and sets the xattr - it does NOT add files to the cache.
// This is useful for pre-populating cache metadata for a directory tree.
func BackfillCacheKeys(rootDir string, xattrName string, progressCallback func(path string, updated bool, err error)) error {
	return filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if progressCallback != nil {
				progressCallback(path, false, err)
			}
			// Continue walking even if there's an error with this path
			return nil
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Check and update cache key for this file
		updated, err := backfillCacheKeyForFile(path, xattrName)
		if progressCallback != nil {
			progressCallback(path, updated, err)
		}

		// Continue walking even if there's an error with this file
		return nil
	})
}

// backfillCacheKeyForFile checks if a file needs a cache key update and performs the update
// Returns true if the xattr was updated, false if it already had a valid cache key
func backfillCacheKeyForFile(filePath string, xattrName string) (bool, error) {
	// Get file stats
	var st syscall.Stat_t
	if err := syscall.Stat(filePath, &st); err != nil {
		return false, fmt.Errorf("failed to stat file: %w", err)
	}

	// Check if file already has a valid cache key
	var xattrBuf [128]byte
	sz, xattrErr := unix.Lgetxattr(filePath, xattrName, xattrBuf[:])

	fileMtime := time.Unix(st.Mtim.Unix())
	needsUpdate := true

	if xattrErr == nil && sz > 0 {
		xattrValue := string(xattrBuf[:sz])
		_, cachedMtime, cachedSize, ok := decodeCacheXattr(xattrValue)

		if ok && cachedMtime.Equal(fileMtime) && cachedSize == st.Size {
			// Cache key is valid (mtime and size match)
			needsUpdate = false
		}
	}

	if !needsUpdate {
		return false, nil
	}

	// Compute SHA256 of the file
	f, err := os.Open(filePath)
	if err != nil {
		return false, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return false, fmt.Errorf("failed to hash file: %w", err)
	}

	sha256sum := hex.EncodeToString(hasher.Sum(nil))

	// Set xattr with SHA256 and mtime
	xattrValue := encodeCacheXattr(sha256sum, fileMtime, st.Size)
	if err := unix.Lsetxattr(filePath, xattrName, []byte(xattrValue), 0); err != nil {
		return false, fmt.Errorf("failed to set xattr: %w", err)
	}

	return true, nil
}
