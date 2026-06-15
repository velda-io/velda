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
	"fmt"
	"log"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/fileserver"
)

// RWNode implements a read-write FUSE node backed by the directfs protocol.
// Reads use SHA256-based content-addressable cache (like SnapshotNode).
// Writes are forwarded to the backend server through the protocol.
// Attribute caching uses FUSE entry/attr timeouts (NFS-like ac semantics).
type RWNode struct {
	fs.Inode

	client *DirectFSClient
	fh     unix.FileHandle      // Server-side file handle
	attr   *fileserver.FileAttr // Cached attributes

	mu sync.Mutex // Protects attr updates

	// Directory prefetch state
	dirFetched     sync.Once
	dirFetchError  error
	prefetchedData uint32

	// Symlink cache
	symlinkFetched sync.Once
	symlinkTarget  []byte
	symlinkError   error
}

// Ensure interface compliance
var _ fs.InodeEmbedder = (*RWNode)(nil)
var _ fs.NodeGetattrer = (*RWNode)(nil)
var _ fs.NodeSetattrer = (*RWNode)(nil)
var _ fs.NodeLookuper = (*RWNode)(nil)
var _ fs.NodeReaddirer = (*RWNode)(nil)
var _ fs.NodeOpener = (*RWNode)(nil)
var _ fs.NodeCreater = (*RWNode)(nil)
var _ fs.NodeMkdirer = (*RWNode)(nil)
var _ fs.NodeUnlinker = (*RWNode)(nil)
var _ fs.NodeRmdirer = (*RWNode)(nil)
var _ fs.NodeRenamer = (*RWNode)(nil)
var _ fs.NodeReadlinker = (*RWNode)(nil)
var _ fs.NodeSymlinker = (*RWNode)(nil)
var _ fs.NodeLinker = (*RWNode)(nil)

// Getattr fetches fresh attributes from the server for every kernel call.
// The attr timeout is set to zero so the kernel always re-validates.
func (n *RWNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	req := fileserver.GetattrRequest{Fh: n.fh}
	var resp fileserver.GetattrResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return fs.ToErrno(err)
	}

	n.mu.Lock()
	n.attr = &resp.Attr
	n.mu.Unlock()

	toFuseAttr(&resp.Attr, &out.Attr)
	return fs.OK
}

// Setattr changes file attributes via the server (synchronous)
func (n *RWNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	req := fileserver.SetattrRequest{Fh: n.fh}

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
	if mtime, ok := in.GetMTime(); ok {
		req.Valid |= fileserver.SetattrMtime
		req.Mtime = mtime.Unix()
	}
	if atime, ok := in.GetATime(); ok {
		req.Valid |= fileserver.SetattrAtime
		req.Atime = atime.Unix()
	}

	var resp fileserver.SetattrResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return fs.ToErrno(err)
	}

	n.mu.Lock()
	n.attr = &resp.Attr
	n.mu.Unlock()

	toFuseAttr(&resp.Attr, &out.Attr)
	return fs.OK
}

// fetchDirIfNeeded prefetches directory entries on first call
func (n *RWNode) fetchDirIfNeeded(ctx context.Context) {
	n.dirFetched.Do(func() {
		if GlobalCacheMetrics != nil {
			GlobalCacheMetrics.ReadDirFromClient.Inc()
		}

		readDirReq := fileserver.ReadDirRequest{Fh: n.fh}
		readDirResp := fileserver.ReadDirResponse{}
		if err := n.client.SendRequest(&readDirReq, &readDirResp); err != nil {
			n.dirFetchError = err
			return
		}

		for _, entry := range readDirResp.Entries {
			childNode := &RWNode{
				client: n.client,
				fh:     entry.Fh,
				attr:   &entry.Attr,
			}
			stable := fs.StableAttr{
				Mode: entry.Attr.Mode,
				Ino:  entry.Attr.Ino,
			}
			child := n.NewPersistentInode(ctx, childNode, stable)
			n.client.registerInodeHandler(entry.Attr.Ino, childNode)
			n.AddChild(entry.Name, child, false)
		}
	})
}

// HandleDirDataNotification applies pushed directory entries.
func (n *RWNode) HandleDirDataNotification(notification *fileserver.DirDataNotification) {
	n.dirFetched.Do(func() {
		ctx := context.Background()
		for _, entry := range notification.Entries {
			if n.GetChild(entry.Name) != nil {
				continue
			}
			childNode := &RWNode{
				client: n.client,
				fh:     entry.Fh,
				attr:   &entry.Attr,
			}
			stable := fs.StableAttr{
				Mode: entry.Attr.Mode,
				Ino:  entry.Attr.Ino,
			}
			childInode := n.NewPersistentInode(ctx, childNode, stable)
			n.client.registerInodeHandler(entry.Attr.Ino, childNode)
			n.AddChild(entry.Name, childInode, false)
		}
	})
}

