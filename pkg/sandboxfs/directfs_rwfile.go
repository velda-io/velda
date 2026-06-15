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
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
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
	// Chunk size for eager fetch reads
	rwEagerFetchChunk = 1024 * 1024 // 1 MB
)

// RWFile implements fs.FileHandle for read-write files over the directfs protocol.
//
// Reads are served from SHA256-based cache when available, with eager background
// fetch populating the cache on cache miss.
//
// Writes are queued and sent asynchronously to the backend. A backpressure
// mechanism blocks new writes when unflushed data exceeds maxUnflushedBytes.
// On Flush/Release, the queue is drained, the server is asked to fsync
// and compute SHA256, and the local cache is updated if writes were sequential.
type RWFile struct {
	client *DirectFSClient
	fh     unix.FileHandle
	fd     uint32
	attr   fileserver.FileAttr

	// Cache-backed read
	mu                sync.RWMutex
	cachedBackingFile *fs.LoopbackFile // Non-nil when reading from local cache
	cacheWriter       CacheWriter      // Active during eager fetch
	fetchDone         chan struct{}    // Closed when eager fetch completes

	// Write state managed through DirectFSClient global write queue
	isWrite       bool
	writeErr      atomic.Value // *error — first async error
	pendingWG     sync.WaitGroup
	lastWriteReq  uint64
	flushInFlight uint32

	// Sequential write tracking for cache copy
	nextSeqOffset int64
	seqEnabled    bool
	seqWriter     CacheWriter
	seqHashHex    string

	// Track which lock owners/flag modes were set through this FD so we can
	// explicitly release them from the client side on close.
	fdLocks map[rwFileLockOwnerKey]struct{}
}

type rwFileLockOwnerKey struct {
	owner uint64
	flags uint32
}

// Ensure interface compliance
var _ fs.FileHandle = (*RWFile)(nil)
var _ fs.FileReader = (*RWFile)(nil)
var _ fs.FileWriter = (*RWFile)(nil)
var _ fs.FileGetlker = (*RWFile)(nil)
var _ fs.FileSetlker = (*RWFile)(nil)
var _ fs.FileSetlkwer = (*RWFile)(nil)
var _ fs.FileFlusher = (*RWFile)(nil)
var _ fs.FileReleaser = (*RWFile)(nil)
var _ fs.FileSetattrer = (*RWFile)(nil)

func newRWFile(client *DirectFSClient, fh unix.FileHandle, fd uint32, attr fileserver.FileAttr, isWrite bool) *RWFile {
	f := &RWFile{
		client:        client,
		fh:            fh,
		fd:            fd,
		attr:          attr,
		isWrite:       isWrite,
		fetchDone:     make(chan struct{}),
		nextSeqOffset: 0,
		seqEnabled:    isWrite,
		fdLocks:       make(map[rwFileLockOwnerKey]struct{}),
	}

	return f
}

// setCachedBacking sets the cached file for passthrough reads (call before any I/O)
func (f *RWFile) setCachedBacking(lf *fs.LoopbackFile) {
	f.mu.Lock()
	f.cachedBackingFile = lf
	f.mu.Unlock()
	// Mark eager fetch as done since we already have a cache hit
	select {
	case <-f.fetchDone:
	default:
		close(f.fetchDone)
	}
}

// ---- Read path ----

// Read serves data from cache or falls back to the server
func (f *RWFile) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	cached := f.cachedBackingFile
	writer := f.cacheWriter

	log.Printf("Read requested: offset=%d size=%d cached=%v writer=%v", off, len(dest), cached != nil, writer != nil)
	if cached != nil {
		return cached.Read(ctx, dest, off)
	}

	// If eager fetch is in progress, try reading from its writer
	if writer != nil {
		if ra, ok := writer.(io.ReaderAt); ok {
			n, err := ra.ReadAt(dest, off)
			if err == nil && n == len(dest) {
				return fuse.ReadResultData(dest[:n]), fs.OK
			}
		}
	}

	// Fallback: read from server
	if f.fd != 0 {
		readReq := fileserver.ReadFdRequest{Fd: f.fd, Offset: uint64(off), Size: uint32(len(dest))}
		readResp := fileserver.ReadResponse{}
		if err := f.client.SendRequest(&readReq, &readResp); err != nil {
			return nil, fs.ToErrno(err)
		}
		return fuse.ReadResultData(readResp.Data), fs.OK
	}

	readReq := fileserver.ReadRequest{Fh: f.fh, Offset: uint64(off), Size: uint32(len(dest))}
	readResp := fileserver.ReadResponse{}
	if err := f.client.SendRequest(&readReq, &readResp); err != nil {
		return nil, fs.ToErrno(err)
	}
	return fuse.ReadResultData(readResp.Data), fs.OK
}

