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
	"context"
	"encoding/hex"
	"hash"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"golang.org/x/sys/unix"
)

// NoCacheFileHandle wraps a file descriptor with cache key setting only.
// It does not read from cache or write data to cache, only sets the cache key (xattr) during write.
type NoCacheFileHandle struct {
	*fs.LoopbackFile

	// For cache key operations
	mu           sync.Mutex
	isWrite      bool
	hasher       hash.Hash
	bytesWritten int64
}

var _ = (fs.FileHandle)((*NoCacheFileHandle)(nil))
var _ = (fs.FileReleaser)((*NoCacheFileHandle)(nil))
var _ = (fs.FileWriter)((*NoCacheFileHandle)(nil))
var _ = (fs.FileFlusher)((*NoCacheFileHandle)(nil))
var _ = (fs.FileFsyncer)((*NoCacheFileHandle)(nil))

// Write implements FileWriter - computes hash for cache key but does not write to cache
func (f *NoCacheFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Check if this is a sequential write
	if f.isWrite && off != f.bytesWritten {
		// Non-sequential write, abort hash computation
		if f.hasher != nil {
			f.hasher = nil // Disable hash computation
		}
	}

	n, errno := f.LoopbackFile.Write(ctx, data, off)
	if errno != 0 {
		return 0, errno
	}

	// If writing sequentially and hasher is active, update hash
	if f.isWrite && off == f.bytesWritten && f.hasher != nil {
		f.hasher.Write(data[:n])
		f.bytesWritten += int64(n)
	}

	return uint32(n), 0
}

// Flush implements FileFlusher - sets cache key (xattr) but does not commit to cache
func (f *NoCacheFileHandle) Flush(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	errno := f.LoopbackFile.Flush(ctx)
	if errno != 0 {
		return errno
	}

	// Set cache key (xattr) if we were writing sequentially
	if f.isWrite && f.hasher != nil {
		// Compute SHA256 from hasher
		sha256sum := hex.EncodeToString(f.hasher.Sum(nil))
		if sha256sum != "" {
			// Get mtime from file after flush
			fd, _ := f.PassthroughFd()
			var st syscall.Stat_t
			if statErr := syscall.Fstat(fd, &st); statErr == nil {
				fileMtime := time.Unix(st.Mtim.Unix())
				// Encode SHA256 and mtime into xattr (cache key only)
				xattrValue := encodeCacheXattr(sha256sum, fileMtime, st.Size)
				unix.Fsetxattr(fd, xattrCacheName, []byte(xattrValue), 0)
				// Note: We do NOT actually store the data in cache, only set the key
			}
		}
		f.hasher = nil
	}

	return 0
}

// Release implements FileReleaser - clean up on close
func (f *NoCacheFileHandle) Release(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Clear hasher if still active
	f.hasher = nil

	oldfile := f.LoopbackFile
	f.LoopbackFile = nil
	return oldfile.Release(ctx)
}