// Lookup resolves a child name
func (n *RWNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// For RW mode, always go to server for lookup to get fresh attributes
	lookupReq := fileserver.LookupRequest{
		ParentFh: n.fh,
		Name:     name,
	}
	lookupResp := fileserver.LookupResponse{}
	if err := n.client.SendRequest(&lookupReq, &lookupResp); err != nil {
		return nil, fs.ToErrno(err)
	}

	childNode := &RWNode{
		client: n.client,
		fh:     lookupResp.Fh,
		attr:   &lookupResp.Attr,
	}

	toFuseAttr(&lookupResp.Attr, &out.Attr)

	stable := fs.StableAttr{
		Mode: lookupResp.Attr.Mode,
		Ino:  lookupResp.Attr.Ino,
	}
	child := n.NewPersistentInode(ctx, childNode, stable)
	n.client.registerInodeHandler(lookupResp.Attr.Ino, childNode)

	return child, fs.OK
}

// Readdir reads directory entries
func (n *RWNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Always fetch fresh for RW mode
	readDirReq := fileserver.ReadDirRequest{Fh: n.fh}
	readDirResp := fileserver.ReadDirResponse{}
	if err := n.client.SendRequest(&readDirReq, &readDirResp); err != nil {
		return nil, fs.ToErrno(err)
	}

	entries := make([]fuse.DirEntry, 0, len(readDirResp.Entries))
	for _, entry := range readDirResp.Entries {
		entries = append(entries, fuse.DirEntry{
			Name: entry.Name,
			Ino:  entry.Attr.Ino,
			Mode: entry.Attr.Mode & unix.S_IFMT,
		})
	}
	return fs.NewListDirStream(entries), fs.OK
}

// Create creates a new file (synchronous metadata, returns RW file handle)
func (n *RWNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	createReq := fileserver.CreateRequest{
		ParentFh: n.fh,
		Name:     name,
		Flags:    flags,
		Mode:     mode,
	}
	if caller, ok := fuse.FromContext(ctx); ok {
		createReq.HasOwner = 1
		createReq.Uid = caller.Uid
		createReq.Gid = caller.Gid
	}
	createResp := fileserver.CreateResponse{}
	if err := n.client.SendRequest(&createReq, &createResp); err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	childNode := &RWNode{
		client: n.client,
		fh:     createResp.Fh,
		attr:   &createResp.Attr,
	}

	toFuseAttr(&createResp.Attr, &out.Attr)

	stable := fs.StableAttr{
		Mode: createResp.Attr.Mode,
		Ino:  createResp.Attr.Ino,
	}
	child := n.NewPersistentInode(ctx, childNode, stable)

	file := newRWFile(n.client, createResp.Fh, createResp.Fd, createResp.Attr, true)

	return child, file, 0, fs.OK
}

// Open opens a file for reading or writing
func (n *RWNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	req := fileserver.OpenRequest{Fh: n.fh, Flags: flags}
	var resp fileserver.OpenResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return nil, 0, fs.ToErrno(err)
	}

	n.mu.Lock()
	n.attr = &resp.Attr
	attr := n.attr
	n.mu.Unlock()

	if attr.Mode&unix.S_IFMT == unix.S_IFDIR {
		return nil, 0, syscall.EISDIR
	}

	isWrite := (flags&unix.O_WRONLY != 0) || (flags&unix.O_RDWR != 0)
	file := newRWFile(n.client, n.fh, resp.Fd, *attr, isWrite)
	// For read-only opens with a valid cache key, try to open from cache
	if isWrite {
		n.mu.Lock()
		n.attr.Sha256 = [32]byte{}
		n.mu.Unlock()
	} else {
		var zeroHash [32]byte
		if attr.Sha256 != zeroHash {
			log.Printf("File has hash: %x\n", attr.Sha256)
			sha256Hex := fmt.Sprintf("%x", attr.Sha256[:])
			if cachedPath, err := n.client.cache.Lookup(sha256Hex); err == nil && cachedPath != "" {
				fd, err := unix.Open(cachedPath, unix.O_RDONLY, 0)
				if err == nil {
					if GlobalCacheMetrics != nil {
						GlobalCacheMetrics.CacheHit.Inc()
					}
					file.setCachedBacking(fs.NewLoopbackFile(fd).(*fs.LoopbackFile))
					return file, fuse.FOPEN_KEEP_CACHE, fs.OK
				}
			}
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheMiss.Inc()
			}
		} else {
			if GlobalCacheMetrics != nil {
				GlobalCacheMetrics.CacheNotExist.Inc()
			}
		}

		// Start eager fetch in background for reads
		go file.eagerFetch()
	}

	return file, 0, fs.OK
}

