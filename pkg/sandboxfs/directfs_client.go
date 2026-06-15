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

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/fileserver"
)

const (
	// Number of background workers for prefetching
	prefetchWorkers = 20
	// Shared worker count for RW eager fetch of small/medium files
	rwEagerFetchWorkers = 8
	// Shared worker count for RW eager fetch of large files
	rwEagerFetchLargeWorkers = 2
	// Shared write workers for protocol write queue
	writeQueueWorkers = 1
	// Maximum total bytes buffered in global write queue
	globalWriteQueueMaxBytes = 256 * 1024 * 1024 // 256MB
	// Files larger than this (bytes) are considered "large" for eagerfetch concurrency limits
	largeFileThreshold = 100 * 1024 * 1024 // 100 MB
)

type Response struct {
	errno syscall.Errno
	data  []byte
}

// partialMessage holds accumulated data for a multi-packet message
type partialMessage struct {
	chunks [][]byte
}

type dirDataNotificationHandler interface {
	HandleDirDataNotification(notification *fileserver.DirDataNotification)
}

// sendRequestOp represents a request to be sent by the send-request-worker
type sendRequestOp struct {
	opCode     uint32                  // Operation code (OpWrite, OpRead, etc.)
	flags      uint32                  // Protocol flags (FlagQosLow, etc.)
	req        fileserver.Serializable // Request object
	responseOp responseOp
}

type responseOp struct {
	resCh    chan Response
	callback func(Response)
}

func (r responseOp) complete(resp Response) {
	if r.resCh != nil {
		r.resCh <- resp
	}
	if r.callback != nil {
		r.callback(resp)
	}
}

// DirectFSClient manages a FUSE filesystem that connects to a remote fileserver
// using the fileserver protocol and uses sandboxfs cache manager
type DirectFSClient struct {
	serverAddr string
	conn       net.Conn

	// Connection state
	mu             sync.Mutex
	seq            uint32
	pending        map[uint32]responseOp
	partialPackets map[uint32]*partialMessage // Buffer for assembling partial packets
	rootFh         unix.FileHandle
	rootAttr       fileserver.FileAttr

	// Reconnection tracking
	reconnectMu sync.Mutex

	// Inode tracking for pre-loaded metadata
	inodeMu sync.RWMutex
	inodes  map[uint64]dirDataNotificationHandler // Map from inode number to notification handler

	// Cache from sandboxfs
	cache *DirectoryCacheManager
	debug *DebugTracker

	// Additional cache sources (HTTP URLs, NFS paths, etc.)
	cacheSources       []string
	cacheSourceClients []CacheSource

	// Prefetch queue and workers
	prefetchQueue chan *prefetchJob
	workersWg     sync.WaitGroup
	// Semaphore to limit concurrent large-file prefetches (per client)
	largeFetchSem chan struct{}

	// Shared eager-fetch worker queues for RW reads
	rwEagerFetchQueue      chan *rwEagerFetchTask
	rwEagerFetchLargeQueue chan *rwEagerFetchTask

	// Shared request send workers
	normalReqQueue chan *sendRequestOp // Higher priority for non-write operations
	writeReqQueue  chan *sendRequestOp // Lower priority for write operations (QOS)
	sendReqWg      sync.WaitGroup

	// Write backpressure and request tracking
	writeMu          sync.Mutex
	writeBufCond     *sync.Cond
	queuedWriteBytes int64
	writeReqID       uint64

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
	path   string
}

type rwEagerFetchTask struct {
	file *RWFile
}

