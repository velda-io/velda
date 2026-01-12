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
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
)

const (
	// OFD (Open File Description) lock constants for fcntl
	_OFD_GETLK  = 36
	_OFD_SETLK  = 37
	_OFD_SETLKW = 38

	// Number of background workers for eager fetching
	eagerFetchWorkers = 200

	// Number of workers for stat/xattr prefetching
	statWorkers = 400

	// Maximum number of children to prefetch stats for
	maxStatPrefetch = 128

	// Maximum file size for small file prefetching (128KB)
	maxSmallFileSize = 128 * 1024

	// Maximum number of small files to prefetch per directory
	maxSmallFilePrefetch = 512
)

// eagerFetchJob represents a background job to fetch and cache a file
type eagerFetchJob struct {
	fullPath   string
	st         syscall.Stat_t
	fileHandle *CachedFileHandle
}

// statJob represents a background job to prefetch stat and xattr
type statJob struct {
	fullPath string
}

// MountContext holds all shared mount-related information
type MountContext struct {
	rootData        *fs.LoopbackRoot
	cache           CacheManager
	eagerFetchQueue chan *eagerFetchJob
	statQueue       chan *statJob
	workersDone     chan struct{}
	workersWg       sync.WaitGroup
	statWorkersWg   sync.WaitGroup
	SnapshotMode    bool // When true, maximize caching for read-only snapshot workloads
	NoCacheMode     bool // When true, only set cache keys during write, do not read/write cache data
}

// NewCachedLoopbackRoot creates a new cached loopback root node
func NewCachedLoopbackRoot(rootPath string, cache CacheManager, snapshotMode bool, noCacheMode bool) (fs.InodeEmbedder, error) {
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

	// Create the shared mount context
	mountCtx := &MountContext{
		rootData:        root,
		cache:           cache,
		eagerFetchQueue: make(chan *eagerFetchJob, 1000), // Buffered channel for job queue
		statQueue:       make(chan *statJob, 1000),       // Buffered channel for stat jobs
		workersDone:     make(chan struct{}),
		SnapshotMode:    snapshotMode,
		NoCacheMode:     noCacheMode,
	}

	// Create the root node using the mount context
	rootNode := &CachedLoopbackNode{
		LoopbackNode: fs.LoopbackNode{
			RootData: root,
		},
		mountCtx: mountCtx,
	}

	// Override NewNode to create cached nodes
	root.NewNode = func(rootData *fs.LoopbackRoot, parent *fs.Inode, name string, st *syscall.Stat_t) fs.InodeEmbedder {
		return &CachedLoopbackNode{
			LoopbackNode: fs.LoopbackNode{
				RootData: rootData,
			},
			mountCtx: mountCtx,
		}
	}

	// Start background workers for eager fetching
	rootNode.startEagerFetchWorkers()

	root.RootNode = rootNode
	return rootNode, nil
}

// startEagerFetchWorkers starts background workers to process eager fetch jobs
func (n *CachedLoopbackNode) startEagerFetchWorkers() {
	for i := 0; i < eagerFetchWorkers; i++ {
		n.mountCtx.workersWg.Add(1)
		go n.eagerFetchWorker()
	}
	// Start stat workers
	for i := 0; i < statWorkers; i++ {
		n.mountCtx.statWorkersWg.Add(1)
		go n.statWorker()
	}
}

// stopEagerFetchWorkers signals workers to stop and waits for them to finish
func (n *CachedLoopbackNode) stopEagerFetchWorkers() {
	close(n.mountCtx.workersDone)
	n.mountCtx.workersWg.Wait()
	n.mountCtx.statWorkersWg.Wait()
}

var _ = (fs.NodeOnForgetter)((*CachedLoopbackNode)(nil))

func (n *CachedLoopbackNode) OnForget() {
	if n.IsRoot() {
		// Stop background workers when root node is forgotten (unmounted)
		n.stopEagerFetchWorkers()
	}
}