// Mkdir creates a new directory (synchronous)
func (n *RWNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	req := fileserver.MkdirRequest{
		ParentFh: n.fh,
		Name:     name,
		Mode:     mode,
	}
	if caller, ok := fuse.FromContext(ctx); ok {
		req.HasOwner = 1
		req.Uid = caller.Uid
		req.Gid = caller.Gid
	}
	var resp fileserver.MkdirResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return nil, fs.ToErrno(err)
	}

	childNode := &RWNode{
		client: n.client,
		fh:     resp.Fh,
		attr:   &resp.Attr,
	}
	toFuseAttr(&resp.Attr, &out.Attr)

	stable := fs.StableAttr{
		Mode: resp.Attr.Mode,
		Ino:  resp.Attr.Ino,
	}
	child := n.NewPersistentInode(ctx, childNode, stable)
	return child, fs.OK
}

// Unlink removes a file (synchronous)
func (n *RWNode) Unlink(ctx context.Context, name string) syscall.Errno {
	req := fileserver.UnlinkRequest{
		ParentFh: n.fh,
		Name:     name,
	}
	var resp fileserver.UnlinkResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return fs.ToErrno(err)
	}
	n.mu.Lock()
	n.attr = nil
	n.mu.Unlock()
	return fs.OK
}

// Rmdir removes a directory (synchronous)
func (n *RWNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	req := fileserver.RmdirRequest{
		ParentFh: n.fh,
		Name:     name,
	}
	var resp fileserver.RmdirResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return fs.ToErrno(err)
	}
	n.mu.Lock()
	n.attr = nil
	n.mu.Unlock()
	return fs.OK
}

// Rename renames a file or directory (synchronous)
func (n *RWNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	newParentNode, ok := newParent.(*RWNode)
	if !ok {
		return syscall.EINVAL
	}

	req := fileserver.RenameRequest{
		OldParentFh: n.fh,
		OldName:     name,
		NewParentFh: newParentNode.fh,
		NewName:     newName,
	}
	var resp fileserver.RenameResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return fs.ToErrno(err)
	}
	// Invalidate cached attrs for both parent directories: mtime/nlink changed.
	n.mu.Lock()
	n.attr = nil
	n.mu.Unlock()
	if newParentNode != n {
		newParentNode.mu.Lock()
		newParentNode.attr = nil
		newParentNode.mu.Unlock()
	}
	return fs.OK
}

// Readlink reads a symlink target
func (n *RWNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	n.symlinkFetched.Do(func() {
		req := fileserver.ReadlinkRequest{Fh: n.fh}
		var resp fileserver.ReadlinkResponse
		if err := n.client.SendRequest(&req, &resp); err != nil {
			n.symlinkError = err
			return
		}
		n.symlinkTarget = []byte(resp.Target)
	})
	if n.symlinkError != nil {
		return nil, fs.ToErrno(n.symlinkError)
	}
	return n.symlinkTarget, fs.OK
}

// Symlink creates a symbolic link (synchronous)
func (n *RWNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	req := fileserver.SymlinkRequest{
		ParentFh: n.fh,
		Name:     name,
		Target:   target,
	}
	if caller, ok := fuse.FromContext(ctx); ok {
		req.HasOwner = 1
		req.Uid = caller.Uid
		req.Gid = caller.Gid
	}
	var resp fileserver.SymlinkResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return nil, fs.ToErrno(err)
	}

	childNode := &RWNode{
		client: n.client,
		fh:     resp.Fh,
		attr:   &resp.Attr,
	}
	toFuseAttr(&resp.Attr, &out.Attr)

	stable := fs.StableAttr{
		Mode: resp.Attr.Mode,
		Ino:  resp.Attr.Ino,
	}
	child := n.NewPersistentInode(ctx, childNode, stable)
	return child, fs.OK
}

// Link creates a hard link (synchronous)
func (n *RWNode) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	targetNode, ok := target.(*RWNode)
	if !ok {
		return nil, syscall.EINVAL
	}

	req := fileserver.LinkRequest{
		ParentFh: n.fh,
		Name:     name,
		TargetFh: targetNode.fh,
	}
	var resp fileserver.LinkResponse
	if err := n.client.SendRequest(&req, &resp); err != nil {
		return nil, fs.ToErrno(err)
	}

	childNode := &RWNode{
		client: n.client,
		fh:     resp.Fh,
		attr:   &resp.Attr,
	}
	toFuseAttr(&resp.Attr, &out.Attr)

	stable := fs.StableAttr{
		Mode: resp.Attr.Mode,
		Ino:  resp.Attr.Ino,
	}
	child := n.NewPersistentInode(ctx, childNode, stable)
	return child, fs.OK
}

var _ fs.NodeFsyncer = (*RWNode)(nil)

func (n *RWNode) Fsync(ctx context.Context, f fs.FileHandle, flags uint32) syscall.Errno {
	if file, ok := f.(*RWFile); ok {
		return file.Fsync(ctx, flags)
	}
	return fs.OK
}
