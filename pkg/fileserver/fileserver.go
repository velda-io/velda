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
package fileserver

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	fuse "github.com/hanwen/go-fuse/v2/fs"
)

var XattrCacheKey = "user.veldafs.sha256"
var ToErrno = fuse.ToErrno

// FileServer manages snapshot file serving with custom protocol
type FileServer struct {
	rootPath string
	listener net.Listener

	// Shared state across all connections
	mu       sync.RWMutex
	sessions map[*Session]bool

	// Request and response queues
	reqQueue  chan Request
	respQueue chan Response

	// Worker management
	numWorkers int
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
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
		rootPath:   rootPath,
		sessions:   make(map[*Session]bool),
		reqQueue:   make(chan Request, 100),
		respQueue:  make(chan Response, 100),
		numWorkers: numWorkers,
		ctx:        ctx,
		cancel:     cancel,
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
		session.SetReadDeadline(time.Now().Add(30 * time.Second))
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
		case resp := <-fs.respQueue:
			resp.Session.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_, err := resp.Session.Write(resp.Data)
			if err != nil {
				fmt.Printf("Write response error: %v\n", err)
			}
		}
	}
}

// handleRequest dispatches request to appropriate handler
func (fs *FileServer) handleRequest(req Request) {
	var resp Serializable
	var errno syscall.Errno

	switch req.Header.Opcode {
	case OpMount:
		mountReq := MountRequest{}
		if err := mountReq.Deserialize(bytes.NewReader(req.Data)); err != nil {
			errno = syscall.EINVAL
		} else {
			resp, errno = fs.handleMount(&mountReq, req.Session)
		}
	case OpLookup:
		lookupReq := LookupRequest{}
		if err := lookupReq.Deserialize(bytes.NewReader(req.Data)); err != nil {
			errno = syscall.EINVAL
		} else {
			resp, errno = fs.handleLookup(&lookupReq, req.Session)
		}
	case OpRead:
		readReq := ReadRequest{}
		if err := readReq.Deserialize(bytes.NewReader(req.Data)); err != nil {
			errno = syscall.EINVAL
		} else {
			resp, errno = fs.handleRead(&readReq, req.Session)
		}
	case OpReadDir:
		readDirReq := ReadDirRequest{}
		if err := readDirReq.Deserialize(bytes.NewReader(req.Data)); err != nil {
			errno = syscall.EINVAL
		} else {
			resp, errno = fs.handleReadDir(&readDirReq, req.Session)
		}
	default:
		errno = syscall.ENOSYS
	}
	if resp == nil && errno == 0 {
		errno = syscall.EIO
	}
	var respBytes []byte
	if errno != 0 {
		respBytes = fs.makeErrorResponse(req.Header.Seq, errno)
	} else {
		var err error
		// Use opcode=0 for success responses
		respBytes, err = SerializeWithHeader(0, req.Header.Seq, resp)
		if err != nil {
			log.Printf("Failed to serialize response: %v", err)
			respBytes = fs.makeErrorResponse(req.Header.Seq, syscall.EIO)
		}
	}
	log.Printf("Handled request opcode %d, seq %d, errno %s", req.Header.Opcode, req.Header.Seq, errno)

	select {
	case fs.respQueue <- Response{Session: req.Session, Data: respBytes}:
	case <-fs.ctx.Done():
	}
}

// serializeResponse converts a response to bytes
// SerializeWithHeader serializes a Serializable payload, prepends a header with correct size, and returns the full bytes
func SerializeWithHeader(op uint32, seq uint32, resp Serializable) ([]byte, error) {
	var body bytes.Buffer
	if err := resp.Serialize(&body); err != nil {
		return nil, fmt.Errorf("Failed to serialize response body: %w", err)
	}

	totalSize := HeaderSize + body.Len()
	var out bytes.Buffer
	header := Header{Opcode: op, Size: uint32(totalSize), Seq: seq}
	if err := header.Serialize(&out); err != nil {
		return nil, fmt.Errorf("failed to serialize header: %w", err)
	}
	if _, err := out.Write(body.Bytes()); err != nil {
		return nil, fmt.Errorf("failed to write body: %w", err)
	}
	return out.Bytes(), nil
}

