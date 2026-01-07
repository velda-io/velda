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

// This file also contains fork of https://github.com/hanwen/go-fuse/blob/master/fs/files.go
// authored by Go-FUSE authors, licensed under the BSD 3-Clause License.

// TODO: Refactor their implementation to make extension easier.
package sandboxfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

const (
	// xattrCacheName is the extended attribute name used to store SHA256 hash and mtime
	xattrCacheName = "user.veldafs.cache"

	// OFD (Open File Description) lock constants for fcntl
	_OFD_GETLK  = 36
	_OFD_SETLK  = 37
	_OFD_SETLKW = 38
)

// encodeCacheXattr encodes SHA256 hash and mtime into xattr format: "version:sha256:mtime_sec.mtime_nsec"
func encodeCacheXattr(sha256sum string, mtime time.Time, size int64) string {
	return fmt.Sprintf("1:%s:%d.%d:%d", sha256sum, mtime.Unix(), mtime.Nanosecond(), size)
}

// decodeCacheXattr decodes xattr value into SHA256 hash and mtime
func decodeCacheXattr(xattrValue string) (sha256sum string, mtime time.Time, size int64, ok bool) {
	parts := strings.Split(xattrValue, ":")
	if len(parts) != 4 || len(parts[1]) != 64 || parts[0] != "1" {
		return "", time.Time{}, 0, false
	}

	sha256sum = parts[1]
	timeParts := strings.Split(parts[2], ".")
	if len(timeParts) != 2 {
		return "", time.Time{}, 0, false
	}

	sec, err := strconv.ParseInt(timeParts[0], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, false
	}

	nsec, err := strconv.ParseInt(timeParts[1], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, false
	}

	mtime = time.Unix(sec, nsec)

	size, err = strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, false
	}

	return sha256sum, mtime, size, true
}

// NewCachedLoopbackRoot creates a new cached loopback root node
func NewCachedLoopbackRoot(rootPath string, cache CacheManager) (fs.InodeEmbedder, error) {
	var st syscall.Stat_t
	err := syscall.Stat(rootPath, &st)
	if err != nil {
		return nil, err
	}

	// Create the LoopbackRoot structure with custom NewNode function
	root := &fs.LoopbackRoot{
		Path: rootPath,
		Dev:  uint64(st.Dev),
	}

	// Override NewNode to create cached nodes
	root.NewNode = func(rootData *fs.LoopbackRoot, parent *fs.Inode, name string, st *syscall.Stat_t) fs.InodeEmbedder {
		return &CachedLoopbackNode{
			LoopbackNode: fs.LoopbackNode{
				RootData: rootData,
			},
			cache: cache,
		}
	}

	// Create the root node as a cached node
	rootNode := &CachedLoopbackNode{
		LoopbackNode: fs.LoopbackNode{
			RootData: root,
		},
		cache: cache,
	}

	root.RootNode = rootNode
	return rootNode, nil
}

// CachedLoopbackNode is a loopback node with caching support
type CachedLoopbackNode struct {
	fs.LoopbackNode
	cache CacheManager
}

var _ = (fs.NodeGetxattrer)((*CachedLoopbackNode)(nil))
var _ = (fs.NodeSetxattrer)((*CachedLoopbackNode)(nil))
var _ = (fs.NodeRemovexattrer)((*CachedLoopbackNode)(nil))
var _ = (fs.NodeOpener)((*CachedLoopbackNode)(nil))
var _ = (fs.NodeCreater)((*CachedLoopbackNode)(nil))

// idFromStat computes stable attributes from stat
func idFromStat(rootDev uint64, st *syscall.Stat_t) fs.StableAttr {
	swapped := (uint64(st.Dev) << 32) | (uint64(st.Dev) >> 32)
	swappedRootDev := (rootDev << 32) | (rootDev >> 32)
	return fs.StableAttr{
		Mode: uint32(st.Mode),
		Gen:  1,
		Ino:  (swapped ^ swappedRootDev) ^ st.Ino,
	}
}

// Lookup overrides the parent Lookup to check cache in parallel
func (n *CachedLoopbackNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	p := filepath.Join(n.Path(n.Root()), name)
	fullPath := filepath.Join(n.RootData.Path, p)

	st := syscall.Stat_t{}
	err := syscall.Lstat(fullPath, &st)
	if err != nil {
		return nil, fs.ToErrno(err)
	}

	out.Attr.FromStat(&st)

	// Create new cached node
	node := &CachedLoopbackNode{
		LoopbackNode: fs.LoopbackNode{
			RootData: n.RootData,
		},
		cache: n.cache,
	}

	ch := n.NewInode(ctx, node, idFromStat(n.RootData.Dev, &st))
	return ch, 0
}

