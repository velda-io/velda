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
package fileserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

var XattrCacheKey = "user.veldafs.sha256"

// FileServer manages snapshot file serving with custom protocol
type FileServer struct {
	rootPath string
	listener net.Listener

	// Shared state across all connections
	mu       sync.RWMutex
	sessions map[*Session]bool

	// Request and response queues (separate queues for high and low priority)
	reqQueue      chan Request
	respQueueHigh chan Response
	respQueueLow  chan Response

	// Pre-loading queue for metadata
	preloadQueue chan PreLoadItem

	// Worker management
	numWorkers int
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	locks      *fileLockManager
}

type fileLockKey struct {
	fh    string
	owner uint64
}

type fileLockManager struct {
	mu       sync.Mutex
	sessions map[*Session]map[fileLockKey]int
}

func newFileLockManager() *fileLockManager {
	return &fileLockManager{sessions: make(map[*Session]map[fileLockKey]int)}
}

func fileHandleKey(fh unix.FileHandle) string {
	return string(encodeFileHandle(fh))
}

const (
	_OFD_GETLK  = 36
	_OFD_SETLK  = 37
	_OFD_SETLKW = 38
)

func (m *fileLockManager) getOrCreateFD(session *Session, fh unix.FileHandle, owner uint64, write bool) (int, error) {
	if session == nil || session.rootFd == -1 {
		return -1, syscall.EBADF
	}

	key := fileLockKey{fh: fileHandleKey(fh), owner: owner}

	m.mu.Lock()
	if perSession, ok := m.sessions[session]; ok {
		if fd, ok := perSession[key]; ok {
			m.mu.Unlock()
			return fd, nil
		}
	}
	m.mu.Unlock()

	openFlags := unix.O_NOFOLLOW | unix.O_CLOEXEC
	mode := unix.O_RDONLY
	if write {
		mode = unix.O_RDWR
	}

	fd, err := unix.OpenByHandleAt(session.rootFd, fh, mode|openFlags)
	if err != nil {
		return -1, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	perSession := m.sessions[session]
	if perSession == nil {
		perSession = make(map[fileLockKey]int)
		m.sessions[session] = perSession
	}
	if existing, ok := perSession[key]; ok {
		unix.Close(fd)
		return existing, nil
	}
	perSession[key] = fd
	return fd, nil
}

func (m *fileLockManager) Getlk(fd int, owner uint64, lk fuse.FileLock, _ uint32) (fuse.FileLock, error) {
	flk := syscall.Flock_t{}
	lk.ToFlockT(&flk)
	if err := syscall.FcntlFlock(uintptr(fd), _OFD_GETLK, &flk); err != nil {
		return NewUnlockFileLock(), err
	}
	out := NewUnlockFileLock()
	out.FromFlockT(&flk)
	return out, nil
}

func (m *fileLockManager) Setlk(fd int, owner uint64, lk fuse.FileLock, flags uint32, blocking bool) error {
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
		return syscall.Flock(fd, op)
	}

	flk := syscall.Flock_t{}
	lk.ToFlockT(&flk)
	op := _OFD_SETLK
	if blocking {
		op = _OFD_SETLKW
	}
	return syscall.FcntlFlock(uintptr(fd), op, &flk)
}

func (m *fileLockManager) ReleaseSession(session *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	perSession := m.sessions[session]
	for _, fd := range perSession {
		unix.Close(fd)
	}
	delete(m.sessions, session)
}

// Request wrapper for dispatching
type Request struct {
	Session *Session
	Header  Header
	Data    []byte
}

// Response wrapper for sending
type Response struct {
	Session *Session
	Data    []byte
}

// NewFileServer creates a new file server
func NewFileServer(rootPath string, numWorkers int) *FileServer {
	ctx, cancel := context.WithCancel(context.Background())

	return &FileServer{
		rootPath:      rootPath,
		sessions:      make(map[*Session]bool),
		reqQueue:      make(chan Request, 100),
		respQueueHigh: make(chan Response, 100),
		respQueueLow:  make(chan Response, 100),
		preloadQueue:  make(chan PreLoadItem, 1000),
		numWorkers:    numWorkers,
		ctx:           ctx,
		cancel:        cancel,
		locks:         newFileLockManager(),
	}
}

// Start starts the file server on the given address
func (fs *FileServer) Start(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	fs.listener = listener

	// Start worker pool for request handling
	for i := 0; i < fs.numWorkers; i++ {
		fs.wg.Add(1)
		go fs.requestWorker()
	}

	// Start pre-loading workers (same number as request workers)
	for i := 0; i < fs.numWorkers; i++ {
		fs.wg.Add(1)
		go fs.preloadWorker()
	}

	// Start response sender
	fs.wg.Add(1)
	go fs.responseSender()

	// Start accepting connections
	fs.wg.Add(1)
	go fs.acceptConnections()

	log.Printf("File server started on %s, serving %s with %d workers", fs.listener.Addr(), fs.rootPath, fs.numWorkers)
	return nil
}

// Stop stops the file server
func (fs *FileServer) Stop() {
	fs.cancel()
	if fs.listener != nil {
		fs.listener.Close()
	}
	fs.wg.Wait()
}

func (fs *FileServer) Addr() net.Addr {
	if fs.listener != nil {
		return fs.listener.Addr()
	}
	return nil
}

// acceptConnections accepts new client connections
func (fs *FileServer) acceptConnections() {
	defer fs.wg.Done()

	for {
		conn, err := fs.listener.Accept()
		if err != nil {
			select {
			case <-fs.ctx.Done():
				return
			default:
				fmt.Printf("Accept error: %v\n", err)
				continue
			}
		}

		// Create session for this connection
		session := NewSession(conn)
		fs.mu.Lock()
		fs.sessions[session] = true
		fs.mu.Unlock()

		// Start connection handler
		fs.wg.Add(1)
		go fs.handleConnection(session)
	}
}

