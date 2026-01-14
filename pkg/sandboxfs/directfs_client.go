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
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/fileserver"
)

const (
	// Number of background workers for prefetching
	prefetchWorkers = 200
)

type Response struct {
	errno syscall.Errno
	data  []byte
}

// DirectFSClient manages a FUSE filesystem that connects to a remote fileserver
// using the fileserver protocol and uses sandboxfs cache manager
type DirectFSClient struct {
	serverAddr string
	conn       net.Conn

	// Connection state
	mu       sync.Mutex
	seq      uint32
	pending  map[uint32]chan Response
	rootFh   unix.FileHandle
	rootAttr fileserver.FileAttr

	// Inode tracking for pre-loaded metadata
	inodeMu sync.RWMutex
	inodes  map[uint64]*fs.Inode // Map from inode number to FUSE inode

	// Cache from sandboxfs
	cache *DirectoryCacheManager

	// Prefetch queue and workers
	prefetchQueue chan *prefetchJob
	workersWg     sync.WaitGroup

	// FUSE state
	fuseServer *fuse.Server
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// prefetchJob represents a background job to prefetch and cache a small file
type prefetchJob struct {
	client *DirectFSClient
	fh     unix.FileHandle
	attr   fileserver.FileAttr
}

// NewDirectFSClient creates a new direct filesystem client
func NewDirectFSClient(serverAddr string, cache *DirectoryCacheManager) *DirectFSClient {
	ctx, cancel := context.WithCancel(context.Background())
	client := &DirectFSClient{
		serverAddr:    serverAddr,
		pending:       make(map[uint32]chan Response),
		inodes:        make(map[uint64]*fs.Inode),
		cache:         cache,
		prefetchQueue: make(chan *prefetchJob, 1000),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start prefetch workers
	for i := 0; i < prefetchWorkers; i++ {
		client.workersWg.Add(1)
		go client.prefetchWorker()
	}

	return client
}

// Connect connects to the file server and performs mount
func (sc *DirectFSClient) Connect() (*SnapshotNode, error) {
	parts := strings.SplitN(sc.serverAddr, "@", 2)
	host := parts[0]
	var path string
	if len(parts) > 1 {
		path = parts[1]
	}
	conn, err := net.Dial("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	sc.conn = conn

	// Start response reader
	sc.wg.Add(1)
	go sc.readResponses()

	// Send mount request
	mountReq := fileserver.MountRequest{
		Version: fileserver.ProtocolVersion,
		Flags:   fileserver.FlagReadOnly,
		Path:    path, // Request root path
	}

	var mountResp fileserver.MountResponse
	err = sc.SendRequest(&mountReq, &mountResp)
	if err != nil {
		sc.conn.Close()
		return nil, fmt.Errorf("mount failed: %w", err)
	}
	// Store root file handle and attributes from mount response
	sc.rootFh = mountResp.Fh
	sc.rootAttr = mountResp.Attr
	return &SnapshotNode{
		client: sc,
		fh:     sc.rootFh,
		attr:   sc.rootAttr,
	}, nil
}

// Mount mounts the filesystem at the given mount point
func (sc *DirectFSClient) Mount(mountPoint string) (*VeldaServer, error) {
	root, err := sc.Connect()
	if err != nil {
		return nil, fmt.Errorf("failed to get root: %w", err)
	}
	// Set up FUSE options optimized for snapshots
	timeout := 1 * time.Hour // Long timeout for snapshots
	opts := &fs.Options{
		EntryTimeout: &timeout,
		AttrTimeout:  &timeout,
		MountOptions: fuse.MountOptions{
			AllowOther:  true,
			DirectMount: true,
			Name:        "veldafs-snapshot",
			FsName:      "snapshot",
		},
	}

	// Mount using go-fuse
	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to mount: %w", err)
	}

	sc.fuseServer = server

	return &VeldaServer{
		Server: server,
		Cache:  sc.cache,
	}, nil
}

// Unmount unmounts the filesystem
func (sc *DirectFSClient) Unmount() error {
	if sc.fuseServer != nil {
		return sc.fuseServer.Unmount()
	}
	return nil
}

// Stop stops the client
func (sc *DirectFSClient) Stop() {
	sc.cancel()
	if sc.conn != nil {
		sc.conn.Close()
	}
	close(sc.prefetchQueue)
	sc.workersWg.Wait()
	sc.wg.Wait()
}

// prefetchWorker is a background worker that processes prefetch jobs
func (sc *DirectFSClient) prefetchWorker() {
	defer sc.workersWg.Done()

	for job := range sc.prefetchQueue {
		if job == nil {
			return
		}
		sc.processPrefetchJob(job)
	}
}

// processPrefetchJob processes a single prefetch job
func (sc *DirectFSClient) processPrefetchJob(job *prefetchJob) {
	// Check if file has SHA256
	var zeroHash [32]byte
	if job.attr.Sha256 == zeroHash {
		return // Cannot cache without SHA256
	}

	sha256Hex := fmt.Sprintf("%x", job.attr.Sha256[:])

	// Check if already in cache
	cachedPath, err := sc.cache.Lookup(sha256Hex)
	if err == nil && cachedPath != "" {
		// Already cached
		return
	}

	// Read entire file from server
	readReq := fileserver.ReadRequest{
		Fh:     job.fh,
		Offset: 0,
		Size:   uint32(job.attr.Size),
	}

	readResp := fileserver.ReadResponse{}
	err = sc.SendRequest(&readReq, &readResp)
	if err != nil {
		return
	}

	// Write to cache
	writer, err := sc.cache.CreateTemp()
	if err != nil {
		return
	}

	if _, err := writer.Write(readResp.Data); err != nil {
		writer.Abort()
		return
	}

	computedHash, err := writer.Commit()
	if err != nil {
		writer.Abort()
		return
	}

	// Verify hash matches
	if computedHash == sha256Hex {
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.CacheSaved.Inc()
		}
	}
}

// nextSeq generates the next sequence number
func (sc *DirectFSClient) nextSeq() uint32 {
	result := atomic.AddUint32(&sc.seq, 1)
	if result == 0 {
		// Skip 0 as it's reserved for notifications
		result = atomic.AddUint32(&sc.seq, 1)
	}
	return result
}

// sendRequestWithData sends pre-serialized data and waits for response
func (sc *DirectFSClient) sendRequestWithData(data []byte, seq uint32, res fileserver.Serializable) error {
	// Create response channel
	respChan := make(chan Response, 1)
	sc.mu.Lock()
	sc.pending[seq] = respChan
	sc.mu.Unlock()

	// Send request
	sc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := sc.conn.Write(data)
	if err != nil {
		sc.mu.Lock()
		delete(sc.pending, seq)
		sc.mu.Unlock()
		return fmt.Errorf("write failed: %w", err)
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		if resp.errno != 0 {
			return resp.errno
		}
		rdr := bytes.NewReader(resp.data)
		if err := res.Deserialize(rdr); err != nil {
			return fmt.Errorf("failed to deserialize response: %w", err)
		}
		return nil
	case <-time.After(30 * time.Second):
		sc.mu.Lock()
		delete(sc.pending, seq)
		sc.mu.Unlock()
		return fmt.Errorf("request timeout")
	case <-sc.ctx.Done():
		return fmt.Errorf("client stopped")
	}
}

// SendRequest sends a request and waits for response
func (sc *DirectFSClient) SendRequest(req fileserver.Serializable, res fileserver.Serializable) error {
	seq := sc.nextSeq()

	var opCode uint32
	switch req.(type) {
	case *fileserver.MountRequest:
		opCode = fileserver.OpMount
	case *fileserver.LookupRequest:
		opCode = fileserver.OpLookup
	case *fileserver.ReadRequest:
		opCode = fileserver.OpRead
	case *fileserver.ReadDirRequest:
		opCode = fileserver.OpReadDir
	case *fileserver.ReadlinkRequest:
		opCode = fileserver.OpReadlink
	default:
		return fmt.Errorf("unknown request type")
	}
	data, err := fileserver.SerializeWithHeader(opCode, seq, req)
	if err != nil {
		return fmt.Errorf("failed to serialize request: %w", err)
	}

	return sc.sendRequestWithData(data, seq, res)
}

// readResponses reads responses from the connection
func (sc *DirectFSClient) readResponses() {
	defer sc.wg.Done()

	buf := make([]byte, 4096)
	for {
		select {
		case <-sc.ctx.Done():
			return
		default:
		}

		// Read header
		sc.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_, err := io.ReadFull(sc.conn, buf[:fileserver.HeaderSize])
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Read header error: %v\n", err)
			}
			return
		}

		// Parse header
		var header fileserver.Header
		if err := header.Deserialize(bytes.NewReader(buf[:fileserver.HeaderSize])); err != nil {
			fmt.Printf("Invalid header: %v\n", err)
			return
		}

		// Validate size
		if header.Size < fileserver.HeaderSize || header.Size > 10*1024*1024 {
			fmt.Printf("Invalid message size: %d\n", header.Size)
			return
		}

		// Read full message
		data := make([]byte, header.Size-fileserver.HeaderSize)

		if header.Size > fileserver.HeaderSize {
			_, err = io.ReadFull(sc.conn, data)
			if err != nil {
				fmt.Printf("Read data error: %v\n", err)
				return
			}
		}

		// Check if this is a DirDataNotification (opcode indicates notification)
		if header.Seq == 0 && header.Opcode == fileserver.OpDirDataNotification {
			// Handle DirDataNotification asynchronously
			go sc.handleDirDataNotification(data)
			continue
		}

		// Dispatch response
		sc.mu.Lock()
		respChan, ok := sc.pending[header.Seq]
		if ok {
			delete(sc.pending, header.Seq)
		}
		sc.mu.Unlock()

		if ok {
			respChan <- Response{errno: syscall.Errno(header.Opcode), data: data}
		}
	}
}