// NewDirectFSClient creates a new direct filesystem client
func NewDirectFSClient(serverAddr string, cache *DirectoryCacheManager, cacheSources []string, verbose bool) *DirectFSClient {
	ctx, cancel := context.WithCancel(context.Background())
	client := &DirectFSClient{
		serverAddr:             serverAddr,
		pending:                make(map[uint32]responseOp),
		partialPackets:         make(map[uint32]*partialMessage),
		inodes:                 make(map[uint64]dirDataNotificationHandler),
		cache:                  cache,
		debug:                  NewDebugTracker(),
		cacheSources:           cacheSources,
		cacheSourceClients:     initializeCacheSources(cacheSources),
		prefetchQueue:          make(chan *prefetchJob, 1000),
		largeFetchSem:          make(chan struct{}, 1),
		rwEagerFetchQueue:      make(chan *rwEagerFetchTask, 256),
		rwEagerFetchLargeQueue: make(chan *rwEagerFetchTask, 64),
		normalReqQueue:         make(chan *sendRequestOp, 256),  // Higher priority
		writeReqQueue:          make(chan *sendRequestOp, 1024), // Lower priority (QOS)
		ctx:                    ctx,
		cancel:                 cancel,
		verbose:                verbose,
	}
	client.writeBufCond = sync.NewCond(&client.writeMu)

	// Start prefetch workers
	for i := 0; i < prefetchWorkers; i++ {
		client.workersWg.Add(1)
		go client.prefetchWorker()
	}

	for i := 0; i < rwEagerFetchWorkers; i++ {
		client.workersWg.Add(1)
		go client.rwEagerFetchWorker(client.rwEagerFetchQueue)
	}
	for i := 0; i < rwEagerFetchLargeWorkers; i++ {
		client.workersWg.Add(1)
		go client.rwEagerFetchWorker(client.rwEagerFetchLargeQueue)
	}

	// Start send-request workers (1 worker for both queues with priority)
	client.sendReqWg.Add(1)
	go client.sendRequestWorker()

	return client
}

// Connect connects to the file server and performs mount
func (sc *DirectFSClient) Connect() (unix.FileHandle, fileserver.FileAttr, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	mountResp, err := sc.connect()
	if err != nil {
		return unix.FileHandle{}, fileserver.FileAttr{}, fmt.Errorf("failed to connect: %w", err)
	}
	// Store root file handle and attributes from mount response
	sc.rootFh = mountResp.Fh
	sc.rootAttr = mountResp.Attr
	return sc.rootFh, sc.rootAttr, nil
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
	//close(sc.prefetchQueue)
	close(sc.rwEagerFetchQueue)
	close(sc.rwEagerFetchLargeQueue)
	close(sc.normalReqQueue)
	close(sc.writeReqQueue)
	func() {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if sc.conn != nil {
			sc.conn.Close()
			sc.conn = nil
		}
	}()
	sc.workersWg.Wait()
	sc.sendReqWg.Wait()
	sc.wg.Wait()
}