func (f *RWFile) eagerFetch() {
	f.client.submitRWEagerFetch(f)
}

// performEagerFetch downloads the entire file in background and populates the cache.
// This method is executed by the shared eager-fetch worker pool on DirectFSClient.
func (f *RWFile) performEagerFetch() {
	defer func() {
		select {
		case <-f.fetchDone:
		default:
			close(f.fetchDone)
		}
	}()

	var zeroHash [32]byte
	sha256Hex := fmt.Sprintf("%x", f.attr.Sha256[:])
	hasKey := f.attr.Sha256 != zeroHash

	writer, err := f.client.cache.CreateTemp()
	if err != nil {
		return
	}

	f.mu.Lock()
	f.cacheWriter = writer
	f.mu.Unlock()

	if hasKey && len(f.client.cacheSourceClients) > 0 {
		if f.tryFetchFromCacheSources(sha256Hex, writer) {
			return
		}
	}

	abort := func() {
		writer.Abort()
		f.mu.Lock()
		f.cacheWriter = nil
		f.mu.Unlock()
	}

	var offset uint64
	for offset < uint64(f.attr.Size) {
		remaining := uint64(f.attr.Size) - offset
		readSize := uint32(rwEagerFetchChunk)
		if remaining < uint64(rwEagerFetchChunk) {
			readSize = uint32(remaining)
		}

		readReq := fileserver.ReadRequest{
			Fh:     f.fh,
			Offset: offset,
			Size:   readSize,
		}
		readResp := fileserver.ReadResponse{}
		if err := f.client.SendRequest(&readReq, &readResp); err != nil {
			abort()
			return
		}

		if _, err := writer.Write(readResp.Data); err != nil {
			abort()
			return
		}

		offset += uint64(len(readResp.Data))
		if len(readResp.Data) < int(readSize) {
			break
		}
	}

	f.mu.Lock()
	f.cacheWriter = nil
	f.mu.Unlock()

	computedSHA, err := writer.Commit()
	if err != nil {
		return
	}

	if hasKey && computedSHA != sha256Hex {
		log.Printf("RWFile eager fetch: hash mismatch expected=%s got=%s", sha256Hex, computedSHA)
		return
	}

	cachedPath, err := f.client.cache.Lookup(computedSHA)
	if err != nil || cachedPath == "" {
		return
	}

	fd, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
	if err != nil {
		return
	}

	f.mu.Lock()
	log.Printf("RWFile eager fetch: cache saved with hash %s at path %s", computedSHA, cachedPath)
	f.cachedBackingFile = fs.NewLoopbackFile(fd).(*fs.LoopbackFile)
	f.mu.Unlock()

	if GlobalCacheMetrics != nil {
		GlobalCacheMetrics.CacheSaved.Inc()
	}
}

// ---- Write path ----

// Write queues data to be written asynchronously to the backend.
// Blocks if unflushed bytes exceed maxUnflushedBytes (backpressure).
func (f *RWFile) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if !f.isWrite {
		return 0, syscall.EBADF
	}
	if atomic.LoadUint32(&f.flushInFlight) != 0 {
		return 0, syscall.EBUSY
	}

	// Check for prior async write error
	if errVal := f.writeErr.Load(); errVal != nil {
		return 0, fs.ToErrno(*(errVal.(*error)))
	}

	// Track sequential writes and build a local cache copy for sequential streams.
	f.mu.Lock()
	if f.seqEnabled {
		if off != f.nextSeqOffset {
			f.seqEnabled = false
			if f.seqWriter != nil {
				_ = f.seqWriter.Abort()
				if GlobalCacheMetrics != nil {
					GlobalCacheMetrics.CacheAborted.Inc()
				}
				f.seqWriter = nil
			}
		} else {
			if f.seqWriter == nil {
				writer, err := f.client.cache.CreateTemp()
				if err == nil {
					f.seqWriter = writer
				} else {
					f.seqEnabled = false
				}
			}
			if f.seqWriter != nil {
				if _, err := f.seqWriter.Write(data); err != nil {
					_ = f.seqWriter.Abort()
					f.seqWriter = nil
					f.seqEnabled = false
				}
			}
			f.nextSeqOffset = off + int64(len(data))
		}
	}
	f.mu.Unlock()

	// Make a copy for the async queue
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	var req fileserver.Serializable
	if f.fd != 0 {
		req = &fileserver.WriteFdRequest{Fd: f.fd, Offset: uint64(off), Data: dataCopy}
	} else {
		req = &fileserver.WriteRequest{Fh: f.fh, Offset: uint64(off), Data: dataCopy}
	}
	resp := &fileserver.WriteResponse{}

	f.pendingWG.Add(1)
	cb := func(err error) {
		defer f.pendingWG.Done()
		if err != nil {
			f.writeErr.CompareAndSwap(nil, &err)
			return
		}
		if resp.Size != uint32(len(dataCopy)) {
			err := fmt.Errorf("short write from server: got=%d want=%d", resp.Size, len(dataCopy))
			f.writeErr.CompareAndSwap(nil, &err)
		}
	}
	f.lastWriteReq = f.client.enqueueAsyncOp(int64(len(dataCopy)), req, resp, cb)

	return uint32(len(data)), fs.OK
}