// handleDirDataNotification processes DirDataNotification from the server
func (sc *DirectFSClient) handleDirDataNotification(data []byte) {
	// Deserialize notification
	var notification fileserver.DirDataNotification
	if err := notification.Deserialize(bytes.NewReader(data)); err != nil {
		log.Printf("Failed to deserialize DirDataNotification: %v", err)
		return
	}

	// Find the inode for this directory
	sc.inodeMu.RLock()
	parentInode, exists := sc.inodes[notification.Ino]
	sc.inodeMu.RUnlock()

	if !exists {
		// Inode not in cache yet, skip pre-loading
		log.Printf("DirDataNotification for unknown inode %d, skipping", notification.Ino)
		return
	}

	// Get the SnapshotNode from the inode
	parentNode, ok := parentInode.Operations().(*SnapshotNode)
	if !ok {
		log.Printf("Failed to get SnapshotNode from inode %d", notification.Ino)
		return
	}

	// Add all child inodes to the FUSE inode tree
	ctx := context.Background()
	for _, entry := range notification.Entries {
		// Check if child already exists
		existingChild := parentInode.GetChild(entry.Name)
		if existingChild != nil {
			// Child already exists, skip
			continue
		}

		// Create child node
		childNode := &SnapshotNode{
			client: sc,
			fh:     entry.Fh,
			attr:   entry.Attr,
		}

		// Create persistent inode
		stable := fs.StableAttr{
			Mode: entry.Attr.Mode,
			Ino:  entry.Attr.Ino,
		}
		childInode := parentNode.NewPersistentInode(ctx, childNode, stable)

		// Add to parent's children (this makes it visible to readdir)
		parentInode.AddChild(entry.Name, childInode, false)
	}

	log.Printf("Pre-loaded %d entries for inode %d", len(notification.Entries), notification.Ino)
}