// Create handles file creation with caching support
func (n *CachedLoopbackNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	p := filepath.Join(n.Path(n.Root()), name)
	fullPath := filepath.Join(n.RootData.Path, p)

	// Remove O_APPEND flag as per loopback implementation
	flags = flags &^ syscall.O_APPEND

	// Create and open the file
	fd, err := syscall.Open(fullPath, int(flags)|os.O_CREATE, mode)
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	// Preserve owner if running as root
	n.preserveOwner(ctx, fullPath)

	// Get file stats
	st := syscall.Stat_t{}
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, nil, 0, fs.ToErrno(err)
	}

	// Create new cached node
	node := &CachedLoopbackNode{
		LoopbackNode: fs.LoopbackNode{
			RootData: n.RootData,
		},
		cache: n.cache,
	}

	ch := n.NewInode(ctx, node, idFromStat(n.RootData.Dev, &st))
	out.FromStat(&st)

	// Create file handle with caching enabled for writes
	//log.Printf("Created file %s, enabling write caching", fullPath)
	fh := &CachedFileHandle{
		fd:       fd,
		path:     fullPath,
		cache:    n.cache,
		isWrite:  true,
		cacheOps: true,
	}

	return ch, fh, 0, 0
}

// preserveOwner sets uid and gid of path according to the caller information in ctx
func (n *CachedLoopbackNode) preserveOwner(ctx context.Context, path string) error {
	if syscall.Getuid() != 0 {
		return nil
	}
	caller, ok := fuse.FromContext(ctx)
	if !ok {
		return nil
	}
	return syscall.Lchown(path, int(caller.Uid), int(caller.Gid))
}

// eagerFetchAndCache reads the entire file and caches it, returning the cached file descriptor
// This is used when we want to immediately populate the cache for read-only access
func (n *CachedLoopbackNode) eagerFetchAndCache(fullPath string, st *syscall.Stat_t) (cachedFd int, cachedPath string, sha256sum string, err error) {
	// Open the original file for reading
	f, err := os.Open(fullPath)
	if err != nil {
		return -1, "", "", err
	}
	defer f.Close()

	// Create a cache writer
	cw, err := n.cache.CreateTemp()
	if err != nil {
		return -1, "", "", err
	}

	io.Copy(io.Writer(cw), f)

	// Commit the cache
	committedSHA, err := cw.Commit()
	if err != nil || committedSHA == "" {
		return -1, "", "", err
	}

	// Set xattr with SHA256 and mtime
	fileMtime := time.Unix(st.Mtim.Unix())
	xattrValue := encodeCacheXattr(committedSHA, fileMtime, st.Size)
	unix.Setxattr(fullPath, xattrCacheName, []byte(xattrValue), 0)

	// Look up the cached file path
	cachedPath, err = n.cache.Lookup(committedSHA)
	if err != nil || cachedPath == "" {
		return -1, "", "", fmt.Errorf("failed to lookup cached file: %w", err)
	}

	// Open the cached file for reading
	cachedFd, err = syscall.Open(cachedPath, syscall.O_RDONLY, 0)
	if err != nil {
		return -1, "", "", err
	}

	return cachedFd, cachedPath, committedSHA, nil
}

