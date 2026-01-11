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
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

// CachedFileHandle wraps a file descriptor with caching capabilities
type CachedFileHandle struct {
	*fs.LoopbackFile
	path       string
	cache      CacheManager
	isWrite    bool
	fromCache  bool
	cachedStat *syscall.Stat_t

	// For caching operations
	mu           sync.Mutex
	cacheOps     bool
	cacheWrite   CacheWriter
	bytesWritten int64
	flushed      bool // Track if file has been flushed
	cacheFlushed bool // Track if cache was committed during flush
}

// switchToCachedFd switches the file handle to use a cached file descriptor
// This is called by background workers after successfully fetching and caching a file
func (f *CachedFileHandle) switchToCachedFd(cachedFd int, st *syscall.Stat_t) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Only switch if we haven't already switched, we're not writing, not closed, and no bytes have been written
	if f.isWrite || f.LoopbackFile == nil {
		syscall.Close(cachedFd) // Close the cached fd if we can't use it
		return
	}

	// Store original fd and switch to cached
	originalFile := f.LoopbackFile
	f.LoopbackFile = fs.NewLoopbackFile(cachedFd).(*fs.LoopbackFile)
	f.fromCache = true
	f.cachedStat = st
	f.cacheOps = false // Disable further caching since we're now using cache
	if f.cacheWrite != nil {
		f.cacheWrite.Abort()
	}

	// Close the original fd
	originalFile.Release(nil)
}

var _ = (fs.FileHandle)((*CachedFileHandle)(nil))
var _ = (fs.FileReleaser)((*CachedFileHandle)(nil))
var _ = (fs.FileGetattrer)((*CachedFileHandle)(nil))
var _ = (fs.FileReader)((*CachedFileHandle)(nil))
var _ = (fs.FileWriter)((*CachedFileHandle)(nil))
var _ = (fs.FileGetlker)((*CachedFileHandle)(nil))
var _ = (fs.FileSetlker)((*CachedFileHandle)(nil))
var _ = (fs.FileSetlkwer)((*CachedFileHandle)(nil))
var _ = (fs.FileLseeker)((*CachedFileHandle)(nil))
var _ = (fs.FileFlusher)((*CachedFileHandle)(nil))
var _ = (fs.FileFsyncer)((*CachedFileHandle)(nil))
var _ = (fs.FileSetattrer)((*CachedFileHandle)(nil))
var _ = (fs.FileAllocater)((*CachedFileHandle)(nil))

// Disable passthrough to allow redirecting to cached file descriptor when available
func (f *CachedFileHandle) PassthroughFd() (int, error) {
	fd, _ := f.LoopbackFile.PassthroughFd()
	return fd, syscall.EIO
}

// Getattr implements FileGetattrer
// Returns the original stat obtained during open when using cached file
func (f *CachedFileHandle) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	// If we're using a cached file, return the original stat
	if f.fromCache && f.cachedStat != nil {
		out.FromStat(f.cachedStat)
		return 0
	}

	// Otherwise, delegate to the underlying LoopbackFile
	return f.LoopbackFile.Getattr(ctx, out)
}

// Read implements FileReader
func (f *CachedFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fd, _ := f.PassthroughFd()
	n, err := syscall.Pread(fd, dest, off)
	if err != nil {
		return nil, fs.ToErrno(err)
	}

	// If we're doing cache ops and reading sequentially, hash the data
	if f.cacheOps && !f.isWrite && off == f.bytesWritten {
		if f.cacheWrite == nil {
			// Initialize cache writer
			cw, err := f.cache.CreateTemp()
			if err != nil {
				// Failed to create cache, disable caching
				f.abortCaching()
			} else {
				f.cacheWrite = cw
			}
		}
		f.cacheWrite.Write(dest[:n])
		f.bytesWritten += int64(n)
	}

	return fuse.ReadResultData(dest[:n]), 0
}