// SnapshotNode implements fs.InodeEmbedder for persistent inodes
type SnapshotNode struct {
	fs.Inode
	client         *DirectFSClient
	fh             unix.FileHandle     // File handle from server
	attr           fileserver.FileAttr // Cached attributes
	dirFetched     sync.Once           // Whether directory entries have been fetched
	dirFetchError  error
	prefetchedData uint32 // Atomic flag for whether small files have been prefetched
}

// Ensure SnapshotNode implements required interfaces
var _ fs.InodeEmbedder = (*SnapshotNode)(nil)
var _ fs.NodeGetattrer = (*SnapshotNode)(nil)
var _ fs.NodeLookuper = (*SnapshotNode)(nil)
var _ fs.NodeReaddirer = (*SnapshotNode)(nil)
var _ fs.NodeOpener = (*SnapshotNode)(nil)
var _ fs.NodeSetattrer = (*SnapshotNode)(nil)
var _ fs.NodeCreater = (*SnapshotNode)(nil)
var _ fs.NodeMkdirer = (*SnapshotNode)(nil)
var _ fs.NodeUnlinker = (*SnapshotNode)(nil)
var _ fs.NodeRmdirer = (*SnapshotNode)(nil)
var _ fs.NodeRenamer = (*SnapshotNode)(nil)
var _ fs.NodeReadlinker = (*SnapshotNode)(nil)