// Open handles file opening with cache redirection
func (n *CachedLoopbackNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	// Check if this is a write operation
	isWrite := (flags&syscall.O_WRONLY != 0) || (flags&syscall.O_RDWR != 0)

	if isWrite {
		// Open the real file for writing
		fd, err := syscall.Open(fullPath, int(flags), 0)
		if err != nil {
			return nil, 0, fs.ToErrno(err)
		}

		//log.Printf("Opened file %s for writing", fullPath)
		return &CachedFileHandle{
			fd:       fd,
			path:     fullPath,
			cache:    n.cache,
			isWrite:  true,
			cacheOps: true, // Start with cache ops enabled
		}, 0, 0
	}

	// For read operations, check cache with mtime verification
	var st syscall.Stat_t
	if err := syscall.Stat(fullPath, &st); err != nil {
		return nil, 0, fs.ToErrno(err)
	}
	fileMtime := time.Unix(st.Mtim.Unix())

	// Try to read xattr with SHA256 and mtime
	var xattrBuf [128]byte // Enough for "sha256:mtime_sec.mtime_nsec:size"
	sz, err := unix.Getxattr(fullPath, xattrCacheName, xattrBuf[:])
	if err == nil && sz > 0 {
		xattrValue := string(xattrBuf[:sz])
		cachedSHA, cachedMtime, cachedSize, ok := decodeCacheXattr(xattrValue)
		if ok && cachedMtime.Equal(fileMtime) && cachedSize == st.Size {
			// Mtime matches, try to use cache
			cachedPath, err := n.cache.Lookup(cachedSHA)
			if err == nil && cachedPath != "" {
				// Open the cached file instead
				fd, err := syscall.Open(cachedPath, syscall.O_RDONLY, 0)
				if err == nil {
					// Successfully opened cached file - CACHE HIT
					if GlobalCacheMetrics != nil {
						GlobalCacheMetrics.CacheHit.Inc()
					}
					//log.Printf("Cache hit for %s (mtime match)", fullPath)
					return &CachedFileHandle{
						fd:         fd,
						path:       fullPath,
						cache:      n.cache,
						isWrite:    false,
						fromCache:  true,
						cachedStat: &st,
					}, fuse.FOPEN_KEEP_CACHE, 0
				}
				// Fall through if cached file can't be opened
			}
			// Have xattr but cache file doesn't exist - CACHE MISS
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheMiss.Inc()
			}
			//log.Printf("Cache miss for %s (cache file not found)", fullPath)

			// Eager fetch if mtime is older than 1 day
			if time.Since(fileMtime) > 24*time.Hour {
				// File is old, eagerly fetch and cache
				if cachedFd, _, _, eagerErr := n.eagerFetchAndCache(fullPath, &st); eagerErr == nil {
					if GlobalCacheMetrics != nil {
						GlobalCacheMetrics.CacheFetched.Inc()
					}
					// Successfully fetched and cached
					return &CachedFileHandle{
						fd:         cachedFd,
						path:       fullPath,
						cache:      n.cache,
						isWrite:    false,
						fromCache:  true,
						cachedStat: &st,
					}, fuse.FOPEN_KEEP_CACHE, 0
				}
				// Fall through if eager fetch failed
			}
		} else if ok {
			// SHA found but mtime doesn't match - CACHE MISS
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheStale.Inc()
			}
			//log.Printf("Cache miss for %s (mtime mismatch: cached=%v, current=%v)", fullPath, cachedMtime, fileMtime)
		}
	} else {
		// No xattr - file not cached - CACHE MISS
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.CacheNotExist.Inc()
		}
		//log.Printf("Cache not exist for %s", fullPath)

		// Eager fetch: immediately cache the file for read-only access
		if cachedFd, _, _, eagerErr := n.eagerFetchAndCache(fullPath, &st); eagerErr == nil {
			// Successfully fetched and cached
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheFetched.Inc()
			}
			return &CachedFileHandle{
				fd:         cachedFd,
				path:       fullPath,
				cache:      n.cache,
				isWrite:    false,
				fromCache:  true,
				cachedStat: &st,
			}, fuse.FOPEN_KEEP_CACHE, 0
		}
		// Fall through if eager fetch failed
	}

	//log.Printf("Opening file %s for reading (no cache)", fullPath)
	// Open the real file for reading
	fd, err := syscall.Open(fullPath, int(flags), 0)
	if err != nil {
		return nil, 0, fs.ToErrno(err)
	}

	// For sequential reads, we'll cache the content
	return &CachedFileHandle{
		fd:       fd,
		path:     fullPath,
		cache:    n.cache,
		isWrite:  false,
		cacheOps: true,
	}, 0, 0
}

// Getxattr retrieves extended attributes
func (n *CachedLoopbackNode) Getxattr(ctx context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	if attr == xattrCacheName {
		return 0, syscall.ENODATA
	}
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	sz, err := unix.Getxattr(fullPath, attr, dest)
	if err != nil {
		return 0, fs.ToErrno(err)
	}

	return uint32(sz), 0
}

// Setxattr sets extended attributes
func (n *CachedLoopbackNode) Setxattr(ctx context.Context, attr string, data []byte, flags uint32) syscall.Errno {
	if attr == xattrCacheName {
		return syscall.EPERM
	}
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	err := unix.Setxattr(fullPath, attr, data, int(flags))
	return fs.ToErrno(err)
}

