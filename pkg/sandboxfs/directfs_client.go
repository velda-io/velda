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
	"errors"
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
	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/fileserver"
)

const (
	// Number of background workers for prefetching
	prefetchWorkers = 20
	// Files larger than this (bytes) are considered "large" for eagerfetch concurrency limits
	largeFileThreshold = 100 * 1024 * 1024 // 100 MB
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

	// Reconnection tracking
	reconnectMu sync.Mutex

	// Inode tracking for pre-loaded metadata
	inodeMu sync.RWMutex
	inodes  map[uint64]*fs.Inode // Map from inode number to FUSE inode

	// Cache from sandboxfs
	cache *DirectoryCacheManager

	// Additional cache sources (HTTP URLs, NFS paths, etc.)
	cacheSources       []string
	cacheSourceClients []CacheSource

	// Prefetch queue and workers
	prefetchQueue chan *prefetchJob
	workersWg     sync.WaitGroup
	// Semaphore to limit concurrent large-file prefetches (per client)
	largeFetchSem chan struct{}

	// FUSE state
	fuseServer *fuse.Server
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	verbose bool
}

// prefetchJob represents a background job to prefetch and cache a small file
type prefetchJob struct {
	client *DirectFSClient
	fh     unix.FileHandle
	attr   fileserver.FileAttr
}

// NewDirectFSClient creates a new direct filesystem client
func NewDirectFSClient(serverAddr string, cache *DirectoryCacheManager, cacheSources []string, verbose bool) *DirectFSClient {
	ctx, cancel := context.WithCancel(context.Background())
	client := &DirectFSClient{
		serverAddr:         serverAddr,
		pending:            make(map[uint32]chan Response),
		inodes:             make(map[uint64]*fs.Inode),
		cache:              cache,
		cacheSources:       cacheSources,
		cacheSourceClients: initializeCacheSources(cacheSources),
		prefetchQueue:      make(chan *prefetchJob, 1000),
		largeFetchSem:      make(chan struct{}, 1),
		ctx:                ctx,
		cancel:             cancel,
		verbose:            verbose,
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
	sc.mu.Lock()
	defer sc.mu.Unlock()
	mountResp, err := sc.connect()
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
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
			Options:     []string{"default_permissions"},
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
	func() {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if sc.conn != nil {
			sc.conn.Close()
			sc.conn = nil
		}
	}()
	sc.workersWg.Wait()
	sc.wg.Wait()
}

func (sc *DirectFSClient) connect() (*fileserver.MountResponse, error) {
	// Parse server address
	parts := strings.SplitN(sc.serverAddr, "@", 2)
	host := parts[0]
	var path string
	if len(parts) > 1 {
		path = parts[1]
	}

	// Retry connection with exponential backoff
	var conn net.Conn
	var err error

	attempt := 0
	for ; attempt < 5; attempt++ {
		if attempt > 0 {
			delay := 2 * time.Second * time.Duration(1<<uint(attempt-1))
			if delay > 60*time.Second {
				delay = 60 * time.Second
			}
			log.Printf("Reconnection attempt %d after %v", attempt, delay)
			time.Sleep(delay)
		}

		conn, err = net.Dial("tcp", host)
		if err == nil {
			break
		}
		log.Printf("Reconnection attempt %d failed: %v", attempt, err)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to reconnect after %d attempts: %w", attempt, err)
	}

	sc.conn = conn

	// Start new response reader
	sc.wg.Add(1)
	sc.pending = make(map[uint32]chan Response)
	go sc.readResponses(conn, sc.pending)

	// Re-send mount request
	mountReq := fileserver.MountRequest{
		Version: fileserver.ProtocolVersion,
		Flags:   fileserver.MountFlagReadOnly,
		Path:    path,
	}

	var mountResp fileserver.MountResponse
	seq := sc.nextSeq()
	data, err := fileserver.SerializeWithHeader(fileserver.OpMount, seq, 0, &mountReq)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize request: %w", err)
	}
	// Create response channel
	respChan := make(chan Response, 1)
	sc.pending[seq] = respChan

	// Send request with connection failure detection
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err = conn.Write(data)

	if err != nil {
		sc.conn.Close()
		return nil, fmt.Errorf("re-mount failed: %w", err)
	}
	sc.mu.Unlock()
	resq := <-respChan
	delete(sc.pending, seq)
	sc.mu.Lock()
	if resq.errno != 0 {
		sc.conn.Close()
		sc.conn = nil
		return nil, fmt.Errorf("mount failed with errno: %w", syscall.Errno(resq.errno))
	}
	if err = mountResp.Deserialize(bytes.NewReader(resq.data)); err != nil {
		sc.conn.Close()
		sc.conn = nil
		return nil, fmt.Errorf("failed to deserialize mount response: %w", err)
	}

	return &mountResp, nil
}