// handleConnection handles a single client session
func (fs *FileServer) handleConnection(session *Session) {
	defer fs.wg.Done()
	defer func() {
		fs.mu.Lock()
		delete(fs.sessions, session)
		fs.mu.Unlock()
		session.Close()
	}()

	buf := make([]byte, 4096)
	for {
		select {
		case <-fs.ctx.Done():
			return
		default:
		}

		// Read header
		session.SetReadDeadline(time.Time{})
		if _, err := io.ReadFull(session, buf[:HeaderSize]); err != nil {
			if err != io.EOF {
				fmt.Printf("Server Read header error: %v\n", err)
			}
			return
		}

		header := Header{}
		if err := header.Deserialize(bytes.NewReader(buf[:HeaderSize])); err != nil {
			fmt.Printf("Failed to parse header: %v\n", err)
			return
		}

		// Validate size
		if header.Size < HeaderSize || header.Size > 1024*1024 {
			fmt.Printf("Invalid message size: %d\n", header.Size)
			return
		}

		// Read remaining data
		dataSize := header.Size - HeaderSize
		data := make([]byte, dataSize)
		if dataSize > 0 {
			if _, err := io.ReadFull(session, data); err != nil {
				fmt.Printf("Read data error: %v\n", err)
				return
			}
		}

		// Decompress data if compressed
		if header.Flags&FlagCompressed != 0 {
			decoder, err := zstd.NewReader(nil)
			if err != nil {
				fmt.Printf("Failed to create zstd decoder: %v\n", err)
				return
			}
			decompressed, err := decoder.DecodeAll(data, nil)
			decoder.Close()
			if err != nil {
				fmt.Printf("Failed to decompress data: %v\n", err)
				return
			}
			data = decompressed
		}

		// Dispatch request to handling queue
		select {
		case fs.reqQueue <- Request{Session: session, Header: header, Data: data}:
		case <-fs.ctx.Done():
			return
		}
	}
}

// requestWorker processes requests from the queue
func (fs *FileServer) requestWorker() {
	defer fs.wg.Done()

	for {
		select {
		case <-fs.ctx.Done():
			return
		case req := <-fs.reqQueue:
			fs.handleRequest(req)
		}
	}
}

// responseSender sends responses from the queue
func (fs *FileServer) responseSender() {
	defer fs.wg.Done()

	for {
		select {
		case <-fs.ctx.Done():
			return
		case resp := <-fs.respQueueHigh:
			fs.sendResponse(resp)
		default:
			// If no high-priority responses, check low-priority queue
			select {
			case <-fs.ctx.Done():
				return
			case resp := <-fs.respQueueHigh:
				fs.sendResponse(resp)
			case resp := <-fs.respQueueLow:
				fs.sendResponseChunked(resp)
			}
		}
	}
}

// sendResponse sends a single response packet
func (fs *FileServer) sendResponse(resp Response) {
	resp.Session.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := resp.Session.Write(resp.Data)
	if err != nil {
		fmt.Printf("Write response error: %v\n", err)
	}
}

// sendResponseChunked splits large low-priority responses into chunks
// to allow high-priority packets to be interleaved
func (fs *FileServer) sendResponseChunked(resp Response) {
	data := resp.Data

	// If data fits in one chunk, send it directly
	if len(data) <= ChunkSize+HeaderSize {
		fs.sendResponse(resp)
		return
	}

	// Parse the header from the original packet.
	var header Header
	if err := header.Deserialize(bytes.NewReader(data[:HeaderSize])); err != nil || header.Seq == 0 {
		log.Printf("Failed to parse header for chunking: %v", err)
		fs.sendResponse(resp)
		return
	}

	payload := data[HeaderSize:]
	offset := 0
	isFirstChunk := true

	for offset < len(payload) {
		// Calculate chunk size
		remaining := len(payload) - offset
		chunkPayloadSize := ChunkSize
		if remaining < ChunkSize {
			chunkPayloadSize = remaining
		}

		// Determine if there are more chunks
		isLastChunk := (offset + chunkPayloadSize) >= len(payload)

		// Set flags for multi-packet messages
		flags := header.Flags
		if !isFirstChunk || !isLastChunk {
			// Mark as part of multi-packet sequence
			if !isLastChunk {
				flags |= FlagHasMore
			} else {
				flags |= FlagEndOfMultiPacket
			}
		}

		// Create chunk packet with header
		var out bytes.Buffer
		chunkHeader := Header{
			Opcode: header.Opcode,
			Size:   uint32(HeaderSize + chunkPayloadSize),
			Flags:  flags,
			Seq:    header.Seq,
		}
		if err := chunkHeader.Serialize(&out); err != nil {
			fmt.Printf("Failed to serialize chunk header: %v\n", err)
			return
		}
		if _, err := out.Write(payload[offset : offset+chunkPayloadSize]); err != nil {
			fmt.Printf("Failed to write chunk payload: %v\n", err)
			return
		}

		// Send chunk
		chunkData := out.Bytes()
		resp.Session.SetWriteDeadline(time.Now().Add(30 * time.Second))
		_, err := resp.Session.Write(chunkData)
		if err != nil {
			fmt.Printf("Write chunk error: %v\n", err)
			return
		}

		offset += chunkPayloadSize
		isFirstChunk = false

		// Yield to allow high-priority packets to be sent
		if !isLastChunk {
			// Check if there are high-priority packets waiting
			select {
			case <-fs.ctx.Done():
				return
			case highPrioResp := <-fs.respQueueHigh:
				// High-priority packet available, send it and continue
				fs.sendResponse(highPrioResp)
			default:
				// No high-priority packets, continue with next chunk
			}
		}
	}
}

// preloadWorker processes pre-loading items from the queue
func (fs *FileServer) preloadWorker() {
	defer fs.wg.Done()

	for {
		select {
		case <-fs.ctx.Done():
			return
		case item := <-fs.preloadQueue:
			fs.handlePreload(item)
		}
	}
}

// handlePreload walks the directory tree and sends metadata to client
func (fs *FileServer) handlePreload(item PreLoadItem) {
	// Walk from the inode up to 2 levels deep
	fs.walkAndSendDirectory(item.Fh, item.Ino, item.Session, 0, 2, 1000)
}