// Getattr returns file attributes
func (n *SnapshotNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	toFuseAttr(&n.attr, &out.Attr)
	return fs.OK
}

// fetchDirIfNeeded prefetches directory entries on first call
func (n *SnapshotNode) fetchDirIfNeeded(ctx context.Context) {
	n.dirFetched.Do(func() {
		// Send readdir request
		readDirReq := fileserver.ReadDirRequest{
			Fh: n.fh,
		}

		readDirResp := fileserver.ReadDirResponse{}
		err := n.client.SendRequest(&readDirReq, &readDirResp)
		if err != nil {
			n.dirFetchError = err
			return
		}
		for _, entry := range readDirResp.Entries {
			// Create child node
			childNode := &SnapshotNode{
				client: n.client,
				fh:     entry.Fh,
				attr:   entry.Attr,
			}

			// Create persistent inode
			stable := fs.StableAttr{
				Mode: entry.Attr.Mode,
				Ino:  entry.Attr.Ino,
			}
			child := n.NewPersistentInode(ctx, childNode, stable)

			// Add to children, don't replace existing child if it already exists
			n.AddChild(entry.Name, child, false)
		}

		// Start prefetching small files after directory fetch is complete
		n.prefetchSmallFiles() // Empty file handle means don't exclude any file
	})
}

// Lookup looks up a child node
func (n *SnapshotNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Prefetch directory on first lookup
	n.fetchDirIfNeeded(ctx)

	// Check if child already exists (from prefetch)
	existingChild := n.GetChild(name)
	if existingChild != nil {
		// Child exists from prefetch, return it
		if childNode, ok := existingChild.Operations().(*SnapshotNode); ok {
			toFuseAttr(&childNode.attr, &out.Attr)
			out.SetEntryTimeout(time.Hour)
			out.SetAttrTimeout(time.Hour)
			return existingChild, fs.OK
		}
	}

	// If directory has been fetched and child doesn't exist, return ENOENT
	// without making a server call
	if n.dirFetchError == nil {
		return nil, syscall.ENOENT
	}

	// Send lookup request for directories that haven't been fully prefetched
	lookupReq := fileserver.LookupRequest{
		ParentFh: n.fh,
		Name:     name,
	}

	lookupResp := fileserver.LookupResponse{}

	err := n.client.SendRequest(&lookupReq, &lookupResp)
	if err != nil {
		return nil, fs.ToErrno(err)
	}

	// Create child node
	childNode := &SnapshotNode{
		client: n.client,
		fh:     lookupResp.Fh,
		attr:   lookupResp.Attr,
	}

	// Convert attr to fuse.Attr
	toFuseAttr(&lookupResp.Attr, &out.Attr)

	// Set long timeouts for snapshots
	out.SetEntryTimeout(time.Hour)
	out.SetAttrTimeout(time.Hour)

	// Create persistent inode
	stable := fs.StableAttr{
		Mode: lookupResp.Attr.Mode,
		Ino:  lookupResp.Attr.Ino,
	}
	child := n.NewPersistentInode(ctx, childNode, stable)
	return child, fs.OK
}

// Readdir reads directory entries
func (n *SnapshotNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Fetch directory entries if not already done
	n.fetchDirIfNeeded(ctx)

	if n.dirFetchError != nil {
		return nil, fs.ToErrno(n.dirFetchError)
	}

	// Convert to fuse.DirEntry
	children := n.Children()
	entries := make([]fuse.DirEntry, 0, len(children))
	for name, entry := range children {
		entries = append(entries, fuse.DirEntry{
			Name: name,
			Ino:  entry.StableAttr().Ino,
			Mode: entry.StableAttr().Mode & unix.S_IFMT,
		})
	}
	return fs.NewListDirStream(entries), fs.OK
}