// reconnect attempts to re-establish the connection and re-mount
func (sc *DirectFSClient) reconnect() error {
	if sc.conn != nil {
		return nil
	}

	log.Printf("Attempting to reconnect to %s", sc.serverAddr)

	// Close old connection if exists
	sc.conn.Close()

	mountResp, err := sc.connect()
	if err != nil {
		return fmt.Errorf("failed to reconnect: %w", err)
	}

	// Verify root handle matches
	if sc.rootFh != mountResp.Fh {
		log.Printf("Warning: Root file handle changed after reconnect (old: %v, new: %v)", sc.rootFh, mountResp.Fh)
	}

	log.Printf("Successfully reconnected to %s", sc.serverAddr)
	return nil
}

// prefetchWorker is a background worker that processes prefetch jobs
func (sc *DirectFSClient) prefetchWorker() {
	defer sc.workersWg.Done()

	for {
		select {
		case <-sc.ctx.Done():
			return
		case job := <-sc.prefetchQueue:
			if job == nil {
				return
			}
			sc.processPrefetchJob(job)
		}
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

	// Read entire file from server with low priority QoS
	readReq := fileserver.ReadRequest{
		Fh:     job.fh,
		Offset: 0,
		Size:   uint32(job.attr.Size),
	}

	readResp := fileserver.ReadResponse{}
	err = sc.SendRequestWithFlags(&readReq, &readResp, fileserver.FlagQosLow)
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
func (sc *DirectFSClient) sendRequestWithData(opCode uint32, flags uint32, req fileserver.Serializable, res fileserver.Serializable) error {
	seq := sc.nextSeq()
	data, err := fileserver.SerializeWithHeader(opCode, seq, flags, req)
	if err != nil {
		return fmt.Errorf("failed to serialize request: %w", err)
	}
	// Create response channel
	respChan := make(chan Response, 1)
	sc.mu.Lock()
	conn := sc.conn
	for conn == nil {
		if err := sc.reconnect(); err != nil {
			sc.mu.Unlock()
			return fmt.Errorf("failed to reconnect: %w", err)
		}
		conn = sc.conn
	}
	sc.pending[seq] = respChan
	sc.mu.Unlock()

	// Send request with connection failure detection
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err = conn.Write(data)
	if err != nil {
		// Mark the connection as down and trigger reconnection at next request
		sc.mu.Lock()
		if sc.conn == conn {
			sc.conn.Close()
			sc.conn = nil
		}
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
	case <-sc.ctx.Done():
		return fmt.Errorf("client stopped")
	}
}

// SendRequest sends a request and waits for response
func (sc *DirectFSClient) SendRequest(req fileserver.Serializable, res fileserver.Serializable) error {
	return sc.SendRequestWithFlags(req, res, fileserver.FlagNone)
}

// SendRequestWithFlags sends a request with specified flags and waits for response
func (sc *DirectFSClient) SendRequestWithFlags(req fileserver.Serializable, res fileserver.Serializable, flags uint32) error {
	// Don't retry mount requests - they're part of reconnection
	_, isMountReq := req.(*fileserver.MountRequest)

	maxAttempts := 1
	if !isMountReq {
		maxAttempts = 2 // One attempt + one retry after reconnect
	}

	var lastErr error
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

	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := sc.sendRequestWithData(opCode, flags, req, res)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if we should retry
		if !isMountReq && isRetriableError(err) {
			continue
		}

		// Non-retriable error or mount request, return immediately
		return err
	}

	return lastErr
}

// isRetriableError checks if an error warrants a retry with reconnection
func isRetriableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		strings.Contains(errStr, "connection lost") ||
		strings.Contains(errStr, "connection reset")
}

