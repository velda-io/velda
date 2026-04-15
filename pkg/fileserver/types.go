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
	"io"

	"golang.org/x/sys/unix"
)

// Protocol operation codes
const (
	OpMount    = 0x1
	OpLookup   = 0x2
	OpRead     = 0x3
	OpReadDir  = 0x4
	OpReadlink = 0x5

	// Write operations (read-write mode)
	OpCreate  = 0x10
	OpWrite   = 0x11
	OpMkdir   = 0x12
	OpUnlink  = 0x13
	OpRmdir   = 0x14
	OpRename  = 0x15
	OpSetattr = 0x16
	OpSymlink = 0x17
	OpLink    = 0x18
	OpFlush   = 0x19
	OpGetattr = 0x1a

	OpDirDataNotification = 0x101
)

// Protocol version and flags
const (
	ProtocolVersion      = 1
	FlagNone             = 0
	FlagQosLow           = 1 << 0 // Low priority QoS for this request
	FlagCompressed       = 1 << 1 // Payload is compressed with zstd
	FlagHasMore          = 1 << 2 // More packets follow for this message (partial packet)
	FlagEndOfMultiPacket = 1 << 3 // This is the final packet of a multi-packet message
)

const (
	// CompressionThreshold is the minimum payload size for compression (32KB)
	CompressionThreshold = 32 * 1024
	// ChunkSize is the maximum size of each packet chunk after compression (32KB)
	ChunkSize = 32 * 1024
)

// Header represents the common header for all requests/responses
type Header struct {
	Opcode uint32 // Operation code, for response this is 0 or errno
	Size   uint32 // Total message size including header
	Flags  uint32 // Flags for future use (currently unused)
	Seq    uint32 // Sequence ID for request/response matching
}

const HeaderSize = 16 // 4 + 4 + 4 + 4

// Serializable represents a type that can write/read its wire representation
type Serializable interface {
	Serialize(io.Writer) error
	Deserialize(io.Reader) error
}

const (
	MountFlagReadOnly = 1 << 0
)

// MountRequest represents a mount request
type MountRequest struct {
	Version uint32
	Flags   uint32
	Path    string // Root path to mount
}

// MountResponse represents a mount response
type MountResponse struct {
	Header  Header
	Version uint32
	Flags   uint32
	Fh      unix.FileHandle // Root file handle
	Attr    FileAttr        // Root file attributes
}

// mount raw structs removed; fields are serialized directly
// LookupRequest represents a lookup request
type LookupRequest struct {
	ParentFh unix.FileHandle
	Name     string // File/directory name
}

// LookupResponse represents a lookup response
type LookupResponse struct {
	// Currently always 0
	Fh   unix.FileHandle // File handle
	Attr FileAttr        // File attributes
}

// Lookup request/response - raw structs for wire protocol
// lookupResponseRaw removed; fields serialized directly

// FileAttr - raw struct safe for unsafe serialization (no pointers)
// Matches unix.Stat_t structure
type FileAttr struct {
	Dev      uint64   // Device ID
	Ino      uint64   // Inode number
	Nlink    uint64   // Number of hard links
	Mode     uint32   // File mode
	Uid      uint32   // User ID
	Gid      uint32   // Group ID
	Rdev     uint64   // Device ID (if special file)
	Size     int64    // File size
	Blksize  int64    // Block size for filesystem I/O
	Blocks   int64    // Number of 512B blocks allocated
	Mtim     int64    // Modification time (unix timestamp seconds)
	Ctim     int64    // Change time (unix timestamp seconds)
	MtimNsec int32    // Modification time nanoseconds
	CtimNsec int32    // Change time nanoseconds
	Sha256   [32]byte // SHA256 hash of file content (all zeros if not available)
}

const FileAttrSize = 160 // unsafe.Sizeof(FileAttr{})

// ReadRequest represents a read request
type ReadRequest struct {
	Fh     unix.FileHandle // File handle
	Offset uint64          // Offset in file
	Size   uint32          // Number of bytes to read
}

// ReadResponse represents a read response
type ReadResponse struct {
	Data []byte // File data
}

// Read request/response - raw structs for wire protocol
// readResponseRaw removed; fields serialized directly
// ReadDirRequest represents a readdir request
type ReadDirRequest struct {
	Fh unix.FileHandle // Directory file handle
}

// DirEntry represents a directory entry
type DirEntry struct {
	Fh   unix.FileHandle // File handle
	Name string          // Entry name
	Attr FileAttr        // File attributes
}