// Removexattr removes extended attributes
func (n *CachedLoopbackNode) Removexattr(ctx context.Context, attr string) syscall.Errno {
	if attr == xattrCacheName {
		return syscall.EPERM
	}
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	err := unix.Removexattr(fullPath, attr)
	return fs.ToErrno(err)
}

// CachedFileHandle wraps a file descriptor with caching capabilities
type CachedFileHandle struct {
	fd         int
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

// Read implements FileReader
func (f *CachedFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, err := syscall.Pread(f.fd, dest, off)
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

	n, err := syscall.Pwrite(f.fd, data, off)
	if err != nil {
		return 0, fs.ToErrno(err)
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

	newOff, err := unix.Seek(f.fd, int64(off), int(whence))
	if err != nil {
		return 0, fs.ToErrno(err)
	}

	return uint64(newOff), 0
}

// Flush implements FileFlusher - commits cache on flush
func (f *CachedFileHandle) Flush(ctx context.Context) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Since Flush() may be called for each dup'd fd, we don't
	// want to really close the file, we just want to flush. This
	// is achieved by closing a dup'd fd.
	newFd, err := syscall.Dup(f.fd)

	if err != nil {
		return fs.ToErrno(err)
	}
	err = syscall.Close(newFd)
	if err != nil {
		return fs.ToErrno(err)
	}

	// Mark as flushed
	f.flushed = true

	// Commit cache if we were writing sequentially
	if f.cacheOps && f.isWrite && f.cacheWrite != nil {
		// Now commit the cache
		committedSHA, err := f.cacheWrite.Commit()
		if err == nil && committedSHA != "" {
			// Get mtime from file after flush
			var st syscall.Stat_t
			if statErr := syscall.Fstat(f.fd, &st); statErr == nil {
				fileMtime := time.Unix(st.Mtim.Unix())
				// Encode SHA256 and mtime into xattr
				xattrValue := encodeCacheXattr(committedSHA, fileMtime, st.Size)
				unix.Setxattr(f.path, xattrCacheName, []byte(xattrValue), 0)
				//log.Printf("Set xattr for %s: SHA=%s, mtime=%v, err=%v", f.path, committedSHA, fileMtime, setErr)
				f.cacheFlushed = true
			}
		}
		f.cacheWrite = nil
		f.cacheOps = false // Disable further caching after flush
	} else if f.cacheOps && !f.isWrite && f.cacheWrite != nil && f.bytesWritten > 0 {
		// For reads, compute SHA256 and set xattr with mtime
		committedSHA, err := f.cacheWrite.Commit()
		if err == nil && committedSHA != "" {
			// Get current mtime
			var st syscall.Stat_t
			if statErr := syscall.Fstat(f.fd, &st); statErr == nil {
				fileMtime := time.Unix(st.Mtim.Unix())
				xattrValue := encodeCacheXattr(committedSHA, fileMtime, st.Size)
				unix.Setxattr(f.path, xattrCacheName, []byte(xattrValue), 0)
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

	return fs.ToErrno(syscall.Close(f.fd))
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

// Getlk implements FileGetlker - get file lock
func (f *CachedFileHandle) Getlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32, out *fuse.FileLock) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	flk := syscall.Flock_t{}
	lk.ToFlockT(&flk)
	errno := fs.ToErrno(syscall.FcntlFlock(uintptr(f.fd), _OFD_GETLK, &flk))
	out.FromFlockT(&flk)
	return errno
}

// Setlk implements FileSetlker - set file lock (non-blocking)
func (f *CachedFileHandle) Setlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) syscall.Errno {
	return f.setLock(ctx, owner, lk, flags, false)
}

// Setlkw implements FileSetlkwer - set file lock (blocking)
func (f *CachedFileHandle) Setlkw(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) syscall.Errno {
	return f.setLock(ctx, owner, lk, flags, true)
}

// setLock is the common implementation for Setlk and Setlkw
func (f *CachedFileHandle) setLock(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32, blocking bool) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()

	if (flags & fuse.FUSE_LK_FLOCK) != 0 {
		var op int
		switch lk.Typ {
		case syscall.F_RDLCK:
			op = syscall.LOCK_SH
		case syscall.F_WRLCK:
			op = syscall.LOCK_EX
		case syscall.F_UNLCK:
			op = syscall.LOCK_UN
		default:
			return syscall.EINVAL
		}
		if !blocking {
			op |= syscall.LOCK_NB
		}
		return fs.ToErrno(syscall.Flock(f.fd, op))
	} else {
		flk := syscall.Flock_t{}
		lk.ToFlockT(&flk)
		var op int
		if blocking {
			op = _OFD_SETLKW
		} else {
			op = _OFD_SETLK
		}
		return fs.ToErrno(syscall.FcntlFlock(uintptr(f.fd), op, &flk))
	}
}

func (f *CachedFileHandle) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if errno := f.setAttr(ctx, in); errno != 0 {
		return errno
	}

	return f.Getattr(ctx, out)
}