// prefetchSmallFiles eagerly fetches small files (<128KB) from a directory
// This is called on first read-only open to warm the cache for sibling files
func (n *SnapshotNode) prefetchSmallFiles() {
	// Ensure we only prefetch once per directory inode
	if !atomic.CompareAndSwapUint32(&n.prefetchedData, 0, 1) {
		return
	}

	// Spawn async goroutine to do the actual prefetching
	go func() {
		// Get all children of this directory
		children := n.Children()

		count := 0
		for _, child := range children {
			if count >= maxSmallFilePrefetch {
				break
			}

			// Get child node
			childNode, ok := child.Operations().(*SnapshotNode)
			if !ok {
				continue
			}

			// Only prefetch regular files under maxSmallFileSize
			if childNode.attr.Mode&unix.S_IFMT == unix.S_IFREG &&
				childNode.attr.Size > 0 &&
				childNode.attr.Size <= maxSmallFileSize {

				// Check if file has SHA256 and is already cached
				var zeroHash [32]byte
				if childNode.attr.Sha256 != zeroHash {
					sha256Hex := fmt.Sprintf("%x", childNode.attr.Sha256[:])
					cachedPath, err := n.client.cache.Lookup(sha256Hex)
					if err == nil && cachedPath != "" {
						// Already cached, skip prefetching
						continue
					}
				}

				// Submit prefetch job (non-blocking)
				job := &prefetchJob{
					client: n.client,
					fh:     childNode.fh,
					attr:   childNode.attr,
				}

				select {
				case n.client.prefetchQueue <- job:
					count++
				default:
					// Queue full, stop prefetching
					return
				}
			}
		}
	}()
}

// Open opens a file for reading
func (n *SnapshotNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Check if this is a directory
	if n.attr.Mode&unix.S_IFMT == unix.S_IFDIR {
		return nil, 0, syscall.EISDIR
	}
	if (flags & (unix.O_WRONLY | unix.O_RDWR)) != 0 {
		return nil, 0, syscall.EROFS
	}

	// Check if file is in cache
	var zeroHash [32]byte
	sha256Hex := fmt.Sprintf("%x", n.attr.Sha256[:])

	if n.attr.Sha256 != zeroHash {
		cachedPath, err := n.client.cache.Lookup(sha256Hex)
		if err == nil && cachedPath != "" {
			// Open cached file using passthrough mode
			fd, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
			if err == nil {
				GlobalCacheMetrics.CacheHit.Inc()
				// Use LoopbackFile for passthrough mode
				loopbackFile := fs.NewLoopbackFile(fd).(*fs.LoopbackFile)
				return &SnapshotFile{
					cachedBackingFile: loopbackFile,
					client:            n.client,
					fh:                n.fh,
					attr:              n.attr,
					cache:             n.client.cache,
				}, fuse.FOPEN_KEEP_CACHE, fs.OK
			}
		}
	}

	// Create file handle for remote access
	file := &SnapshotFile{
		client: n.client,
		fh:     n.fh,
		attr:   n.attr,
		cache:  n.client.cache,
	}

	return file, fuse.FOPEN_KEEP_CACHE, fs.OK
}

// Setattr is not supported (read-only snapshot filesystem)
func (n *SnapshotNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	return syscall.EROFS
}

// Create is not supported (read-only snapshot filesystem)
func (n *SnapshotNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	return nil, nil, 0, syscall.EROFS
}

// Mkdir is not supported (read-only snapshot filesystem)
func (n *SnapshotNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return nil, syscall.EROFS
}

// Unlink is not supported (read-only snapshot filesystem)
func (n *SnapshotNode) Unlink(ctx context.Context, name string) syscall.Errno {
	return syscall.EROFS
}

// Rmdir is not supported (read-only snapshot filesystem)
func (n *SnapshotNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	return syscall.EROFS
}

// Rename is not supported (read-only snapshot filesystem)
func (n *SnapshotNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	return syscall.EROFS
}

// Readlink reads the target of a symlink
func (n *SnapshotNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	// Send readlink request
	readlinkReq := fileserver.ReadlinkRequest{
		Fh: n.fh,
	}

	readlinkResp := fileserver.ReadlinkResponse{}
	err := n.client.SendRequest(&readlinkReq, &readlinkResp)
	if err != nil {
		return nil, fs.ToErrno(err)
	}

	return []byte(readlinkResp.Target), fs.OK
}

// SnapshotFile implements fs.FileHandle for reading files
type SnapshotFile struct {
	cachedBackingFile *fs.LoopbackFile // Embedded for passthrough mode when cached
	client            *DirectFSClient
	fh                unix.FileHandle
	attr              fileserver.FileAttr
	cache             *DirectoryCacheManager
}