// ReadDirResponse represents a readdir response
type ReadDirResponse struct {
	Entries []DirEntry
}

type DirDataNotification struct {
	Ino     uint64 // Inode number of the file/directory
	Entries []DirEntry
}

// PreLoadItem represents an item in the pre-loading queue
type PreLoadItem struct {
	Fh      unix.FileHandle // Directory file handle
	Ino     uint64          // Inode number
	Session *Session        // Session to send data to
}

// ReadlinkRequest represents a readlink request
type ReadlinkRequest struct {
	Fh unix.FileHandle // Symlink file handle
}

// ReadlinkResponse represents a readlink response
type ReadlinkResponse struct {
	Target string // Symlink target path
}

// --- Write operation types ---

// MountFlagReadWrite indicates the client wants read-write access
const MountFlagReadWrite = 1 << 1

// CreateRequest creates a new file in a directory
type CreateRequest struct {
	ParentFh unix.FileHandle
	Name     string
	Flags    uint32 // O_WRONLY, O_RDWR, etc.
	Mode     uint32 // File permission bits
}

// CreateResponse returns the new file's handle and attributes
type CreateResponse struct {
	Fh   unix.FileHandle
	Attr FileAttr
}

// WriteRequest writes data to a file at a given offset
type WriteRequest struct {
	Fh     unix.FileHandle
	Offset uint64
	Data   []byte
}

// WriteResponse returns the number of bytes written
type WriteResponse struct {
	Size uint32
}

// MkdirRequest creates a new directory
type MkdirRequest struct {
	ParentFh unix.FileHandle
	Name     string
	Mode     uint32
}

// MkdirResponse returns the new directory's handle and attributes
type MkdirResponse struct {
	Fh   unix.FileHandle
	Attr FileAttr
}

// UnlinkRequest removes a file from a directory
type UnlinkRequest struct {
	ParentFh unix.FileHandle
	Name     string
}

// UnlinkResponse is an empty success response
type UnlinkResponse struct{}

// RmdirRequest removes a directory
type RmdirRequest struct {
	ParentFh unix.FileHandle
	Name     string
}

// RmdirResponse is an empty success response
type RmdirResponse struct{}

// RenameRequest renames/moves a file or directory
type RenameRequest struct {
	OldParentFh unix.FileHandle
	OldName     string
	NewParentFh unix.FileHandle
	NewName     string
}

// RenameResponse is an empty success response
type RenameResponse struct{}

// SetattrRequest changes file attributes
type SetattrRequest struct {
	Fh    unix.FileHandle
	Valid uint32 // Bitmask of which fields to set
	Mode  uint32
	Uid   uint32
	Gid   uint32
	Size  int64 // For truncation
	Mtime int64 // Modification time seconds
	Atime int64 // Access time seconds
}

// Bitmask constants for SetattrRequest.Valid
const (
	SetattrMode  = 1 << 0
	SetattrUid   = 1 << 1
	SetattrGid   = 1 << 2
	SetattrSize  = 1 << 3
	SetattrMtime = 1 << 4
	SetattrAtime = 1 << 5
)

// SetattrResponse returns updated attributes
type SetattrResponse struct {
	Attr FileAttr
}

// SymlinkRequest creates a symbolic link
type SymlinkRequest struct {
	ParentFh unix.FileHandle
	Name     string
	Target   string
}

// SymlinkResponse returns the symlink's handle and attributes
type SymlinkResponse struct {
	Fh   unix.FileHandle
	Attr FileAttr
}

// LinkRequest creates a hard link
type LinkRequest struct {
	ParentFh unix.FileHandle
	Name     string
	TargetFh unix.FileHandle
}

// LinkResponse returns the new link's attributes
type LinkResponse struct {
	Fh   unix.FileHandle
	Attr FileAttr
}

// FlushRequest flushes pending writes for a file and returns updated attributes
type FlushRequest struct {
	Fh unix.FileHandle
	// Client-side write queue barrier identifier.
	LastWriteReqID uint64
	// HasSha256 indicates whether Sha256 is valid.
	HasSha256 uint8
	// Optional client-computed SHA256 for sequential write path.
	Sha256 [32]byte
}

// FlushResponse returns updated attributes after flush (with SHA256 if available)
type FlushResponse struct {
	Attr FileAttr
}

// GetattrRequest fetches current attributes for a file handle
type GetattrRequest struct {
	Fh unix.FileHandle
}

// GetattrResponse returns current file attributes
type GetattrResponse struct {
	Attr FileAttr
}