func (f *CachedFileHandle) fchmod(mode uint32) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fs.ToErrno(syscall.Fchmod(f.fd, mode))
}

func (f *CachedFileHandle) fchown(uid, gid int) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fs.ToErrno(syscall.Fchown(f.fd, uid, gid))
}

func (f *CachedFileHandle) ftruncate(sz uint64) syscall.Errno {
	return fs.ToErrno(syscall.Ftruncate(f.fd, int64(sz)))
}

func (f *CachedFileHandle) setAttr(ctx context.Context, in *fuse.SetAttrIn) syscall.Errno {
	var errno syscall.Errno
	if mode, ok := in.GetMode(); ok {
		if errno := f.fchmod(mode); errno != 0 {
			return errno
		}
	}

	uid32, uOk := in.GetUID()
	gid32, gOk := in.GetGID()
	if uOk || gOk {
		uid := -1
		gid := -1

		if uOk {
			uid = int(uid32)
		}
		if gOk {
			gid = int(gid32)
		}
		if errno := f.fchown(uid, gid); errno != 0 {
			return errno
		}
	}

	mtime, mok := in.GetMTime()
	atime, aok := in.GetATime()

	if mok || aok {
		ap := &atime
		mp := &mtime
		if !aok {
			ap = nil
		}
		if !mok {
			mp = nil
		}
		errno = f.utimens(ap, mp)
		if errno != 0 {
			return errno
		}
	}

	if sz, ok := in.GetSize(); ok {
		if errno := f.ftruncate(sz); errno != 0 {
			return errno
		}
	}
	return fs.OK
}

func (f *CachedFileHandle) Getattr(ctx context.Context, a *fuse.AttrOut) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fromCache && f.cachedStat != nil {
		a.FromStat(f.cachedStat)
		return fs.OK
	}
	st := syscall.Stat_t{}
	err := syscall.Fstat(f.fd, &st)
	if err != nil {
		return fs.ToErrno(err)
	}
	a.FromStat(&st)

	return fs.OK
}

func (f *CachedFileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := syscall.Fsync(f.fd)
	if err != nil {
		return fs.ToErrno(err)
	}
	return fs.OK
}

func (f *CachedFileHandle) Allocate(ctx context.Context, off uint64, sz uint64, mode uint32) syscall.Errno {
	f.mu.Lock()
	defer f.mu.Unlock()
	err := unix.Fallocate(f.fd, mode, int64(off), int64(sz))
	if err != nil {
		return fs.ToErrno(err)
	}
	return fs.OK
}

func (f *CachedFileHandle) Ioctl(ctx context.Context, cmd uint32, arg uint64, input []byte, output []byte) (result int32, errno syscall.Errno) {
	f.mu.Lock()
	defer f.mu.Unlock()

	argWord := uintptr(arg)
	iot := (cmd >> 30) & 3
	switch iot {
	case 1: // IOT_READ
		argWord = uintptr(unsafe.Pointer(&input[0]))
	case 2: // IOT_WRITE
		argWord = uintptr(unsafe.Pointer(&output[0]))
	}

	res, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(f.fd), uintptr(cmd), argWord)
	return int32(res), fs.ToErrno(errno)
}

// Utimens - file handle based version of loopbackFileSystem.Utimens()
func (f *CachedFileHandle) utimens(a *time.Time, m *time.Time) syscall.Errno {
	var ts [2]syscall.Timespec
	ts[0] = fuse.UtimeToTimespec(a)
	ts[1] = fuse.UtimeToTimespec(m)
	err := futimens(int(f.fd), &ts)
	return fs.ToErrno(err)
}

// futimens - futimens(3) calls utimensat(2) with "pathname" set to null and
// "flags" set to zero
func futimens(fd int, times *[2]syscall.Timespec) (err error) {
	_, _, e1 := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(fd), 0, uintptr(unsafe.Pointer(times)), uintptr(0), 0, 0)
	if e1 != 0 {
		err = syscall.Errno(e1)
	}
	return
}
