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

	OpDirDataNotification = 0x101
)

// Protocol version and flags
const (
	ProtocolVersion = 1
	FlagNone        = 0
	FlagQosLow      = 1 << 0 // Low priority QoS for this request
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