// walkAndSendDirectory recursively walks directory and sends DirData
// maxDepth: maximum depth to walk (2 for this implementation)
// maxEntries: maximum entries to send per directory (1000, except last dir can have all)
func (fs *FileServer) walkAndSendDirectory(fh unix.FileHandle, ino uint64, session *Session, currentDepth, maxDepth, maxEntries int) {
	if currentDepth >= maxDepth {
		return
	}

	// Open directory using OpenByHandleAt
	fd, err := unix.OpenByHandleAt(session.rootFd, fh, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		log.Printf("Failed to open directory for preload: %v", err)
		return
	}
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()

	entries, err := file.ReadDir(-1)
	if err != nil {
		log.Printf("Failed to read directory for preload: %v", err)
		return
	}

	// Determine how many entries to process
	entriesToProcess := len(entries)
	// Skip this dir: request client to read it directly to get full entries
	if len(entries) > maxEntries {
		return
	}

	// Build directory entries
	dirEntries := make([]DirEntry, 0, entriesToProcess)
	subdirs := make([]struct {
		fh  unix.FileHandle
		ino uint64
	}, 0)

	for i := 0; i < entriesToProcess; i++ {
		entry := entries[i]

		// Get file handle using NameToHandleAt
		entryFileHandle, entryMountID, err := unix.NameToHandleAt(fd, entry.Name(), 0)
		if err != nil {
			log.Printf("Failed to get file handle for %s during preload: %v", entry.Name(), err)
			continue
		}

		// Verify mount_id matches session
		if int32(entryMountID) != session.mountID {
			continue
		}

		attr, err := fs.makeFileAttrWithPath(fd, entry.Name())
		if err != nil {
			log.Printf("Failed to get file attributes for %s during preload: %v", entry.Name(), err)
			continue
		}

		dirEntries = append(dirEntries, DirEntry{
			Fh:   entryFileHandle,
			Name: entry.Name(),
			Attr: attr,
		})

		// Track subdirectories for recursive processing
		if entry.IsDir() {
			subdirs = append(subdirs, struct {
				fh  unix.FileHandle
				ino uint64
			}{entryFileHandle, attr.Ino})
		}
	}

	// Send DirData notification to client
	if len(dirEntries) > 0 {
		fs.sendDirData(ino, dirEntries, session)
	}

	// Recursively process subdirectories
	for _, subdir := range subdirs {
		fs.walkAndSendDirectory(subdir.fh, subdir.ino, session, currentDepth+1, maxDepth, maxEntries)
	}
}

// sendDirData sends a DirDataNotification to the client
func (fs *FileServer) sendDirData(ino uint64, entries []DirEntry, session *Session) {
	notification := &DirDataNotification{
		Ino:     ino,
		Entries: entries,
	}

	respBytes, err := SerializeWithHeader(OpDirDataNotification, 0, FlagQosLow, notification)
	if err != nil {
		log.Printf("Failed to serialize DirData notification: %v", err)
		return
	}

	// Send via response queue as low priority (preload notifications)
	select {
	case fs.respQueueLow <- Response{Session: session, Data: respBytes}:
	case <-fs.ctx.Done():
	}
}

// handleRequest dispatches request to appropriate handler
func (fs *FileServer) handleRequest(req Request) {
	var resp Serializable
	var err error

	switch req.Header.Opcode {
	case OpMount:
		mountReq := MountRequest{}
		if deserErr := mountReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize mount request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleMount(&mountReq, req.Session)
		}
	case OpLookup:
		lookupReq := LookupRequest{}
		if deserErr := lookupReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize lookup request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleLookup(&lookupReq, req.Session)
		}
	case OpOpen:
		openReq := OpenRequest{}
		if deserErr := openReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize open request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleOpen(&openReq, req.Session)
		}
	case OpRead:
		readReq := ReadRequest{}
		if deserErr := readReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize read request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleRead(&readReq, req.Session)
		}
	case OpReadFd:
		readReq := ReadFdRequest{}
		if deserErr := readReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize readfd request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleReadFd(&readReq, req.Session)
		}
	case OpReadDir:
		readDirReq := ReadDirRequest{}
		if deserErr := readDirReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize readdir request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleReadDir(&readDirReq, req.Session)
		}
	case OpReadlink:
		readlinkReq := ReadlinkRequest{}
		if deserErr := readlinkReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize readlink request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleReadlink(&readlinkReq, req.Session)
		}
	case OpCreate:
		createReq := CreateRequest{}
		if deserErr := createReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize create request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleCreate(&createReq, req.Session)
		}
	case OpWrite:
		writeReq := WriteRequest{}
		if deserErr := writeReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize write request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleWrite(&writeReq, req.Session)
		}
	case OpWriteFd:
		writeReq := WriteFdRequest{}
		if deserErr := writeReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize writefd request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleWriteFd(&writeReq, req.Session)
		}
	case OpMkdir:
		mkdirReq := MkdirRequest{}
		if deserErr := mkdirReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize mkdir request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleMkdir(&mkdirReq, req.Session)
		}
	case OpUnlink:
		unlinkReq := UnlinkRequest{}
		if deserErr := unlinkReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize unlink request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleUnlink(&unlinkReq, req.Session)
		}
	case OpRmdir:
		rmdirReq := RmdirRequest{}
		if deserErr := rmdirReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize rmdir request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleRmdir(&rmdirReq, req.Session)
		}
	case OpRename:
		renameReq := RenameRequest{}
		if deserErr := renameReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize rename request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleRename(&renameReq, req.Session)
		}
	case OpSetattr:
		setattrReq := SetattrRequest{}
		if deserErr := setattrReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize setattr request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleSetattr(&setattrReq, req.Session)
		}
	case OpSymlink:
		symlinkReq := SymlinkRequest{}
		if deserErr := symlinkReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize symlink request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleSymlink(&symlinkReq, req.Session)
		}
	case OpLink:
		linkReq := LinkRequest{}
		if deserErr := linkReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize link request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleLink(&linkReq, req.Session)
		}
	case OpFlush:
		flushReq := FlushRequest{}
		if deserErr := flushReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize flush request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleFlush(&flushReq, req.Session)
		}
	case OpFlushFd:
		flushReq := FlushFdRequest{}
		if deserErr := flushReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize flushfd request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleFlushFd(&flushReq, req.Session)
		}
	case OpReleaseFd:
		releaseReq := ReleaseFdRequest{}
		if deserErr := releaseReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize releasefd request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleReleaseFd(&releaseReq, req.Session)
		}
	case OpGetattr:
		getattrReq := GetattrRequest{}
		if deserErr := getattrReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize getattr request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleGetattr(&getattrReq, req.Session)
		}
	case OpGetlk:
		lockReq := LockRequest{}
		if deserErr := lockReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize getlk request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleGetlk(&lockReq, req.Session)
		}
	case OpSetlk:
		lockReq := LockRequest{}
		if deserErr := lockReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize setlk request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleSetlk(&lockReq, req.Session, false)
		}
	case OpSetlkw:
		lockReq := LockRequest{}
		if deserErr := lockReq.Deserialize(bytes.NewReader(req.Data)); deserErr != nil {
			err = fmt.Errorf("failed to deserialize setlkw request: %w", syscall.EINVAL)
		} else {
			resp, err = fs.handleSetlk(&lockReq, req.Session, true)
		}
	default:
		err = fmt.Errorf("unsupported opcode %d: %w", req.Header.Opcode, syscall.ENOSYS)
	}

	// Convert error to errno and log if needed
	var errno syscall.Errno
	if err != nil {
		errno = toErrno(err)
		// Log unusual errors
		if errno != syscall.ENOENT && errno != syscall.EPERM && errno != syscall.EACCES {
			log.Printf("Request opcode=%d seq=%d failed: %v (errno=%d)", req.Header.Opcode, req.Header.Seq, err, errno)
		}
	} else if resp == nil {
		errno = syscall.EIO
		log.Printf("Request opcode=%d seq=%d returned nil response without error", req.Header.Opcode, req.Header.Seq)
	}

	var respBytes []byte
	if errno != 0 {
		respBytes = fs.makeErrorResponse(req.Header.Seq, errno)
	} else {
		var serErr error
		// Use opcode=0 for success responses
		respBytes, serErr = SerializeWithHeader(0, req.Header.Seq, 0, resp)
		if serErr != nil {
			log.Printf("Failed to serialize response for opcode=%d seq=%d: %v", req.Header.Opcode, req.Header.Seq, serErr)
			respBytes = fs.makeErrorResponse(req.Header.Seq, syscall.EIO)
		}
	}
	//log.Printf("Handled request opcode=%d seq=%d, response size=%d, error=%v", req.Header.Opcode, req.Header.Seq, len(respBytes), err)

	// Route response to appropriate priority queue based on request priority
	if req.Header.Flags&FlagQosLow != 0 {
		select {
		case fs.respQueueLow <- Response{Session: req.Session, Data: respBytes}:
		case <-fs.ctx.Done():
		}
	} else {
		select {
		case fs.respQueueHigh <- Response{Session: req.Session, Data: respBytes}:
		case <-fs.ctx.Done():
		}
	}
}