func (sc *DirectFSClient) registerInodeHandler(ino uint64, handler dirDataNotificationHandler) {
	sc.inodeMu.Lock()
	sc.inodes[ino] = handler
	sc.inodeMu.Unlock()
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
	sc.pending = make(map[uint32]responseOp)
	sc.partialPackets = make(map[uint32]*partialMessage) // Clear partial packets on reconnect
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
	sc.pending[seq] = responseOp{
		resCh: respChan,
	}

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

func (sc *DirectFSClient) rwEagerFetchWorker(queue <-chan *rwEagerFetchTask) {
	defer sc.workersWg.Done()
	for {
		select {
		case <-sc.ctx.Done():
			return
		case task, ok := <-queue:
			if !ok {
				return
			}
			if task == nil || task.file == nil {
				continue
			}
			task.file.performEagerFetch()
		}
	}
}

// sendRequestWorker processes both normal and write requests with priority scheduling.
// It gives higher priority to normal operations and lower priority to write operations (QOS).
func (sc *DirectFSClient) sendRequestWorker() {
	defer sc.sendReqWg.Done()

	for {
		select {
		case <-sc.ctx.Done():
			return
		case op, ok := <-sc.normalReqQueue:
			if ok {
				sc.executeRequestOp(op)
			}
		default:
			// No normal request available, check write queue with lower priority
			select {
			case <-sc.ctx.Done():
				return
			case op, ok := <-sc.writeReqQueue:
				if ok {
					sc.executeRequestOp(op)
				}
			case op, ok := <-sc.normalReqQueue:
				if ok {
					sc.executeRequestOp(op)
				}
			}
		}
	}
}

// executeRequestOp executes a single request operation
func (sc *DirectFSClient) executeRequestOp(op *sendRequestOp) {
	resp := Response{errno: 0, data: nil}
	seq := sc.nextSeq()
	data, err := fileserver.SerializeWithHeader(op.opCode, seq, op.flags, op.req)
	if err != nil {
		resp.errno = syscall.EIO
		op.responseOp.complete(resp)
		return
	}

	// Create response channel and register it
	sc.mu.Lock()
	conn := sc.conn
	for conn == nil {
		if err := sc.reconnect(); err != nil {
			sc.mu.Unlock()
			resp.errno = syscall.ECONNREFUSED
			op.responseOp.complete(resp)
			return
		}
		conn = sc.conn
	}
	sc.pending[seq] = op.responseOp
	sc.mu.Unlock()

	// Send request
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err = conn.Write(data)
	if err != nil {
		sc.mu.Lock()
		if sc.conn == conn {
			sc.conn.Close()
			sc.conn = nil
		}
		delete(sc.pending, seq)
		sc.mu.Unlock()
		resp.errno = syscall.EIO
		op.responseOp.complete(resp)
		return
	}
}

// enqueueGlobalWriteOp enqueues a write operation with backpressure handling.
// The operation is queued as a low-priority request and returns a request ID.
// The callback will be invoked asynchronously when the operation completes.
func (sc *DirectFSClient) enqueueAsyncOp(size int64, req fileserver.Serializable, res fileserver.Serializable, callback func(error)) uint64 {
	if size < 0 {
		size = 0
	}

	id := atomic.AddUint64(&sc.writeReqID, 1)

	sc.writeMu.Lock()
	for sc.queuedWriteBytes+size > globalWriteQueueMaxBytes {
		sc.writeBufCond.Wait()
		select {
		case <-sc.ctx.Done():
			sc.writeMu.Unlock()
			if callback != nil {
				callback(fmt.Errorf("client stopped"))
			}
			return id
		default:
		}
	}
	sc.queuedWriteBytes += size
	sc.writeMu.Unlock()
	newCallback := func(resp Response) {
		// Update bytes and broadcast after completion
		sc.writeMu.Lock()
		sc.queuedWriteBytes -= size
		sc.writeBufCond.Broadcast()
		sc.writeMu.Unlock()
		var err error
		err = resp.errno
		if err != nil {
			reader := bytes.NewReader(resp.data)
			err = res.Deserialize(reader)
		}
		if callback != nil {
			callback(err)
		}
	}
	opCode, err := reqToOpCode(req)
	if err != nil {
		if callback != nil {
			callback(fmt.Errorf("unknown request type: %w", err))
		}
		return id
	}
	sc.writeReqQueue <- &sendRequestOp{
		opCode: opCode,
		flags:  fileserver.FlagQosLow,
		req:    req,
		responseOp: responseOp{
			callback: newCallback,
		},
	}
	return id
}

func (sc *DirectFSClient) submitRWEagerFetch(file *RWFile) {
	if file == nil {
		return
	}
	task := &rwEagerFetchTask{file: file}
	if file.attr.Size > int64(largeFileThreshold) {
		select {
		case sc.rwEagerFetchLargeQueue <- task:
		default:
		}
		return
	}
	select {
	case sc.rwEagerFetchQueue <- task:
	default:
	}
}

// processPrefetchJob processes a single prefetch job
func (sc *DirectFSClient) processPrefetchJob(job *prefetchJob) {
	opID := sc.debug.StartOperation("directfs-prefetch", job.path, fmt.Sprintf("inode=%d size=%d", job.attr.Ino, job.attr.Size))
	defer sc.debug.FinishOperation(opID)

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
	// Create response channel
	respChan := make(chan Response, 1)
	sc.normalReqQueue <- &sendRequestOp{
		opCode: opCode,
		flags:  flags,
		req:    req,
		responseOp: responseOp{
			resCh:    respChan,
			callback: nil,
		},
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

func reqToOpCode(req fileserver.Serializable) (uint32, error) {
	var opCode uint32
	switch req.(type) {
	case *fileserver.MountRequest:
		opCode = fileserver.OpMount
	case *fileserver.LookupRequest:
		opCode = fileserver.OpLookup
	case *fileserver.ReadRequest:
		opCode = fileserver.OpRead
	case *fileserver.ReadFdRequest:
		opCode = fileserver.OpReadFd
	case *fileserver.ReadDirRequest:
		opCode = fileserver.OpReadDir
	case *fileserver.ReadlinkRequest:
		opCode = fileserver.OpReadlink
	case *fileserver.OpenRequest:
		opCode = fileserver.OpOpen
	case *fileserver.CreateRequest:
		opCode = fileserver.OpCreate
	case *fileserver.WriteRequest:
		opCode = fileserver.OpWrite
	case *fileserver.WriteFdRequest:
		opCode = fileserver.OpWriteFd
	case *fileserver.MkdirRequest:
		opCode = fileserver.OpMkdir
	case *fileserver.UnlinkRequest:
		opCode = fileserver.OpUnlink
	case *fileserver.RmdirRequest:
		opCode = fileserver.OpRmdir
	case *fileserver.RenameRequest:
		opCode = fileserver.OpRename
	case *fileserver.SetattrRequest:
		opCode = fileserver.OpSetattr
	case *fileserver.SymlinkRequest:
		opCode = fileserver.OpSymlink
	case *fileserver.LinkRequest:
		opCode = fileserver.OpLink
	case *fileserver.FlushRequest:
		opCode = fileserver.OpFlush
	case *fileserver.FlushFdRequest:
		opCode = fileserver.OpFlushFd
	case *fileserver.GetattrRequest:
		opCode = fileserver.OpGetattr
	case *fileserver.LockRequest:
		opCode = fileserver.OpSetlk
	case *fileserver.ReleaseFdRequest:
		opCode = fileserver.OpReleaseFd
	default:
		return 0, fmt.Errorf("unknown request type")
	}
	return opCode, nil
}

// SendRequestWithFlags sends a request with specified flags and waits for response
func (sc *DirectFSClient) SendRequestWithFlags(req fileserver.Serializable, res fileserver.Serializable, flags uint32) error {
	// Don't retry mount requests - they're part of reconnection
	_, isMountReq := req.(*fileserver.MountRequest)

	opCode, err := reqToOpCode(req)
	if err != nil {
		return err
	}
	maxAttempts := 1
	if !isMountReq {
		maxAttempts = 2 // One attempt + one retry after reconnect
	}

	var lastErr error
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
func (sc *DirectFSClient) readResponses(conn net.Conn, pendingList map[uint32]responseOp) {
	defer sc.wg.Done()
	defer func() {
		// Mark connection as down when reader exits
		log.Printf("Response reader exited, connection marked as down")
		sc.mu.Lock()
		for _, op := range pendingList {
			op.complete(Response{errno: syscall.ECONNRESET, data: nil})
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

		// Handle partial packets - only lock if it's part of a multi-packet message
		isMultiPacket := (header.Flags&fileserver.FlagHasMore != 0) || (header.Flags&fileserver.FlagEndOfMultiPacket != 0)

		if isMultiPacket {
			sc.mu.Lock()
			if header.Flags&fileserver.FlagHasMore != 0 {
				// This is a partial packet - accumulate it
				partial, exists := sc.partialPackets[header.Seq]
				if !exists {
					partial = &partialMessage{chunks: make([][]byte, 0)}
					sc.partialPackets[header.Seq] = partial
				}
				partial.chunks = append(partial.chunks, data)
				sc.mu.Unlock()
				continue // Wait for more chunks
			} else if header.Flags&fileserver.FlagEndOfMultiPacket != 0 {
				// This is the final chunk of a partial message
				partial, hasPartial := sc.partialPackets[header.Seq]
				if hasPartial {
					// Assemble all chunks
					partial.chunks = append(partial.chunks, data)
					totalSize := 0
					for _, chunk := range partial.chunks {
						totalSize += len(chunk)
					}
					assembled := make([]byte, 0, totalSize)
					for _, chunk := range partial.chunks {
						assembled = append(assembled, chunk...)
					}
					data = assembled
					delete(sc.partialPackets, header.Seq)
				} else {
					log.Printf("Warning: received END_OF_MULTIPACKET for seq %d but no partial message found", header.Seq)
				}
			}
			sc.mu.Unlock()
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
				log.Printf("header flags: %d, seq: %d, opcode: %d, compressed size: %d", header.Flags, header.Seq, header.Opcode, len(data))
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
			respChan.complete(Response{errno: syscall.Errno(header.Opcode), data: data})
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
	handler, exists := sc.inodes[notification.Ino]
	sc.inodeMu.RUnlock()

	if !exists {
		// Inode not in cache yet, skip pre-loading
		log.Printf("DirDataNotification for unknown inode %d, skipping", notification.Ino)
		return
	}

	handler.HandleDirDataNotification(&notification)
}

func (sc *DirectFSClient) DebugLog(format string, args ...interface{}) {
	if sc.verbose {
		log.Printf(format, args...)
	}
}
