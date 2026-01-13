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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/fileserver"
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
	mountPoint string

	// Connection state
	mu       sync.Mutex
	seq      uint32
	pending  map[uint32]chan Response
	rootFh   unix.FileHandle
	rootAttr fileserver.FileAttr

	// Cache from sandboxfs
	cache *DirectoryCacheManager

	// FUSE state
	fuseServer *fuse.Server
	rootNode   *SnapshotNode
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewDirectFSClient creates a new direct filesystem client
func NewDirectFSClient(serverAddr string, cache *DirectoryCacheManager) *DirectFSClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &DirectFSClient{
		serverAddr: serverAddr,
		pending:    make(map[uint32]chan Response),
		cache:      cache,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Connect connects to the file server and performs mount
func (sc *DirectFSClient) Connect() error {
	conn, err := net.Dial("tcp", sc.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	sc.conn = conn

	// Start response reader
	sc.wg.Add(1)
	go sc.readResponses()

	// Send mount request
	mountReq := fileserver.MountRequest{
		Version: fileserver.ProtocolVersion,
		Flags:   fileserver.FlagReadOnly,
	}

	var mountResp fileserver.MountResponse
	err = sc.SendRequest(&mountReq, &mountResp)
	if err != nil {
		sc.conn.Close()
		return fmt.Errorf("mount failed: %w", err)
	}
	sc.rootFh = mountResp.Fh
	return nil
}

// Mount mounts the filesystem at the given mount point
func (sc *DirectFSClient) Mount(mountPoint string) (*VeldaServer, error) {
	sc.mountPoint = mountPoint

	// Create root node
	sc.rootNode = &SnapshotNode{
		client: sc,
		fh:     sc.rootFh,
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
	server, err := fs.Mount(mountPoint, sc.rootNode, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to mount: %w", err)
	}

	sc.fuseServer = server

	return &VeldaServer{
		Server: server,
		Cache:  sc.cache,
		Root:   nil, // Not using CachedLoopbackNode for snapshots
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
	sc.wg.Wait()
}

// nextSeq generates the next sequence number
func (sc *DirectFSClient) nextSeq() uint32 {
	return atomic.AddUint32(&sc.seq, 1)
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

// SnapshotNode implements fs.InodeEmbedder for persistent inodes
type SnapshotNode struct {
	fs.Inode
	client *DirectFSClient
	fh     unix.FileHandle     // File handle from server
	attr   fileserver.FileAttr // Cached attributes
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

// Getattr returns file attributes
func (n *SnapshotNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	toFuseAttr(&n.attr, &out.Attr)
	return fs.OK
}

// Lookup looks up a child node
func (n *SnapshotNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Send lookup request
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
	// Send readdir request
	readDirReq := fileserver.ReadDirRequest{
		Fh:     n.fh,
		Offset: 0,
		Count:  10000, // Read many entries at once for snapshots
	}

	readDirResp := fileserver.ReadDirResponse{}
	err := n.client.SendRequest(&readDirReq, &readDirResp)
	if err != nil {
		return nil, fs.ToErrno(err)
	}

	// Convert to fuse.DirEntry
	entries := make([]fuse.DirEntry, len(readDirResp.Entries))
	for i, entry := range readDirResp.Entries {
		entries[i] = fuse.DirEntry{
			Name: entry.Name,
			Mode: entry.Attr.Mode,
			Ino:  entry.Attr.Ino,
		}
	}

	return fs.NewListDirStream(entries), fs.OK
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
				loopbackFile := fs.NewLoopbackFile(fd)
				if lf, ok := loopbackFile.(*fs.LoopbackFile); ok {
					return &SnapshotFile{
						baseFile:  lf,
						client:    n.client,
						fh:        n.fh,
						attr:      n.attr,
						cache:     n.client.cache,
						cacheFd:   fd,
						fromCache: true,
					}, fuse.FOPEN_KEEP_CACHE, fs.OK
				}
				// Fallback: close fd if type assertion fails
				unix.Close(fd)
			}
		}
	}

	// Create file handle for remote access
	file := &SnapshotFile{
		client:    n.client,
		fh:        n.fh,
		attr:      n.attr,
		cache:     n.client.cache,
		cacheFd:   -1,
		fromCache: false,
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

// SnapshotFile implements fs.FileHandle for reading files
type SnapshotFile struct {
	baseFile  *fs.LoopbackFile // Embedded for passthrough mode when cached
	client    *DirectFSClient
	fh        unix.FileHandle
	attr      fileserver.FileAttr
	cache     *DirectoryCacheManager
	cacheFd   int  // File descriptor for cached file (-1 if not cached)
	fromCache bool // Whether this file is served from cache
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
	if f.fromCache && f.baseFile != nil {
		return f.baseFile.Read(ctx, dest, off)
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
	} else {
		log.Printf("Not caching file with SHA256 %s (size: %d, read size: %d)\n", sha256Hex, f.attr.Size, len(readResp.Data))
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
	if f.cacheFd >= 0 {
		unix.Close(f.cacheFd)
		f.cacheFd = -1
	}
	if f.baseFile != nil {
		return f.baseFile.Release(ctx)
	}
	return fs.OK
}

var _ = (fs.FilePassthroughFder)((*SnapshotFile)(nil))

func (f *SnapshotFile) PassthroughFd() (int, bool) {
	if f.fromCache && f.baseFile != nil {
		return f.baseFile.PassthroughFd()
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