// SerializeWithHeader serializes a Serializable payload with flags, prepends a header with correct size, and returns the full bytes
// If the payload is larger than 32KB, it will be compressed using zstd and FlagCompressed will be set
func SerializeWithHeader(op uint32, seq uint32, flags uint32, resp Serializable) ([]byte, error) {
	var body bytes.Buffer
	if err := resp.Serialize(&body); err != nil {
		return nil, fmt.Errorf("Failed to serialize response body: %w", err)
	}

	payload := body.Bytes()

	// Compress payload if it exceeds threshold
	if len(payload) > CompressionThreshold {
		encoder, err := zstd.NewWriter(nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
		}
		compressed := encoder.EncodeAll(payload, make([]byte, 0, len(payload)))
		encoder.Close()

		// Only use compression if it actually reduces size
		if len(compressed) < len(payload) {
			payload = compressed
			flags |= FlagCompressed
		}
	}

	totalSize := HeaderSize + len(payload)
	var out bytes.Buffer
	header := Header{Opcode: op, Size: uint32(totalSize), Flags: flags, Seq: seq}
	if err := header.Serialize(&out); err != nil {
		return nil, fmt.Errorf("failed to serialize header: %w", err)
	}
	if _, err := out.Write(payload); err != nil {
		return nil, fmt.Errorf("failed to write body: %w", err)
	}
	return out.Bytes(), nil
}

// NewZstdDecoder creates a new zstd decoder for decompression
func NewZstdDecoder() (*zstd.Decoder, error) {
	return zstd.NewReader(nil)
}

// handleMount handles mount requests
// old handleMount signature removed; new handler defined below
func (fs *FileServer) handleMount(mountReq *MountRequest, session *Session) (Serializable, error) {
	// Check version compatibility
	if mountReq.Version != ProtocolVersion {
		return nil, fmt.Errorf("protocol version mismatch: client=%d, server=%d: %w", mountReq.Version, ProtocolVersion, syscall.EPROTONOSUPPORT)
	}

	// Use the path from the request, or fallback to fs.rootPath
	mountPath := mountReq.Path
	if mountPath == "" || mountPath[0] != '/' {
		mountPath = filepath.Join(fs.rootPath, mountPath)
	}
	mountPath = filepath.Clean(mountPath)
	if !strings.HasPrefix(mountPath+"/", fs.rootPath+"/") {
		return nil, fmt.Errorf("mount path %s is outside root path %s: %w", mountPath, fs.rootPath, syscall.EPERM)
	}
	// Check NFS export authorization: verify the client IP is allowed for this path.
	remoteAddr := session.conn.RemoteAddr()
	tcpAddr, ok := remoteAddr.(*net.TCPAddr)
	if !ok {
		return nil, fmt.Errorf("failed to determine client address: %w", syscall.EACCES)
	}
	if err := checkNFSExport(tcpAddr.IP, mountPath); err != nil {
		return nil, err
	}
	// Trigger lazy mount of zfs snapshot if needed by appending "/." to the path
	mountPath = mountPath + "/."

	// Get file handle for root directory using NameToHandleAt
	fh, mountID, err := unix.NameToHandleAt(unix.AT_FDCWD, mountPath, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file handle for mount path %s: %w", mountPath, err)
	}
	rootFd, err := unix.Open(mountPath, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to open mount path %s: %w", mountPath, err)
	}

	// Get stat for the root directory
	var stat unix.Stat_t
	if err := unix.Fstat(rootFd, &stat); err != nil {
		unix.Close(rootFd)
		return nil, fmt.Errorf("failed to stat mount path %s: %w", mountPath, err)
	}

	// Save mount_id in session
	session.Init(int32(mountID), rootFd)
	session.onClose = func() {
		fs.locks.ReleaseSession(session)
	}

	// Create file attributes for the root
	attr := fs.makeFileAttr(&stat)

	// Build response
	resp := &MountResponse{
		Version: ProtocolVersion,
		Flags:   mountReq.Flags & MountFlagReadOnly,
		Fh:      fh,
		Attr:    attr,
	}

	return resp, nil
}