// eagerFetchWorker is a background worker that processes eager fetch jobs
func (n *CachedLoopbackNode) eagerFetchWorker() {
	defer n.mountCtx.workersWg.Done()

	buf := make([]byte, 1024*1024) // 1MB buffer for copying files
	for {
		select {
		case <-n.mountCtx.workersDone:
			return
		case job := <-n.mountCtx.eagerFetchQueue:
			if job == nil {
				return
			}
			n.processEagerFetch(job, buf)
		}
	}
}

// statWorker is a background worker that prefetches stat and xattr data
func (n *CachedLoopbackNode) statWorker() {
	defer n.mountCtx.statWorkersWg.Done()

	var xattrBuf [128]byte // Reusable buffer for xattr reads
	for {
		select {
		case <-n.mountCtx.workersDone:
			return
		case job := <-n.mountCtx.statQueue:
			if job == nil {
				return
			}
			// Invoke stat - this warms the kernel buffer cache
			var st syscall.Stat_t
			syscall.Lstat(job.fullPath, &st)
			// Invoke getxattr - this also warms the kernel buffer cache
			unix.Lgetxattr(job.fullPath, xattrCacheName, xattrBuf[:])
		}
	}
}

// processEagerFetch processes a single eager fetch job
func (n *CachedLoopbackNode) processEagerFetch(job *eagerFetchJob, buf []byte) {
	// Fetch and cache the file
	// In snapshot mode, skip if no cache key exists
	skipIfNoKey := n.mountCtx.SnapshotMode
	cachedFd, _, sha256sum, err := n.eagerFetchAndCacheSync(job.fullPath, &job.st, n.mountCtx.cache, buf, skipIfNoKey)
	if err != nil {
		// Failed to fetch, file handle continues using real fd (if exists)
		return
	}

	// Successfully fetched - update the file handle to use cached fd (if exists)
	if job.fileHandle != nil {
		job.fileHandle.switchToCachedFd(cachedFd, &job.st)
	} else {
		// No file handle - just close the cached fd since we only wanted to populate cache
		syscall.Close(cachedFd)
	}

	if GlobalCacheMetrics != nil {
		GlobalCacheMetrics.CacheFetched.Inc()
	}

	// Set xattr with SHA256 and mtime
	fileMtime := time.Unix(job.st.Mtim.Unix())
	xattrValue := encodeCacheXattr(sha256sum, fileMtime, job.st.Size)
	unix.Lsetxattr(job.fullPath, xattrCacheName, []byte(xattrValue), 0)
}