// Write implements FileWriter
func (f *CachedFileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Check if this is a sequential write
	if f.cacheOps && off != f.bytesWritten {
		// Non-sequential write, mark as uncachable
		f.abortCaching()
	}

	n, errno := f.LoopbackFile.Write(ctx, data, off)
	if errno != 0 {
		return 0, errno
	}

	// If caching is enabled, write to cache as well
	if f.cacheOps && f.isWrite {
		if f.cacheWrite == nil {
			// Initialize cache writer
			cw, err := f.cache.CreateTemp()
			if err != nil {
				// Failed to create cache, disable caching
				f.abortCaching()
			} else {
				f.cacheWrite = cw
			}
		}

		if f.cacheWrite != nil {
			_, err := f.cacheWrite.Write(data[:n])
			if err != nil {
				// Failed to write to cache, abort
				f.abortCaching()
			} else {
				f.bytesWritten += int64(n)
			}
		}
	}

	return uint32(n), 0
}

// Lseek implements FileLseeker - seeking aborts caching
func (f *CachedFileHandle) Lseek(ctx context.Context, off uint64, whence uint32) (uint64, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Any seek aborts caching
	f.abortCaching()

	return f.LoopbackFile.Lseek(ctx, off, whence)
}

// Flush implements FileFlusher - commits cache on flush
func (f *CachedFileHandle) Flush(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	errno := f.LoopbackFile.Flush(ctx)
	if errno != 0 {
		return errno
	}
	fd, _ := f.PassthroughFd()

	// Mark as flushed
	f.flushed = true

	// Commit cache if we were writing sequentially
	if f.cacheOps && f.isWrite && f.cacheWrite != nil {
		// Now commit the cache
		committedSHA, err := f.cacheWrite.Commit()
		if err == nil && committedSHA != "" {
			// Get mtime from file after flush
			var st syscall.Stat_t
			if statErr := syscall.Fstat(fd, &st); statErr == nil {
				fileMtime := time.Unix(st.Mtim.Unix())
				// Encode SHA256 and mtime into xattr
				xattrValue := encodeCacheXattr(committedSHA, fileMtime, st.Size)
				unix.Lsetxattr(f.path, xattrCacheName, []byte(xattrValue), 0)
				//log.Printf("Set xattr for %s: SHA=%s, mtime=%v, err=%v", f.path, committedSHA, fileMtime, setErr)
				f.cacheFlushed = true
			}
		}
		f.cacheWrite = nil
		f.cacheOps = false // Disable further caching after flush
	} else if f.cacheOps && !f.isWrite && f.cacheWrite != nil && f.bytesWritten > 0 {
		// Get current mtime & size
		var st syscall.Stat_t
		if statErr := syscall.Fstat(fd, &st); statErr == nil && f.bytesWritten == st.Size {
			// For reads, compute SHA256 and set xattr with mtime
			committedSHA, err := f.cacheWrite.Commit()
			if err == nil && committedSHA != "" {
				fileMtime := time.Unix(st.Mtim.Unix())
				xattrValue := encodeCacheXattr(committedSHA, fileMtime, st.Size)
				unix.Lsetxattr(f.path, xattrCacheName, []byte(xattrValue), 0)
				//log.Printf("Set xattr for %s after read: SHA=%s, mtime=%v", f.path, sha256sum, fileMtime)
			}
		}
		f.cacheOps = false // Disable further caching after flush
	} else if f.cacheWrite != nil {
		// Abort any pending cache write that wasn't sequential - ABORTED
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.CacheAborted.Inc()
		}
		f.cacheWrite.Abort()
		f.cacheWrite = nil
		f.cacheOps = false
	}

	return 0
}

// Release implements FileReleaser - clean up on close
func (f *CachedFileHandle) Release(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	// If cache operations are still pending and not flushed, abort them
	if f.cacheWrite != nil {
		f.cacheWrite.Abort()
		f.cacheWrite = nil
	}
	oldfile := f.LoopbackFile
	f.LoopbackFile = nil
	return oldfile.Release(ctx)
}

// abortCaching disables caching for this file handle
func (f *CachedFileHandle) abortCaching() {
	if f.cacheWrite != nil {
		// Track aborted cache operation
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.CacheAborted.Inc()
		}
		f.cacheWrite.Abort()
		f.cacheWrite = nil
	}
	f.cacheOps = false
}