// handleLookup handles lookup requests (includes getattr)
func (fs *FileServer) handleLookup(lookupReq *LookupRequest, session *Session) (Serializable, error) {
	// Open parent directory
	parentFd, err := unix.OpenByHandleAt(session.rootFd, lookupReq.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory (type=%d, size=%d): %w", lookupReq.ParentFh.Type(), lookupReq.ParentFh.Size(), err)
	}
	defer unix.Close(parentFd)

	// Get file handle using NameToHandleAt
	fileHandle, mountID, err := unix.NameToHandleAt(parentFd, lookupReq.Name, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file handle for %s: %w", lookupReq.Name, err)
	}

	// Verify mount_id matches session
	if int32(mountID) != session.mountID {
		return nil, fmt.Errorf("mount ID mismatch for %s: got=%d, expected=%d: %w", lookupReq.Name, mountID, session.mountID, syscall.EXDEV)
	}

	// Build response
	attr, err := fs.makeFileAttrWithPath(parentFd, lookupReq.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get file attributes for %s: %w", lookupReq.Name, err)
	}
	resp := &LookupResponse{
		Fh:   fileHandle,
		Attr: attr,
	}

	// If it's a directory, enqueue for pre-loading
	if attr.Mode&syscall.S_IFDIR != 0 {
		select {
		case fs.preloadQueue <- PreLoadItem{
			Fh:      fileHandle,
			Ino:     attr.Ino,
			Session: session,
		}:
		default:
			// Queue full, skip pre-loading for this directory
		}
	}

	return resp, nil
}

// handleRead handles read requests
func (fs *FileServer) handleRead(readReq *ReadRequest, session *Session) (Serializable, error) {
	// Open file using OpenByHandleAt
	fd, err := unix.OpenByHandleAt(session.rootFd, readReq.Fh, unix.O_RDONLY)
	if err != nil {
		return nil, fmt.Errorf("failed to open file (type=%d, size=%d): %w", readReq.Fh.Type(), readReq.Fh.Size(), err)
	}
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()

	// Read data
	data := make([]byte, readReq.Size)
	n, err := file.ReadAt(data, int64(readReq.Offset))
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read at offset=%d, size=%d: %w", readReq.Offset, readReq.Size, err)
	}

	// Build response
	resp := &ReadResponse{
		Data: data[:n],
	}

	return resp, nil
}

// handleReadFd handles read requests that reference a session-scoped fd token.
func (fs *FileServer) handleReadFd(readReq *ReadFdRequest, session *Session) (Serializable, error) {
	fd, ok := session.LookupFd(readReq.Fd)
	if !ok {
		return nil, fmt.Errorf("failed to resolve fd %d: %w", readReq.Fd, syscall.EBADF)
	}

	data := make([]byte, readReq.Size)
	n, err := unix.Pread(fd, data, int64(readReq.Offset))
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read at offset=%d, size=%d: %w", readReq.Offset, readReq.Size, err)
	}

	return &ReadResponse{Data: data[:n]}, nil
}

// handleReadDir handles readdir requests
func (fs *FileServer) handleReadDir(readDirReq *ReadDirRequest, session *Session) (Serializable, error) {
	// Open directory using OpenByHandleAt
	fd, err := unix.OpenByHandleAt(session.rootFd, readDirReq.Fh, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open directory (type=%d, size=%d): %w", readDirReq.Fh.Type(), readDirReq.Fh.Size(), err)
	}
	file := os.NewFile(uintptr(fd), "")
	file.Seek(0, 0)
	defer file.Close()

	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory entries: %w", err)
	}

	// Build directory entries with file handles using NameToHandleAt
	dirEntries := make([]DirEntry, 0, len(entries))
	for _, entry := range entries {
		// Get file handle using NameToHandleAt
		entryFileHandle, entryMountID, err := unix.NameToHandleAt(fd, entry.Name(), 0)
		if err != nil {
			// Skip entries we can't get handles for
			log.Printf("Failed to get file handle for %s: %v", entry.Name(), err)
			continue
		}

		// Verify mount_id matches session
		if int32(entryMountID) != session.mountID {
			log.Printf("Skipping entry %s due to mount ID mismatch: %d vs %d", entry.Name(), entryMountID, session.mountID)
			// Skip entries from different filesystems
			continue
		}

		attr, err := fs.makeFileAttrWithPath(fd, entry.Name())
		if err != nil {
			log.Printf("Failed to get file attributes for %s: %v", entry.Name(), err)
			continue
		}
		dirEntries = append(dirEntries, DirEntry{
			Fh:   entryFileHandle,
			Name: entry.Name(),
			Attr: attr,
		})

		// If it's a directory, enqueue for pre-loading
		if attr.Mode&syscall.S_IFDIR != 0 {
			select {
			case fs.preloadQueue <- PreLoadItem{
				Fh:      entryFileHandle,
				Ino:     attr.Ino,
				Session: session,
			}:
			default:
				// Queue full, skip pre-loading for this directory
			}
		}
	}

	// Build response
	resp := &ReadDirResponse{
		Entries: dirEntries,
	}

	return resp, nil
}

// handleReadlink handles readlink requests
func (fs *FileServer) handleReadlink(readlinkReq *ReadlinkRequest, session *Session) (Serializable, error) {
	// Open symlink using OpenByHandleAt with O_PATH|O_NOFOLLOW
	fd, err := unix.OpenByHandleAt(session.rootFd, readlinkReq.Fh, unix.O_PATH|unix.O_NOFOLLOW)
	if err != nil {
		return nil, fmt.Errorf("failed to open symlink (type=%d, size=%d): %w", readlinkReq.Fh.Type(), readlinkReq.Fh.Size(), err)
	}
	defer unix.Close(fd)

	// Read symlink target using readlinkat on the /proc/self/fd path
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(fd, "", buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read symlink target: %w", err)
	}

	// Build response
	resp := &ReadlinkResponse{
		Target: string(buf[:n]),
	}

	return resp, nil
}

// handleCreate handles file creation requests
func (fs *FileServer) handleCreate(req *CreateRequest, session *Session) (Serializable, error) {
	parentFd, err := unix.OpenByHandleAt(session.rootFd, req.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer unix.Close(parentFd)

	flags := int(req.Flags) | os.O_CREATE
	fd, err := unix.Openat(parentFd, req.Name, flags, req.Mode)
	if err != nil {
		return nil, fmt.Errorf("failed to create file %s: %w", req.Name, err)
	}

	if req.HasOwner != 0 {
		if err := unix.Fchown(fd, int(req.Uid), int(req.Gid)); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("failed to set owner on file %s to %d:%d: %w", req.Name, req.Uid, req.Gid, err)
		}
	}

	// Get file handle
	fileHandle, mountID, err := unix.NameToHandleAt(parentFd, req.Name, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file handle for %s: %w", req.Name, err)
	}
	if int32(mountID) != session.mountID {
		unix.Close(fd)
		return nil, fmt.Errorf("mount ID mismatch: %w", syscall.EXDEV)
	}

	attr, err := fs.makeFileAttrWithPath(parentFd, req.Name)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}

	fdToken := session.TrackFd(fd)
	return &CreateResponse{Fd: fdToken, Fh: fileHandle, Attr: attr}, nil
}