// CachedLoopbackNode is a loopback node with caching support
type CachedLoopbackNode struct {
	fs.LoopbackNode
	mountCtx *MountContext

	// prefetched marks whether this inode's directory-prefetch has run
	prefetchedMetadata uint32
	prefetchedData     uint32
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
	curPath := filepath.Join(n.RootData.Path, n.Path(n.Root()))
	n.prefetchDirStats(curPath)
	fullPath := filepath.Join(n.RootData.Path, n.Path(n.Root()), name)

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
		mountCtx: n.mountCtx,
	}

	ch := n.NewInode(ctx, node, idFromStat(n.RootData.Dev, &st))

	// If this is a directory lookup, prefetch stats for its children
	if st.Mode&syscall.S_IFMT == syscall.S_IFDIR {
		// Call prefetch on the new node so the prefetch state is stored
		// with the directory inode itself.
		node.prefetchDirStats(fullPath)
	}

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
		mountCtx: n.mountCtx,
	}

	ch := n.NewInode(ctx, node, idFromStat(n.RootData.Dev, &st))
	out.FromStat(&st)

	// In NoCacheMode, use NoCacheFileHandle
	if n.mountCtx.NoCacheMode {
		fh := &NoCacheFileHandle{
			LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
			path:         fullPath,
			isWrite:      true,
			hasher:       sha256.New(),
		}
		return ch, fh, 0, 0
	}

	// Create file handle with caching enabled for writes
	//log.Printf("Created file %s, enabling write caching", fullPath)
	fh := &CachedFileHandle{
		LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
		path:         fullPath,
		cache:        n.mountCtx.cache,
		isWrite:      true,
		cacheOps:     true,
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

// eagerFetchAndCacheSync reads the entire file and caches it, returning the cached file descriptor
// This is the synchronous version used by background workers
// If skipIfNoKey is true, skip fetching when the file has no existing cache key (xattr)
func (n *CachedLoopbackNode) eagerFetchAndCacheSync(fullPath string, st *syscall.Stat_t, cache CacheManager, buf []byte, skipIfNoKey bool) (cachedFd int, cachedPath string, sha256sum string, err error) {
	// First, check if file already has a valid cache key
	var xattrBuf [128]byte
	sz, xattrErr := unix.Lgetxattr(fullPath, xattrCacheName, xattrBuf[:])
	if xattrErr == nil && sz > 0 {
		xattrValue := string(xattrBuf[:sz])
		cachedSHA, cachedMtime, cachedSize, ok := decodeCacheXattr(xattrValue)
		fileMtime := time.Unix(st.Mtim.Unix())

		if ok && cachedMtime.Equal(fileMtime) && cachedSize == st.Size {
			// Mtime and size match, check if cache exists
			cachedPath, lookupErr := cache.Lookup(cachedSHA)
			if lookupErr == nil && cachedPath != "" {
				// Cache already exists and is valid, try to open it
				cachedFd, openErr := syscall.Open(cachedPath, syscall.O_RDONLY, 0)
				if openErr == nil {
					// Successfully opened cached file - no need to fetch again
					return cachedFd, cachedPath, cachedSHA, nil
				}
				// Fall through if cached file can't be opened
			}
			// Fall through if cache lookup failed - will re-fetch and update
		}
		if skipIfNoKey {
			return -1, "", "", fmt.Errorf("cache key exists but is invalid (mtime or size mismatch)")
		}
	} else {
		// No xattr exists
		if skipIfNoKey {
			// Skip fetching when no cache key exists (snapshot mode)
			return -1, "", "", fmt.Errorf("no cache key and skipIfNoKey=true")
		}
	}

	// Open the original file for reading
	f, err := os.Open(fullPath)
	if err != nil {
		return -1, "", "", err
	}
	defer f.Close()

	// Create a cache writer
	cw, err := cache.CreateTemp()
	if err != nil {
		return -1, "", "", err
	}

	_, err = io.CopyBuffer(io.Writer(cw), f, buf)
	if err != nil {
		return -1, "", "", err
	}

	// Commit the cache
	committedSHA, err := cw.Commit()
	if err != nil || committedSHA == "" {
		return -1, "", "", err
	}

	// Set xattr with SHA256 and mtime
	fileMtime := time.Unix(st.Mtim.Unix())
	xattrValue := encodeCacheXattr(committedSHA, fileMtime, st.Size)
	unix.Lsetxattr(fullPath, xattrCacheName, []byte(xattrValue), 0)

	// Look up the cached file path
	cachedPath, err = cache.Lookup(committedSHA)
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
	flags &^= syscall.O_APPEND | fuse.FMODE_EXEC // Remove O_APPEND flag as per loopback implementation
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	// Check if this is a write operation
	isWrite := (flags&syscall.O_WRONLY != 0) || (flags&syscall.O_RDWR != 0)

	// In NoCacheMode, use NoCacheFileHandle
	if n.mountCtx.NoCacheMode {
		fd, err := syscall.Open(fullPath, int(flags), 0)
		if err != nil {
			return nil, 0, fs.ToErrno(err)
		}

		fh := &NoCacheFileHandle{
			LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
			path:         fullPath,
			isWrite:      isWrite,
		}
		// Initialize hasher for write operations
		if isWrite {
			fh.hasher = sha256.New()
		}
		return fh, 0, 0
	}

	if isWrite {
		// Open the real file for writing
		fd, err := syscall.Open(fullPath, int(flags), 0)
		if err != nil {
			return nil, 0, fs.ToErrno(err)
		}
		cacheEnabled := flags&syscall.O_TRUNC != 0 || flags&syscall.O_CREAT != 0

		//log.Printf("Opened file %s for writing", fullPath)
		return &CachedFileHandle{
			LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
			path:         fullPath,
			cache:        n.mountCtx.cache,
			isWrite:      true,
			cacheOps:     cacheEnabled, // Start with cache ops enabled if truncating or creating
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
	sz, err := unix.Lgetxattr(fullPath, xattrCacheName, xattrBuf[:])
	if err == nil && sz > 0 {
		xattrValue := string(xattrBuf[:sz])
		cachedSHA, cachedMtime, cachedSize, ok := decodeCacheXattr(xattrValue)
		if ok && cachedMtime.Equal(fileMtime) && cachedSize == st.Size {
			// Mtime matches, try to use cache
			cachedPath, err := n.mountCtx.cache.Lookup(cachedSHA)
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
						LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
						path:         fullPath,
						cache:        n.mountCtx.cache,
						isWrite:      false,
						fromCache:    true,
						cachedStat:   &st,
					}, fuse.FOPEN_KEEP_CACHE, 0
				}
				// Fall through if cached file can't be opened
			}
			// Have xattr but cache file doesn't exist - CACHE MISS
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheMiss.Inc()
			}
			//log.Printf("Cache miss for %s (cache file not found)", fullPath)

			// Eager fetch if mtime is older than 1 day - submit to background queue
			if time.Since(fileMtime) > 24*time.Hour {
				// Open the real file for now
				fd, err := syscall.Open(fullPath, int(flags), 0)
				if err != nil {
					return nil, 0, fs.ToErrno(err)
				}

				// Create file handle with real fd
				fh := &CachedFileHandle{
					LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
					path:         fullPath,
					cache:        n.mountCtx.cache,
					isWrite:      false,
					cacheOps:     false, // Disable lazy caching since we'll eager fetch
				}

				// Submit eager fetch job to front of queue (high priority)
				job := &eagerFetchJob{
					fullPath:   fullPath,
					st:         st,
					fileHandle: fh,
				}

				// Try to add to front of queue (non-blocking)
			queue:
				for {
					select {
					case n.mountCtx.eagerFetchQueue <- job:
						// Job queued successfully
						break queue
					default:
						select {
						case <-n.mountCtx.eagerFetchQueue: // Queue full, discard an old job and try again
						default:
						}
						// Queue full, continue with normal operation
					}
				}

				return fh, 0, 0
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

		// In snapshot mode, skip eager fetch for files without cache key
		// because we cannot write xattr to track the cached state
		if !n.mountCtx.SnapshotMode {
			// Eager fetch: submit to background queue for caching
			// Open the real file for now
			fd, err := syscall.Open(fullPath, int(flags), 0)
			if err != nil {
				return nil, 0, fs.ToErrno(err)
			}

			// Create file handle with real fd
			fh := &CachedFileHandle{
				LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
				path:         fullPath,
				cache:        n.mountCtx.cache,
				isWrite:      false,
				cacheOps:     false, // Disable lazy caching since we'll eager fetch
			}

			// Submit eager fetch job to front of queue (high priority)
			job := &eagerFetchJob{
				fullPath:   fullPath,
				st:         st,
				fileHandle: fh,
			}

		queue2:
			for {
				// Try to add to front of queue (non-blocking)
				select {
				case n.mountCtx.eagerFetchQueue <- job:
					break queue2
					// Job queued successfully
				default:
					select {
					case <-n.mountCtx.eagerFetchQueue: // Queue full, discard an old job and try again
					default:
					}
					// Queue full, continue with normal operation
				}
			}

			return fh, 0, 0
		}
	}

	//log.Printf("Opening file %s for reading (no cache)", fullPath)
	// No eager fetch needed - open the real file for reading with lazy caching
	var fd int
	fd, err = syscall.Open(fullPath, int(flags), 0)
	if err != nil {
		return nil, 0, fs.ToErrno(err)
	}

	// On first read-only open, prefetch small files from parent directory
	if flags&syscall.O_RDONLY == syscall.O_RDONLY || (flags&syscall.O_WRONLY == 0 && flags&syscall.O_RDWR == 0) {
		parentDir := filepath.Dir(fullPath)
		// Try to find the parent inode and invoke prefetch on that inode so the
		// prefetch flag is stored with the directory inode.
		if _, parent := n.Parent(); parent != nil {
			if ops := parent.Operations(); ops != nil {
				if pd, ok := ops.(*CachedLoopbackNode); ok {
					pd.prefetchSmallFiles(parentDir, fullPath)
				}
			}
		}
	}

	// For sequential reads, we'll cache the content
	return &CachedFileHandle{
		LoopbackFile: fs.NewLoopbackFile(fd).(*fs.LoopbackFile),
		path:         fullPath,
		cache:        n.mountCtx.cache,
		isWrite:      false,
		cacheOps:     true,
	}, 0, 0
}

// prefetchDirStats submits stat jobs for children of a directory
func (n *CachedLoopbackNode) prefetchDirStats(dirPath string) {
	// Ensure we only prefetch once per inode
	if !atomic.CompareAndSwapUint32(&n.prefetchedMetadata, 0, 1) {
		return
	}

	// Spawn async goroutine to do the actual prefetching
	go func() {
		// Open directory and read entries
		fd, err := syscall.Open(dirPath, syscall.O_DIRECTORY|syscall.O_RDONLY, 0)
		if err != nil {
			return
		}
		defer syscall.Close(fd)

		// Read directory entries
		buf := make([]byte, 8192)
		count := 0
		for count < maxStatPrefetch {
			bytesRead, err := unix.Getdents(fd, buf)
			if err != nil || bytesRead == 0 {
				break
			}

			// Parse entries
			offset := 0
			for offset < bytesRead && count < maxStatPrefetch {
				entry := (*unix.Dirent)(unsafe.Pointer(&buf[offset]))
				if entry.Reclen == 0 {
					break
				}

				// Extract name (name starts after fixed dirent structure)
				nameStart := offset + 19 // On Linux amd64: ino(8) + off(8) + reclen(2) + type(1) = 19
				nameEnd := offset + int(entry.Reclen)
				nameBytes := buf[nameStart:nameEnd]
				nameLen := 0
				for nameLen < len(nameBytes) && nameBytes[nameLen] != 0 {
					nameLen++
				}
				name := string(nameBytes[:nameLen])

				// Skip . and ..
				if name != "." && name != ".." {
					fullPath := filepath.Join(dirPath, name)
					// Submit stat job (non-blocking)
					select {
					case n.mountCtx.statQueue <- &statJob{fullPath: fullPath}:
						count++
					default:
						// Queue full, stop prefetching
						return
					}
				}

				offset += int(entry.Reclen)
			}
		}
	}()
}

// prefetchSmallFiles eagerly fetches small files (<128KB) from a directory
// This is called on first read-only open to warm the cache for sibling files
// excludePath can be used to exclude the current file from prefetching
func (n *CachedLoopbackNode) prefetchSmallFiles(dirPath string, excludePath string) {
	// Ensure we only prefetch once per inode
	if !atomic.CompareAndSwapUint32(&n.prefetchedData, 0, 1) {
		return
	}

	// Spawn async goroutine to do the actual prefetching
	go func() {
		// Open directory and read entries
		fd, err := syscall.Open(dirPath, syscall.O_DIRECTORY|syscall.O_RDONLY, 0)
		if err != nil {
			return
		}
		defer syscall.Close(fd)

		// Read directory entries
		buf := make([]byte, 8192)
		count := 0
		for count < maxSmallFilePrefetch {
			bytesRead, err := unix.Getdents(fd, buf)
			if err != nil || bytesRead == 0 {
				break
			}

			// Parse entries
			offset := 0
			for offset < bytesRead && count < maxSmallFilePrefetch {
				entry := (*unix.Dirent)(unsafe.Pointer(&buf[offset]))
				if entry.Reclen == 0 {
					break
				}

				// Extract name
				nameStart := offset + 19
				nameEnd := offset + int(entry.Reclen)
				nameBytes := buf[nameStart:nameEnd]
				nameLen := 0
				for nameLen < len(nameBytes) && nameBytes[nameLen] != 0 {
					nameLen++
				}
				name := string(nameBytes[:nameLen])

				// Skip . and ..
				if name != "." && name != ".." {
					fullPath := filepath.Join(dirPath, name)

					// Skip the file being opened (exclude path)
					if fullPath == excludePath {
						offset += int(entry.Reclen)
						continue
					}

					// Check file size and type
					var st syscall.Stat_t
					if err := syscall.Lstat(fullPath, &st); err != nil {
						offset += int(entry.Reclen)
						continue
					}

					// Only prefetch regular files under maxSmallFileSize
					if st.Mode&syscall.S_IFMT == syscall.S_IFREG && st.Size > 0 && st.Size <= maxSmallFileSize {
						// Submit eager fetch job (non-blocking)
						job := &eagerFetchJob{
							fullPath:   fullPath,
							st:         st,
							fileHandle: nil, // No file handle for prefetch
						}

						select {
						case n.mountCtx.eagerFetchQueue <- job:
							count++
						default:
							// Queue full, stop prefetching
							return
						}
					}
				}

				offset += int(entry.Reclen)
			}
		}
	}()
}

// Readdir overrides the parent to prefetch stats for directory children
func (n *CachedLoopbackNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	p := n.Path(n.Root())
	fullPath := filepath.Join(n.RootData.Path, p)

	// Prefetch stats for all children
	n.prefetchDirStats(fullPath)

	// Call parent implementation
	return fs.NewLoopbackDirStream(fullPath)
}

// Getxattr retrieves extended attributes
func (n *CachedLoopbackNode) Getxattr(ctx context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	if attr == xattrCacheName {
		return 0, syscall.ENODATA
	}
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	sz, err := unix.Lgetxattr(fullPath, attr, dest)
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

	err := unix.Lsetxattr(fullPath, attr, data, int(flags))
	return fs.ToErrno(err)
}

// Removexattr removes extended attributes
func (n *CachedLoopbackNode) Removexattr(ctx context.Context, attr string) syscall.Errno {
	if attr == xattrCacheName {
		return syscall.EPERM
	}
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	err := unix.Lremovexattr(fullPath, attr)
	return fs.ToErrno(err)
}

func (n *CachedLoopbackNode) Listxattr(ctx context.Context, dest []byte) (uint32, syscall.Errno) {
	p := filepath.Join(n.Path(n.Root()))
	fullPath := filepath.Join(n.RootData.Path, p)

	// Get all xattrs first
	sz, err := unix.Llistxattr(fullPath, dest)
	if err != nil {
		return 0, fs.ToErrno(err)
	}

	// Filter out xattrCacheName from the list
	if sz > 0 && sz <= len(dest) {
		names := dest[:sz]
		filtered := make([]byte, 0, sz)
		start := 0
		for i := 0; i < len(names); i++ {
			if names[i] == 0 {
				name := string(names[start:i])
				if name != xattrCacheName {
					filtered = append(filtered, names[start:i+1]...)
				}
				start = i + 1
			}
		}
		copy(dest, filtered)
		return uint32(len(filtered)), 0
	}

	return uint32(sz), 0
}