// Flush drains the write queue, asks server to fsync, and updates cache if sequential
func (f *RWFile) Flush(ctx context.Context) syscall.Errno {
	if !f.isWrite {
		return fs.OK
	}
	if !atomic.CompareAndSwapUint32(&f.flushInFlight, 0, 1) {
		return fs.OK
	}
	defer atomic.StoreUint32(&f.flushInFlight, 0)

	var clientHash [32]byte
	hasHash := uint8(0)

	f.mu.Lock()
	if f.seqEnabled && f.seqWriter != nil {
		hashHex, err := f.seqWriter.Commit()
		if err == nil && len(hashHex) == 64 {
			decoded, decErr := hex.DecodeString(hashHex)
			if decErr == nil && len(decoded) == 32 {
				copy(clientHash[:], decoded)
				hasHash = 1
				f.seqHashHex = hashHex
			}
		}
		f.seqWriter = nil
	} else if f.seqWriter != nil {
		_ = f.seqWriter.Abort()
		f.seqWriter = nil
	}
	f.mu.Unlock()

	var flushReq fileserver.Serializable
	if f.fd != 0 {
		flushReq = &fileserver.FlushFdRequest{Fd: f.fd, LastWriteReqID: f.lastWriteReq, HasSha256: hasHash, Sha256: clientHash}
	} else {
		flushReq = &fileserver.FlushRequest{Fh: f.fh, LastWriteReqID: f.lastWriteReq, HasSha256: hasHash, Sha256: clientHash}
	}
	flushResp := &fileserver.FlushResponse{}

	f.pendingWG.Add(1)
	cb := func(err error) {
		defer f.pendingWG.Done()
		if err != nil {
			f.writeErr.CompareAndSwap(nil, &err)
			return
		}
		f.mu.Lock()
		f.attr = flushResp.Attr
		f.mu.Unlock()
	}
	f.lastWriteReq = f.client.enqueueAsyncOp(0, flushReq, flushResp, cb)

	log.Printf("Flush requested: lastWriteReq=%d hasHash=%d clientHash=%s", f.lastWriteReq, hasHash, f.seqHashHex)
	// Wait for all pending write/flush callbacks to complete.
	f.pendingWG.Wait()

	// Check for async errors
	if errVal := f.writeErr.Load(); errVal != nil {
		return fs.ToErrno(*(errVal.(*error)))
	}

	return fs.OK
}

// Setattr on open file (synchronous)
func (f *RWFile) Setattr(ctx context.Context, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	req := fileserver.SetattrRequest{Fh: f.fh}

	if m, ok := in.GetMode(); ok {
		req.Valid |= fileserver.SetattrMode
		req.Mode = m
	}
	if uid, ok := in.GetUID(); ok {
		req.Valid |= fileserver.SetattrUid
		req.Uid = uid
	}
	if gid, ok := in.GetGID(); ok {
		req.Valid |= fileserver.SetattrGid
		req.Gid = gid
	}
	if sz, ok := in.GetSize(); ok {
		req.Valid |= fileserver.SetattrSize
		req.Size = int64(sz)
	}

	var resp fileserver.SetattrResponse
	if err := f.client.SendRequest(&req, &resp); err != nil {
		return fs.ToErrno(err)
	}

	toFuseAttr(&resp.Attr, &out.Attr)
	return fs.OK
}

func (f *RWFile) Getlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32, out *fuse.FileLock) syscall.Errno {
	req := fileserver.LockRequest{
		Fd:    f.fd,
		Owner: owner,
		Lk:    *lk,
		Flags: flags,
	}
	var resp fileserver.GetlkResponse
	if err := f.client.sendRequestWithData(fileserver.OpGetlk, fileserver.FlagNone, &req, &resp); err != nil {
		return fs.ToErrno(err)
	}
	*out = resp.Lk
	return fs.OK
}

func (f *RWFile) Setlk(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) syscall.Errno {
	req := fileserver.LockRequest{
		Fd:    f.fd,
		Owner: owner,
		Lk:    *lk,
		Flags: flags,
	}
	if err := f.client.sendRequestWithData(fileserver.OpSetlk, fileserver.FlagNone, &req, &fileserver.EmptyResponse{}); err != nil {
		return fs.ToErrno(err)
	}
	f.recordFDLock(owner, lk.Typ, flags)
	return fs.OK
}