// handleOpen opens an existing file and returns a session-scoped fd token.
func (fs *FileServer) handleOpen(req *OpenRequest, session *Session) (Serializable, error) {
	openFlags := int(req.Flags) | unix.O_CLOEXEC
	if openFlags&(unix.O_RDONLY|unix.O_WRONLY|unix.O_RDWR) == 0 {
		openFlags |= unix.O_RDONLY
	}
	fd, err := unix.OpenByHandleAt(session.rootFd, req.Fh, openFlags)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	if req.HasOwner != 0 {
		if err := unix.Fchown(fd, int(req.Uid), int(req.Gid)); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("failed to set owner on open file to %d:%d: %w", req.Uid, req.Gid, err)
		}
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("failed to stat opened file: %w", err)
	}

	fdToken := session.TrackFd(fd)
	return &OpenResponse{Fd: fdToken, Attr: fs.makeFileAttr(&stat)}, nil
}

// handleWrite handles write requests
func (fs *FileServer) handleWrite(req *WriteRequest, session *Session) (Serializable, error) {
	fd, err := unix.OpenByHandleAt(session.rootFd, req.Fh, unix.O_WRONLY)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for writing: %w", err)
	}
	defer unix.Close(fd)

	n, err := unix.Pwrite(fd, req.Data, int64(req.Offset))
	if err != nil {
		return nil, fmt.Errorf("failed to write at offset=%d: %w", req.Offset, err)
	}

	return &WriteResponse{Size: uint32(n)}, nil
}

// handleWriteFd handles writes using a session-scoped fd token.
func (fs *FileServer) handleWriteFd(req *WriteFdRequest, session *Session) (Serializable, error) {
	fd, ok := session.LookupFd(req.Fd)
	if !ok {
		return nil, fmt.Errorf("failed to resolve fd %d: %w", req.Fd, syscall.EBADF)
	}
	n, err := unix.Pwrite(fd, req.Data, int64(req.Offset))
	if err != nil {
		return nil, fmt.Errorf("failed to write at offset=%d: %w", req.Offset, err)
	}
	return &WriteResponse{Size: uint32(n)}, nil
}