// handleMount handles mount requests
// old handleMount signature removed; new handler defined below
func (fs *FileServer) handleMount(mountReq *MountRequest, session *Session) (Serializable, syscall.Errno) {
	// Check version compatibility
	if mountReq.Version != ProtocolVersion {
		return nil, syscall.EPROTONOSUPPORT
	}

	// Get file handle for root directory using NameToHandleAt
	fh, mountID, err := unix.NameToHandleAt(unix.AT_FDCWD, fs.rootPath, 0)
	if err != nil {
		return nil, ToErrno(err)
	}
	rootFd, err := unix.Open(fs.rootPath, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, ToErrno(err)
	}
	stat, err := os.Stat(fs.rootPath)
	if err != nil {
		unix.Close(rootFd)
		return nil, ToErrno(err)
	}
	log.Printf("root stat: %s, size %d, isDir %t, ino: %d", stat.Name(), stat.Size(), stat.IsDir(), stat.Sys().(*syscall.Stat_t).Ino)

	// Save mount_id in session
	session.Init(int32(mountID), rootFd)

	// Build response
	resp := &MountResponse{
		Version: ProtocolVersion,
		Flags:   mountReq.Flags & FlagReadOnly,
		Fh:      fh,
	}

	return resp, 0
}

// handleLookup handles lookup requests (includes getattr)
func (fs *FileServer) handleLookup(lookupReq *LookupRequest, session *Session) (Serializable, syscall.Errno) {
	// Open parent directory
	parentFd, err := unix.OpenByHandleAt(session.rootFd, lookupReq.ParentFh, unix.O_DIRECTORY)
	if err != nil {
		log.Printf("Failed to open parent directory: %v, %d %d %v", err, lookupReq.ParentFh.Type(), lookupReq.ParentFh.Size(), lookupReq.ParentFh.Bytes())
		return nil, ToErrno(err)
	}
	defer unix.Close(parentFd)

	// Get file handle using NameToHandleAt
	fileHandle, mountID, err := unix.NameToHandleAt(parentFd, lookupReq.Name, 0)
	if err != nil {
		log.Printf("Failed to get file handle for %s: %v", lookupReq.Name, err)
		return nil, ToErrno(err)
	}

	// Verify mount_id matches session
	if int32(mountID) != session.mountID {
		return nil, ToErrno(syscall.EXDEV)
	}

	// Build response
	attr, err := fs.makeFileAttrWithPath(parentFd, lookupReq.Name)
	if err != nil {
		return nil, ToErrno(err)
	}
	resp := &LookupResponse{
		Fh:   fileHandle,
		Attr: attr,
	}

	return resp, 0
}

// handleRead handles read requests
func (fs *FileServer) handleRead(readReq *ReadRequest, session *Session) (Serializable, syscall.Errno) {
	// Open file using OpenByHandleAt
	fd, err := unix.OpenByHandleAt(session.rootFd, readReq.Fh, unix.O_RDONLY)
	if err != nil {
		return nil, ToErrno(err)
	}
	file := os.NewFile(uintptr(fd), "")
	defer file.Close()

	// Read data
	data := make([]byte, readReq.Size)
	n, err := file.ReadAt(data, int64(readReq.Offset))
	if err != nil && err != io.EOF {
		return nil, ToErrno(err)
	}

	// Build response
	resp := &ReadResponse{
		Data: data[:n],
	}

	return resp, 0
}

// handleReadDir handles readdir requests
func (fs *FileServer) handleReadDir(readDirReq *ReadDirRequest, session *Session) (Serializable, syscall.Errno) {
	// Open directory using OpenByHandleAt
	fd, err := unix.OpenByHandleAt(session.rootFd, readDirReq.Fh, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, ToErrno(err)
	}
	file := os.NewFile(uintptr(fd), "")
	file.Seek(0, 0)
	defer file.Close()

	entries, err := file.ReadDir(-1)
	if err != nil {
		return nil, ToErrno(err)
	}

	// Apply offset and count
	start := int(readDirReq.Offset)
	if start >= len(entries) {
		start = len(entries)
	}
	end := start + int(readDirReq.Count)
	if end > len(entries) {
		end = len(entries)
	}

	log.Printf("ReadDir: %d entries, offset %d, count %d, returned %d entries", len(entries), readDirReq.Offset, readDirReq.Count, len(entries))
	entries = entries[start:end]

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
	}

	// Build response
	resp := &ReadDirResponse{
		Entries: dirEntries,
	}

	return resp, 0
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

	fd, err := unix.Openat(parentFd, name, unix.O_RDONLY, 0)
	if err != nil {
		return attr, fmt.Errorf("failed to open file: %w", err)
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
			} else {
				log.Printf("Invalid SHA256 hex string for %s: %s", name, sha256sum)
			}
		} else {
			log.Printf("Invalid cache xattr for %s: %s", name, string(cacheKey[:sz]))
		}
	} else {
		log.Printf("Failed to read xattr for %s: %v", name, err)
	}
	// If xattr doesn't exist or has wrong size, Sha256 remains all zeros

	return attr, nil
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