func (f *RWFile) Setlkw(ctx context.Context, owner uint64, lk *fuse.FileLock, flags uint32) syscall.Errno {
	req := fileserver.LockRequest{
		Fd:    f.fd,
		Owner: owner,
		Lk:    *lk,
		Flags: flags,
	}
	if err := f.client.sendRequestWithData(fileserver.OpSetlkw, fileserver.FlagNone, &req, &fileserver.EmptyResponse{}); err != nil {
		return fs.ToErrno(err)
	}
	f.recordFDLock(owner, lk.Typ, flags)
	return fs.OK
}

func (f *RWFile) recordFDLock(owner uint64, typ uint32, flags uint32) {
	key := rwFileLockOwnerKey{owner: owner, flags: flags}
	f.mu.Lock()
	defer f.mu.Unlock()
	if typ == syscall.F_UNLCK {
		delete(f.fdLocks, key)
		return
	}
	f.fdLocks[key] = struct{}{}
}

func (f *RWFile) drainFDLocks() []rwFileLockOwnerKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]rwFileLockOwnerKey, 0, len(f.fdLocks))
	for key := range f.fdLocks {
		out = append(out, key)
	}
	f.fdLocks = make(map[rwFileLockOwnerKey]struct{})
	return out
}

func (f *RWFile) unlockAllFDLocks() {
	keys := f.drainFDLocks()
	if len(keys) == 0 {
		return
	}

	for _, key := range keys {
		unlockReq := fileserver.LockRequest{
			Fd:    f.fd,
			Owner: key.owner,
			Lk: fuse.FileLock{
				Start: 0,
				End:   ^uint64(0),
				Typ:   syscall.F_UNLCK,
				Pid:   0,
			},
			Flags: key.flags,
		}
		if err := f.client.sendRequestWithData(fileserver.OpSetlk, fileserver.FlagNone, &unlockReq, &fileserver.EmptyResponse{}); err != nil {
			log.Printf("RWFile Release: failed to unlock fh (type=%d, size=%d) owner=%d flags=%d: %v", f.fh.Type(), f.fh.Size(), key.owner, key.flags, err)
		}
	}
}

// Release closes the file handle
func (f *RWFile) Release(ctx context.Context) syscall.Errno {
	if f.isWrite {
		_ = f.Flush(ctx)
	}
	f.unlockAllFDLocks()
	if f.fd != 0 {
		releaseReq := fileserver.ReleaseFdRequest{Fd: f.fd}
		if err := f.client.SendRequest(&releaseReq, &fileserver.EmptyResponse{}); err != nil {
			log.Printf("RWFile Release: failed to release fd token %d: %v", f.fd, err)
		}
		f.fd = 0
	}

	f.mu.Lock()
	cached := f.cachedBackingFile
	f.cachedBackingFile = nil
	f.mu.Unlock()

	if cached != nil {
		cached.Release(ctx)
	}

	return fs.OK
}

// PassthroughFd returns the cache file descriptor for kernel passthrough if available
func (f *RWFile) PassthroughFd() (int, bool) {
	f.mu.Lock()
	cached := f.cachedBackingFile
	f.mu.Unlock()
	if cached != nil {
		return cached.PassthroughFd()
	}
	return -1, false
}

var _ fs.FilePassthroughFder = (*RWFile)(nil)

// tryFetchFromCacheSources attempts to fetch file from configured cache sources.
func (f *RWFile) tryFetchFromCacheSources(hash string, writer CacheWriter) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	for _, source := range f.client.cacheSourceClients {
		reader, size, err := source.Fetch(ctx, f.attr.Size, hash)
		if err != nil {
			continue
		}

		written, copyErr := io.Copy(writer, reader)
		_ = reader.Close()
		if copyErr != nil {
			_ = writer.Abort()
			continue
		}

		if size > 0 && written != size {
			_ = writer.Abort()
			continue
		}

		computedHash, err := writer.Commit()
		if err != nil || computedHash != hash {
			continue
		}

		cachedPath, err := f.client.cache.Lookup(computedHash)
		if err != nil || cachedPath == "" {
			continue
		}
		fd, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
		if err != nil {
			continue
		}
		f.mu.Lock()
		log.Printf("Cache source hit for hash %s at path %s", hash, cachedPath)
		f.cachedBackingFile = fs.NewLoopbackFile(fd).(*fs.LoopbackFile)
		f.cacheWriter = nil
		f.mu.Unlock()
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.CacheSaved.Inc()
		}
		return true
	}

	return false
}

var _ fs.FileFsyncer = (*RWFile)(nil)

func (f *RWFile) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	f.pendingWG.Wait()
	return fs.OK
}