// readResponses reads responses from the connection
func (sc *DirectFSClient) readResponses(conn net.Conn, pendingList map[uint32]chan Response) {
	defer sc.wg.Done()
	defer func() {
		// Mark connection as down when reader exits
		log.Printf("Response reader exited, connection marked as down")
		sc.mu.Lock()
		for _, ch := range pendingList {
			ch <- Response{errno: syscall.ECONNRESET, data: nil}
		}
		sc.mu.Unlock()
	}()

	buf := make([]byte, 4096)
	for {
		select {
		case <-sc.ctx.Done():
			return
		default:
		}

		// Read header
		_, err := io.ReadFull(conn, buf[:fileserver.HeaderSize])
		if err != nil {
			if err != io.EOF && !errors.Is(err, net.ErrClosed) {
				log.Printf("Read header error: %v", err)
			}
			return
		}

		// Parse header
		var header fileserver.Header
		if err := header.Deserialize(bytes.NewReader(buf[:fileserver.HeaderSize])); err != nil {
			log.Printf("Invalid header: %v", err)
			return
		}

		// Validate size
		if header.Size < fileserver.HeaderSize || header.Size > 10*1024*1024 {
			log.Printf("Invalid message size: %d", header.Size)
			return
		}

		// Read full message
		data := make([]byte, header.Size-fileserver.HeaderSize)

		if header.Size > fileserver.HeaderSize {
			_, err = io.ReadFull(conn, data)
			if err != nil {
				log.Printf("Read data error: %v", err)
				return
			}
		}

		// Decompress data if compressed
		if header.Flags&fileserver.FlagCompressed != 0 {
			decoder, err := zstd.NewReader(nil)
			if err != nil {
				log.Printf("Failed to create zstd decoder: %v", err)
				return
			}
			decompressed, err := decoder.DecodeAll(data, nil)
			decoder.Close()
			if err != nil {
				log.Printf("Failed to decompress data: %v", err)
				return
			}
			data = decompressed
		}

		// Check if this is a DirDataNotification (opcode indicates notification)
		if header.Seq == 0 && header.Opcode == fileserver.OpDirDataNotification {
			// Handle DirDataNotification asynchronously
			go sc.handleDirDataNotification(data)
			continue
		}

		// Dispatch response
		sc.mu.Lock()
		respChan, ok := pendingList[header.Seq]
		delete(pendingList, header.Seq)
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

	parentNode.dirFetched.Do(func() {
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

			// Register inode in the map for future notifications
			sc.inodeMu.Lock()
			sc.inodes[entry.Attr.Ino] = childInode
			sc.inodeMu.Unlock()

			// Add to parent's children (this makes it visible to readdir)
			parentInode.AddChild(entry.Name, childInode, false)
		}

		sc.DebugLog("Pre-loaded %d entries for inode %d", len(notification.Entries), notification.Ino)
	})
}

func (sc *DirectFSClient) DebugLog(format string, args ...interface{}) {
	if sc.verbose {
		log.Printf(format, args...)
	}
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

	// Symlink caching
	symlinkFetched sync.Once // Whether symlink target has been fetched
	symlinkTarget  []byte    // Cached symlink target
	symlinkError   error     // Error from fetching symlink
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
		n.client.DebugLog("Fetching directory entries for inode %d", n.attr.Ino)
		// Increment counter for readdir request from client
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.ReadDirFromClient.Inc()
		}
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

			// Register inode in the map for potential DirDataNotification
			n.client.inodeMu.Lock()
			n.client.inodes[entry.Attr.Ino] = child
			n.client.inodeMu.Unlock()

			// Add to children, don't replace existing child if it already exists
			n.AddChild(entry.Name, child, false)
		}

		n.client.DebugLog("Fetched directory entries for inode %d", n.attr.Ino)
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

	// Register inode in the map for potential DirDataNotification
	n.client.inodeMu.Lock()
	n.client.inodes[lookupResp.Attr.Ino] = child
	n.client.inodeMu.Unlock()

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
	cacheKeyExists := n.attr.Sha256 != zeroHash

	if cacheKeyExists {
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
		GlobalCacheMetrics.CacheMiss.Inc()
	} else {
		GlobalCacheMetrics.CacheNotExist.Inc()
	}

	// Cache miss or no cache key - create file handle and start eager fetch
	file := &SnapshotFile{
		client: n.client,
		fh:     n.fh,
		attr:   n.attr,
		cache:  n.client.cache,
		path:   n.Path(nil),
	}

	// Start eager fetch in background
	go file.eagerFetchAndCache(cacheKeyExists, sha256Hex)

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
	// Fetch symlink target only once and cache it
	n.symlinkFetched.Do(func() {
		// Send readlink request
		readlinkReq := fileserver.ReadlinkRequest{
			Fh: n.fh,
		}

		readlinkResp := fileserver.ReadlinkResponse{}
		err := n.client.SendRequest(&readlinkReq, &readlinkResp)
		if err != nil {
			n.symlinkError = err
			return
		}

		n.symlinkTarget = []byte(readlinkResp.Target)
	})

	if n.symlinkError != nil {
		return nil, fs.ToErrno(n.symlinkError)
	}

	return n.symlinkTarget, fs.OK
}