// handleMkdir handles mkdir requests
func (fs *FileServer) handleMkdir(req *MkdirRequest, session *Session) (Serializable, error) {
	parentFd, err := unix.OpenByHandleAt(session.rootFd, req.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer unix.Close(parentFd)

	if err := unix.Mkdirat(parentFd, req.Name, req.Mode); err != nil {
		return nil, fmt.Errorf("failed to mkdir %s: %w", req.Name, err)
	}

	if req.HasOwner != 0 {
		if err := unix.Fchownat(parentFd, req.Name, int(req.Uid), int(req.Gid), unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("failed to set owner on dir %s to %d:%d: %w", req.Name, req.Uid, req.Gid, err)
		}
	}

	fileHandle, mountID, err := unix.NameToHandleAt(parentFd, req.Name, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file handle for %s: %w", req.Name, err)
	}
	if int32(mountID) != session.mountID {
		return nil, fmt.Errorf("mount ID mismatch: %w", syscall.EXDEV)
	}

	attr, err := fs.makeFileAttrWithPath(parentFd, req.Name)
	if err != nil {
		return nil, err
	}

	return &MkdirResponse{Fh: fileHandle, Attr: attr}, nil
}

// handleUnlink handles unlink requests
func (fs *FileServer) handleUnlink(req *UnlinkRequest, session *Session) (Serializable, error) {
	parentFd, err := unix.OpenByHandleAt(session.rootFd, req.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer unix.Close(parentFd)

	if err := unix.Unlinkat(parentFd, req.Name, 0); err != nil {
		return nil, fmt.Errorf("failed to unlink %s: %w", req.Name, err)
	}

	return &UnlinkResponse{}, nil
}

// handleRmdir handles rmdir requests
func (fs *FileServer) handleRmdir(req *RmdirRequest, session *Session) (Serializable, error) {
	parentFd, err := unix.OpenByHandleAt(session.rootFd, req.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer unix.Close(parentFd)

	if err := unix.Unlinkat(parentFd, req.Name, unix.AT_REMOVEDIR); err != nil {
		return nil, fmt.Errorf("failed to rmdir %s: %w", req.Name, err)
	}

	return &RmdirResponse{}, nil
}

// handleRename handles rename requests
func (fs *FileServer) handleRename(req *RenameRequest, session *Session) (Serializable, error) {
	oldParentFd, err := unix.OpenByHandleAt(session.rootFd, req.OldParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open old parent directory: %w", err)
	}
	defer unix.Close(oldParentFd)

	newParentFd, err := unix.OpenByHandleAt(session.rootFd, req.NewParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open new parent directory: %w", err)
	}
	defer unix.Close(newParentFd)

	if err := unix.Renameat(oldParentFd, req.OldName, newParentFd, req.NewName); err != nil {
		return nil, fmt.Errorf("failed to rename %s to %s: %w", req.OldName, req.NewName, err)
	}

	return &RenameResponse{}, nil
}

// handleSetattr handles setattr requests
func (fs *FileServer) handleSetattr(req *SetattrRequest, session *Session) (Serializable, error) {
	fd, err := unix.OpenByHandleAt(session.rootFd, req.Fh, unix.O_PATH)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for setattr: %w", err)
	}
	defer unix.Close(fd)

	if req.Valid&SetattrMode != 0 {
		if err := unix.Fchmodat(fd, "", req.Mode, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("failed to chmod: %w", err)
		}
	}
	if req.Valid&(SetattrUid|SetattrGid) != 0 {
		uid := -1
		gid := -1
		if req.Valid&SetattrUid != 0 {
			uid = int(req.Uid)
		}
		if req.Valid&SetattrGid != 0 {
			gid = int(req.Gid)
		}
		if err := unix.Fchownat(fd, "", uid, gid, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("failed to chown: %w", err)
		}
	}
	if req.Valid&SetattrSize != 0 {
		fdForWrite, err := unix.OpenByHandleAt(session.rootFd, req.Fh, unix.O_WRONLY)
		if err != nil {
			return nil, fmt.Errorf("failed to open file for truncate: %w", err)
		}
		if err := unix.Ftruncate(fdForWrite, req.Size); err != nil {
			unix.Close(fdForWrite)
			return nil, fmt.Errorf("failed to truncate: %w", err)
		}
		unix.Close(fdForWrite)
	}
	if req.Valid&(SetattrMtime|SetattrAtime) != 0 {
		ts := []unix.Timespec{
			{Sec: 0, Nsec: unix.UTIME_OMIT}, // atime
			{Sec: 0, Nsec: unix.UTIME_OMIT}, // mtime
		}
		if req.Valid&SetattrAtime != 0 {
			ts[0] = unix.Timespec{Sec: req.Atime}
		}
		if req.Valid&SetattrMtime != 0 {
			ts[1] = unix.Timespec{Sec: req.Mtime}
		}
		if err := unix.UtimesNanoAt(fd, "", ts, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("failed to set times: %w", err)
		}
	}

	// Re-stat to get updated attributes
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, "", &stat, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, fmt.Errorf("failed to stat after setattr: %w", err)
	}

	return &SetattrResponse{Attr: fs.makeFileAttr(&stat)}, nil
}

// handleSymlink handles symlink creation requests
func (fs *FileServer) handleSymlink(req *SymlinkRequest, session *Session) (Serializable, error) {
	parentFd, err := unix.OpenByHandleAt(session.rootFd, req.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer unix.Close(parentFd)

	if err := unix.Symlinkat(req.Target, parentFd, req.Name); err != nil {
		return nil, fmt.Errorf("failed to symlink %s -> %s: %w", req.Name, req.Target, err)
	}

	if req.HasOwner != 0 {
		if err := unix.Fchownat(parentFd, req.Name, int(req.Uid), int(req.Gid), unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, fmt.Errorf("failed to set owner on symlink %s to %d:%d: %w", req.Name, req.Uid, req.Gid, err)
		}
	}

	fileHandle, mountID, err := unix.NameToHandleAt(parentFd, req.Name, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file handle: %w", err)
	}
	if int32(mountID) != session.mountID {
		return nil, fmt.Errorf("mount ID mismatch: %w", syscall.EXDEV)
	}

	attr, err := fs.makeFileAttrWithPath(parentFd, req.Name)
	if err != nil {
		return nil, err
	}

	return &SymlinkResponse{Fh: fileHandle, Attr: attr}, nil
}

// handleLink handles hard link creation requests
func (fs *FileServer) handleLink(req *LinkRequest, session *Session) (Serializable, error) {
	// Open target by handle to get a /proc fd path for linkat
	targetFd, err := unix.OpenByHandleAt(session.rootFd, req.TargetFh, unix.O_PATH)
	if err != nil {
		return nil, fmt.Errorf("failed to open target: %w", err)
	}
	defer unix.Close(targetFd)

	parentFd, err := unix.OpenByHandleAt(session.rootFd, req.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		return nil, fmt.Errorf("failed to open parent directory: %w", err)
	}
	defer unix.Close(parentFd)

	procPath := fmt.Sprintf("/proc/self/fd/%d", targetFd)
	if err := unix.Linkat(unix.AT_FDCWD, procPath, parentFd, req.Name, unix.AT_SYMLINK_FOLLOW); err != nil {
		return nil, fmt.Errorf("failed to link %s: %w", req.Name, err)
	}

	fileHandle, mountID, err := unix.NameToHandleAt(parentFd, req.Name, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file handle: %w", err)
	}
	if int32(mountID) != session.mountID {
		return nil, fmt.Errorf("mount ID mismatch: %w", syscall.EXDEV)
	}

	attr, err := fs.makeFileAttrWithPath(parentFd, req.Name)
	if err != nil {
		return nil, err
	}

	return &LinkResponse{Fh: fileHandle, Attr: attr}, nil
}

// handleFlush handles flush requests without server-side hashing.
// If the client provides a SHA256 value, it is persisted as xattr metadata.
func (fs *FileServer) handleFlush(req *FlushRequest, session *Session) (Serializable, error) {
	fd, err := unix.OpenByHandleAt(session.rootFd, req.Fh, unix.O_RDONLY)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for flush: %w", err)
	}
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()

	// Fsync the file
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("failed to sync file: %w", err)
	}

	// Re-stat
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("failed to stat file after flush: %w", err)
	}

	attr := fs.makeFileAttr(&stat)

	if req.HasSha256 != 0 {
		copy(attr.Sha256[:], req.Sha256[:])
		sha256Hex := hex.EncodeToString(req.Sha256[:])
		mtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)
		xval := fmt.Sprintf("1:%s:%d.%d:%d", sha256Hex, mtime.Unix(), mtime.Nanosecond(), stat.Size)
		_ = unix.Fsetxattr(fd, XattrCacheKey, []byte(xval), 0)
	}

	return &FlushResponse{Attr: attr}, nil
}

func (fs *FileServer) handleFlushFd(req *FlushFdRequest, session *Session) (Serializable, error) {
	fd, ok := session.LookupFd(req.Fd)
	if !ok {
		return nil, fmt.Errorf("failed to resolve fd %d: %w", req.Fd, syscall.EBADF)
	}
	if err := unix.Fsync(fd); err != nil {
		return nil, fmt.Errorf("failed to sync file: %w", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("failed to stat file after flush: %w", err)
	}

	attr := fs.makeFileAttr(&stat)
	if req.HasSha256 != 0 {
		copy(attr.Sha256[:], req.Sha256[:])
		sha256Hex := hex.EncodeToString(req.Sha256[:])
		mtime := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)
		xval := fmt.Sprintf("1:%s:%d.%d:%d", sha256Hex, mtime.Unix(), mtime.Nanosecond(), stat.Size)
		_ = unix.Fsetxattr(fd, XattrCacheKey, []byte(xval), 0)
	}
	return &FlushResponse{Attr: attr}, nil
}

func (fs *FileServer) handleReleaseFd(req *ReleaseFdRequest, session *Session) (Serializable, error) {
	session.CloseFd(req.Fd)
	return &EmptyResponse{}, nil
}

// Helper functions for encoding

func (fs *FileServer) makeFileAttr(info *unix.Stat_t) FileAttr {
	attr := FileAttr{
		Dev:      info.Dev,
		Ino:      info.Ino,
		Nlink:    uint64(info.Nlink),
		Mode:     info.Mode,
		Uid:      info.Uid,
		Gid:      info.Gid,
		Rdev:     info.Rdev,
		Size:     info.Size,
		Blksize:  int64(info.Blksize),
		Blocks:   info.Blocks,
		Mtim:     info.Mtim.Sec,
		MtimNsec: int32(info.Mtim.Nsec),
		Ctim:     info.Ctim.Sec,
		CtimNsec: int32(info.Ctim.Nsec),
	}
	// SHA256 defaults to all zeros
	// Will be populated by caller if available
	return attr
}