// Ensure SnapshotFile implements required interfaces
var _ fs.FileHandle = (*SnapshotFile)(nil)
var _ fs.FileReader = (*SnapshotFile)(nil)
var _ fs.FileWriter = (*SnapshotFile)(nil)
var _ fs.FileFlusher = (*SnapshotFile)(nil)
var _ fs.FileSetattrer = (*SnapshotFile)(nil)
var _ fs.FileReleaser = (*SnapshotFile)(nil)

// Read reads data from the file with caching
func (f *SnapshotFile) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// If file is from cache, use passthrough mode
	if f.cachedBackingFile != nil {
		return f.cachedBackingFile.Read(ctx, dest, off)
	}

	// Check cache first if SHA256 is available
	var zeroHash [32]byte
	sha256Hex := fmt.Sprintf("%x", f.attr.Sha256[:])

	if f.attr.Sha256 != zeroHash {
		// Try to get from cache
		cachedPath, err := f.cache.Lookup(sha256Hex)
		if err == nil && cachedPath != "" {
			// Read from cache
			cachedFile, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
			if err == nil {
				defer unix.Close(cachedFile)

				buf := make([]byte, len(dest))
				n, err := unix.Pread(cachedFile, buf, off)
				if err == nil || err == io.EOF {
					GlobalCacheMetrics.CacheHit.Inc()
					return fuse.ReadResultData(buf[:n]), fs.OK
				}
			}
		}
		GlobalCacheMetrics.CacheMiss.Inc()
	}

	// Send read request to server
	readReq := fileserver.ReadRequest{
		Fh:     f.fh,
		Offset: uint64(off),
		Size:   uint32(len(dest)),
	}

	readResp := fileserver.ReadResponse{}
	err := f.client.SendRequest(&readReq, &readResp)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	// Cache the full file if this is a complete read and SHA256 is available
	if f.attr.Sha256 != zeroHash && off == 0 && int64(len(readResp.Data)) == f.attr.Size {
		log.Printf("Caching with SHA256 %s\n", sha256Hex)
		// Write to cache asynchronously
		go func() {
			writer, err := f.cache.CreateTemp()
			if err != nil {
				return
			}

			if _, err := writer.Write(readResp.Data); err != nil {
				writer.Abort()
				return
			}

			computedHash, err := writer.Commit()
			if err != nil {
				writer.Abort()
				return
			}

			// Verify hash matches
			if computedHash != sha256Hex {
				fmt.Printf("Warning: computed hash %s doesn't match expected %s\n", computedHash, sha256Hex)
			} else {
				GlobalCacheMetrics.CacheSaved.Inc()
			}
		}()
	}

	return fuse.ReadResultData(readResp.Data), fs.OK
}

// Write is not supported (read-only snapshot filesystem)
func (f *SnapshotFile) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	return 0, syscall.EROFS
}

// Flush is a no-op for read-only filesystem
func (f *SnapshotFile) Flush(ctx context.Context) syscall.Errno {
	return fs.OK
}

// Setattr is not supported (read-only snapshot filesystem)
func (f *SnapshotFile) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	return syscall.EROFS
}

// Release closes the file descriptor if it's a cached file
func (f *SnapshotFile) Release(ctx context.Context) syscall.Errno {
	if f.cachedBackingFile != nil {
		return f.cachedBackingFile.Release(ctx)
	}
	return fs.OK
}

var _ = (fs.FilePassthroughFder)((*SnapshotFile)(nil))

func (f *SnapshotFile) PassthroughFd() (int, bool) {
	if f.cachedBackingFile != nil {
		return f.cachedBackingFile.PassthroughFd()
	}
	return -1, false
}

func toFuseAttr(attr *fileserver.FileAttr, out *fuse.Attr) {
	// Convert FileAttr to fuse.Attr
	out.Ino = attr.Ino
	out.Size = uint64(attr.Size)
	out.Blocks = uint64(attr.Blocks)
	out.Mtime = uint64(attr.Mtim)
	out.Mtimensec = uint32(attr.MtimNsec)
	out.Ctime = uint64(attr.Ctim)
	out.Ctimensec = uint32(attr.CtimNsec)
	out.Mode = attr.Mode
	out.Nlink = uint32(attr.Nlink)
	out.Owner = fuse.Owner{
		Uid: attr.Uid,
		Gid: attr.Gid,
	}
	out.Rdev = uint32(attr.Rdev)
	out.Blksize = uint32(attr.Blksize)

}