// SnapshotFile implements fs.FileHandle for reading files
type SnapshotFile struct {
	cachedBackingFile *fs.LoopbackFile // Embedded for passthrough mode when cached
	client            *DirectFSClient
	fh                unix.FileHandle
	attr              fileserver.FileAttr
	cache             *DirectoryCacheManager

	// Eager fetch state
	fetchMu     sync.Mutex
	cacheWriter CacheWriter // Active cache writer for pread access during fetch

	path string // For logging purposes
}

// Ensure SnapshotFile implements required interfaces
var _ fs.FileHandle = (*SnapshotFile)(nil)
var _ fs.FileReader = (*SnapshotFile)(nil)
var _ fs.FileWriter = (*SnapshotFile)(nil)
var _ fs.FileFlusher = (*SnapshotFile)(nil)
var _ fs.FileSetattrer = (*SnapshotFile)(nil)
var _ fs.FileReleaser = (*SnapshotFile)(nil)

// eagerFetchAndCache fetches the entire file from server and caches it using 1MB buffered chunks
// It first tries to fetch from configured cache sources, then falls back to fileserver
func (f *SnapshotFile) eagerFetchAndCache(hasExistingKey bool, expectedSHA string) {
	f.fetchMu.Lock()
	if f.cacheWriter != nil {
		f.fetchMu.Unlock()
		return
	}

	// Create cache writer
	writer, err := f.cache.CreateTemp()
	if err != nil {
		f.fetchMu.Unlock()
		return
	}

	// Store writer for pread access
	f.cacheWriter = writer
	f.fetchMu.Unlock()

	// Try to fetch from cache sources first if we have a hash
	if hasExistingKey && len(f.client.cacheSourceClients) > 0 {
		if f.tryFetchFromCacheSources(expectedSHA, writer) {
			return // Successfully fetched from cache source
		}
	}

	// For large files (> largeFileThreshold), only allow one concurrent fetch per client.
	if f.attr.Size > int64(largeFileThreshold) {
		// Acquire semaphore (blocks until available) and ensure release on function exit
		f.client.largeFetchSem <- struct{}{}
		defer func() { <-f.client.largeFetchSem }()
	}

	abort := func() {
		writer.Abort()
		f.fetchMu.Lock()
		f.cacheWriter = nil
		f.fetchMu.Unlock()
	}

	const chunkSize = 1024 * 1024 // 1MB chunks
	var offset uint64

	for offset < uint64(f.attr.Size) {
		// Calculate read size for this chunk
		remaining := uint64(f.attr.Size) - offset
		readSize := uint32(chunkSize)
		if remaining < uint64(chunkSize) {
			readSize = uint32(remaining)
		}

		// Read chunk from server
		readReq := fileserver.ReadRequest{
			Fh:     f.fh,
			Offset: offset,
			Size:   readSize,
		}

		start := time.Now()
		readResp := fileserver.ReadResponse{}
		err := f.client.SendRequest(&readReq, &readResp)
		if err != nil {
			abort()
			return
		}

		// Write chunk to cache
		if _, err := writer.Write(readResp.Data); err != nil {
			abort()
			return
		}
		elapsed := time.Since(start)
		f.client.DebugLog("Pre-fetched %d bytes for %s at offset %d in %v", len(readResp.Data), f.path, offset, elapsed)

		offset += uint64(len(readResp.Data))

		// Check if we got less data than expected (EOF)
		if len(readResp.Data) < int(readSize) {
			break
		}
	}

	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
	f.cacheWriter = nil // Clear writer reference

	// Commit the cache and get computed SHA256
	computedSHA, err := writer.Commit()
	if err != nil {
		return
	}

	// Verify hash if we had an existing key
	if hasExistingKey && computedSHA != expectedSHA {
		log.Printf("Warning: computed hash %s doesn't match expected %s", computedSHA, expectedSHA)
		// Don't update fetch state on hash mismatch
		return
	}

	// Look up cached file path
	cachedPath, err := f.cache.Lookup(computedSHA)
	if err != nil || cachedPath == "" {
		return
	}

	// Open cached file for future reads
	fd, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
	if err != nil {
		return
	}

	// Update fetch state and set cachedBackingFile
	f.cachedBackingFile = fs.NewLoopbackFile(fd).(*fs.LoopbackFile)

	if GlobalCacheMetrics != nil {
		GlobalCacheMetrics.CacheSaved.Inc()
	}
}