// makeFileAttrWithPath creates FileAttr with SHA256 from xattr
func (fs *FileServer) makeFileAttrWithPath(parentFd int, name string) (FileAttr, error) {
	stat := unix.Stat_t{}
	if err := unix.Fstatat(parentFd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return FileAttr{}, fmt.Errorf("failed to stat file: %w", err)
	}
	attr := fs.makeFileAttr(&stat)

	if stat.Mode&syscall.S_IFMT == syscall.S_IFREG {
		fd, err := unix.Openat(parentFd, name, unix.O_RDONLY|unix.AT_SYMLINK_NOFOLLOW, 0)
		if err != nil {
			return attr, fmt.Errorf("failed to open file for xattr: %w", err)
		}
		defer unix.Close(fd)

		// Try to read SHA256 from xattr user.veldafs.cache
		var cacheKey [128]byte
		sz, err := unix.Fgetxattr(fd, "user.veldafs.cache", cacheKey[:])
		if err == nil {
			sha256sum, mtime, size, ok := decodeCacheXattr(string(cacheKey[:sz]))
			if ok && len(sha256sum) == 64 && size == attr.Size && mtime.Equal(time.Unix(attr.Mtim, int64(attr.MtimNsec))) {
				// Decode hex string to bytes
				sha256Bytes, err := hex.DecodeString(sha256sum)
				if err == nil && len(sha256Bytes) == 32 {
					copy(attr.Sha256[:], sha256Bytes)
				}
			} else {
				log.Printf("Invalid cache xattr for %s: %s, expected: %d:%d:%d", name, string(cacheKey[:sz]), attr.Mtim, attr.MtimNsec, attr.Size)
			}
		}
		// If xattr doesn't exist or has wrong size, Sha256 remains all zeros
	}

	return attr, nil
}

// handleGetattr handles getattr requests — fetches fresh stat and optional SHA256 xattr for a file handle.
func (fs *FileServer) handleGetattr(req *GetattrRequest, session *Session) (Serializable, error) {
	fd, err := unix.OpenByHandleAt(session.rootFd, req.Fh, unix.O_PATH|unix.O_NOFOLLOW)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for getattr: %w", err)
	}
	defer unix.Close(fd)

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("failed to stat file for getattr: %w", err)
	}

	//log.Printf("Getattr for fh (type=%d, size=%d): stat=%+v", req.Fh.Type(), req.Fh.Size(), stat)
	attr := fs.makeFileAttr(&stat)

	// For regular files, try to read the SHA256 xattr written by flush.
	if stat.Mode&syscall.S_IFMT == syscall.S_IFREG {
		var xval [256]byte
		if sz, xerr := unix.Fgetxattr(fd, XattrCacheKey, xval[:]); xerr == nil {
			sha256sum, mtime, size, ok := decodeCacheXattr(string(xval[:sz]))
			if ok && size == attr.Size && mtime.Equal(time.Unix(attr.Mtim, int64(attr.MtimNsec))) {
				if sha256Bytes, decErr := hex.DecodeString(sha256sum); decErr == nil && len(sha256Bytes) == 32 {
					copy(attr.Sha256[:], sha256Bytes)
				}
			}
		}
	}

	return &GetattrResponse{Attr: attr}, nil
}

func (fs *FileServer) handleGetlk(req *LockRequest, session *Session) (Serializable, error) {
	fd, ok := session.LookupFd(req.Fd)
	if !ok {
		return nil, fmt.Errorf("failed to resolve fd %d: %w", req.Fd, syscall.EBADF)
	}
	lk, err := fs.locks.Getlk(fd, req.Owner, req.Lk, req.Flags)
	if err != nil {
		return nil, err
	}
	return &GetlkResponse{Lk: lk}, nil
}

func (fs *FileServer) handleSetlk(req *LockRequest, session *Session, blocking bool) (Serializable, error) {
	fd, ok := session.LookupFd(req.Fd)
	if !ok {
		return nil, fmt.Errorf("failed to resolve fd %d: %w", req.Fd, syscall.EBADF)
	}
	if err := fs.locks.Setlk(fd, req.Owner, req.Lk, req.Flags, blocking); err != nil {
		return nil, err
	}
	return &EmptyResponse{}, nil
}

func (fs *FileServer) makeErrorResponse(seq uint32, errno syscall.Errno) []byte {
	var buf bytes.Buffer
	header := Header{Opcode: uint32(errno), Size: uint32(HeaderSize), Seq: seq}
	if err := header.Serialize(&buf); err != nil {
		log.Printf("Error serializing error header: %v", err)
		return nil
	}
	return buf.Bytes()
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

func toErrno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return syscall.EIO
}

// checkNFSExport verifies that clientIP is allowed to access exportPath according
// to the system NFS exports table at /var/lib/nfs/etab. It checks only whether
// an entry covers the path and allows the client; export options are ignored.
func checkNFSExport(clientIP net.IP, exportPath string) error {
	if clientIP.IsLoopback() {
		// Allow loopback clients regardless of spec for testing.
		return nil
	}
	data, err := os.ReadFile("/var/lib/nfs/etab")
	if err != nil {
		return fmt.Errorf("failed to read NFS exports (/var/lib/nfs/etab): %w", syscall.EIO)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Each line: /export/path client1(opts) client2(opts) ...
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		entryPath := fields[0]

		// The export must cover the requested path (exact or prefix match).
		if exportPath != entryPath && !strings.HasPrefix(exportPath+"/", entryPath+"/") {
			continue
		}

		for _, clientSpec := range fields[1:] {
			// Strip options: client(opts) -> client
			client := clientSpec
			if idx := strings.IndexByte(clientSpec, '('); idx >= 0 {
				client = clientSpec[:idx]
			}
			if matchNFSClient(client, clientIP) {
				return nil
			}
		}
	}

	return fmt.Errorf("client %s not authorized for export path %s: %w", clientIP, exportPath, syscall.EACCES)
}

// matchNFSClient reports whether spec (from /var/lib/nfs/etab) matches clientIP.
// Supported spec forms: "*" (wildcard), CIDR notation, exact IP address.
func matchNFSClient(spec string, clientIP net.IP) bool {
	if spec == "*" {
		return true
	}
	// CIDR (e.g. 192.168.1.0/24)
	if _, ipNet, err := net.ParseCIDR(spec); err == nil {
		return ipNet.Contains(clientIP)
	}
	// Exact IP
	if ip := net.ParseIP(spec); ip != nil {
		return ip.Equal(clientIP)
	}
	return false
}