// Read reads data from the file with caching
func (f *SnapshotFile) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Check if eager fetch is in progress or completed
	f.fetchMu.Lock()
	// If file is from cache, use passthrough mode
	cachedFile := f.cachedBackingFile
	writer := f.cacheWriter

	if writer != nil {
		// Try to read from in-progress cache using ReadAt if available
		// This access needs lock to prevent concurrent commit of the writer.
		if readerAt, ok := writer.(io.ReaderAt); ok {
			n, err := readerAt.ReadAt(dest, off)
			if err == nil && n == len(dest) {
				f.fetchMu.Unlock()
				// Successfully read from in-progress cache
				return fuse.ReadResultData(dest[:n]), fs.OK
			}
			// If ReadAt failed (range not yet cached), fall through to network fetch
		}
	}

	f.fetchMu.Unlock()

	if cachedFile != nil {
		return cachedFile.Read(ctx, dest, off)
	}
	// Send read request to server
	readReq := fileserver.ReadRequest{
		Fh:     f.fh,
		Offset: uint64(off),
		Size:   uint32(len(dest)),
	}

	start := time.Now()
	readResp := fileserver.ReadResponse{}
	err := f.client.SendRequest(&readReq, &readResp)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	elapsed := time.Since(start)
	f.client.DebugLog("Read %d bytes for file %s at offset %d in %v", len(readResp.Data), f.path, off, elapsed)

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
	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
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

// tryFetchFromCacheSources attempts to fetch file from configured cache sources
// Returns true if successfully fetched and cached, false otherwise
func (f *SnapshotFile) tryFetchFromCacheSources(hash string, writer CacheWriter) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, source := range f.client.cacheSourceClients {
		f.client.DebugLog("Trying to fetch %d/%s from %s cache source", f.attr.Size, hash, source.Type())

		reader, size, err := source.Fetch(ctx, f.attr.Size, hash)
		if err != nil {
			log.Printf("Failed to fetch from %s cache source: %v", source.Type(), err)
			continue
		}
		defer reader.Close()

		// Copy data from cache source to local cache
		written, err := io.Copy(writer, reader)
		if err != nil {
			log.Printf("Failed to copy from cache source: %v", err)
			writer.Abort()
			continue
		}

		// Verify size if provided
		if size > 0 && written != size {
			log.Printf("Size mismatch: expected %d, got %d", size, written)
			writer.Abort()
			continue
		}

		// Commit to cache
		computedHash, err := writer.Commit()
		if err != nil {
			log.Printf("Failed to commit to cache: %v", err)
			continue
		}

		// Verify hash matches
		if computedHash != hash {
			log.Printf("Hash mismatch: expected %s, got %s", hash, computedHash)
			continue
		}

		// Successfully cached, now open for reading
		cachedPath, err := f.cache.Lookup(computedHash)
		if err != nil || cachedPath == "" {
			log.Printf("Failed to lookup cached file: %v", err)
			continue
		}

		fd, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
		if err != nil {
			log.Printf("Failed to open cached file: %v", err)
			continue
		}

		// Update file state
		f.fetchMu.Lock()
		f.cachedBackingFile = fs.NewLoopbackFile(fd).(*fs.LoopbackFile)
		f.fetchMu.Unlock()

		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.CacheSaved.Inc()
		}

		f.client.DebugLog("Successfully fetched file %d/%s from %s cache source (%d bytes)", f.attr.Size, hash, source.Type(), written)
		return true
	}

	return false
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
