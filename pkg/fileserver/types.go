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
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

// Protocol operation codes
const (
	OpMount   = 1
	OpLookup  = 2
	OpRead    = 3
	OpReadDir = 4
)

// Protocol version and flags
const (
	ProtocolVersion = 1
	FlagNone        = 0
	FlagReadOnly    = 1 << 0
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

// Serialize writes Header to io.Writer
func (h *Header) Serialize(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, h.Opcode); err != nil {
		return fmt.Errorf("failed to write header opcode: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, h.Size); err != nil {
		return fmt.Errorf("failed to write header size: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, h.Flags); err != nil {
		return fmt.Errorf("failed to write header flags: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, h.Seq); err != nil {
		return fmt.Errorf("failed to write header seq: %w", err)
	}
	return nil
}

// Deserialize reads Header from io.Reader
func (h *Header) Deserialize(r io.Reader) error {
	if err := binary.Read(r, binary.LittleEndian, &h.Opcode); err != nil {
		return fmt.Errorf("failed to read header opcode: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &h.Size); err != nil {
		return fmt.Errorf("failed to read header size: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &h.Flags); err != nil {
		return fmt.Errorf("failed to read header flags: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &h.Seq); err != nil {
		return fmt.Errorf("failed to read header seq: %w", err)
	}
	return nil
}

// MountRequest represents a mount request
type MountRequest struct {
	Version uint32
	Flags   uint32
}

// Serialize writes MountRequest to io.Writer (including header)
func (r *MountRequest) Serialize(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, r.Version); err != nil {
		return fmt.Errorf("failed to write mount version: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Flags); err != nil {
		return fmt.Errorf("failed to write mount flags: %w", err)
	}
	return nil
}

// Deserialize reads MountRequest from io.Reader (header-excluded data)
func (r *MountRequest) Deserialize(reader io.Reader) error {
	if err := binary.Read(reader, binary.LittleEndian, &r.Version); err != nil {
		return fmt.Errorf("failed to read mount version: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Flags); err != nil {
		return fmt.Errorf("failed to read mount flags: %w", err)
	}
	return nil
}

// MountResponse represents a mount response
type MountResponse struct {
	Header  Header
	Version uint32
	Flags   uint32
	Fh      unix.FileHandle // Root file handle
}

// Serialize writes MountResponse to io.Writer (including header)
func (r *MountResponse) Serialize(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, r.Version); err != nil {
		return fmt.Errorf("failed to write version: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Flags); err != nil {
		return fmt.Errorf("failed to write flags: %w", err)
	}
	// Write file handle using helper
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}

	return nil
}

// Deserialize reads MountResponse from io.Reader (header-excluded data)
func (r *MountResponse) Deserialize(reader io.Reader) error {
	if err := binary.Read(reader, binary.LittleEndian, &r.Version); err != nil {
		return fmt.Errorf("failed to read version: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Flags); err != nil {
		return fmt.Errorf("failed to read flags: %w", err)
	}
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	return nil
}

// Mount request/response - raw structs safe for unsafe serialization
// mount raw structs removed; fields are serialized directly
// LookupRequest represents a lookup request
type LookupRequest struct {
	ParentFh unix.FileHandle
	Name     string // File/directory name
}

// Serialize writes LookupRequest to io.Writer (including header)
func (r *LookupRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	nameBytes := []byte(r.Name)
	if err := binary.Write(w, binary.LittleEndian, uint32(len(nameBytes))); err != nil {
		return fmt.Errorf("failed to write name length: %w", err)
	}
	if _, err := w.Write(nameBytes); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	return nil
}

// Deserialize reads LookupRequest from io.Reader (header-excluded data)
func (r *LookupRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh

	var nameLen uint32
	if err := binary.Read(reader, binary.LittleEndian, &nameLen); err != nil {
		return fmt.Errorf("failed to read name length: %w", err)
	}
	nameBytes := make([]byte, nameLen)
	if _, err := io.ReadFull(reader, nameBytes); err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = string(nameBytes)
	return nil
}

// LookupResponse represents a lookup response
type LookupResponse struct {
	// Currently always 0
	Fh   unix.FileHandle // File handle
	Attr FileAttr        // File attributes
}

// Serialize writes LookupResponse to io.Writer (including header)
func (r *LookupResponse) Serialize(w io.Writer) error {
	// Write file handle
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh bytes: %w", err)
	}

	// Write attributes
	if err := binary.Write(w, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to write attributes: %w", err)
	}

	return nil
}

// Deserialize reads LookupResponse from io.Reader (header-excluded data)
func (r *LookupResponse) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh

	if err := binary.Read(reader, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to read attributes: %w", err)
	}
	return nil
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

// Serialize writes ReadRequest to io.Writer (including header)
func (r *ReadRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Offset); err != nil {
		return fmt.Errorf("failed to write offset: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Size); err != nil {
		return fmt.Errorf("failed to write size: %w", err)
	}

	return nil
}

// Deserialize reads ReadRequest from io.Reader (header-excluded data)
func (r *ReadRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh

	if err := binary.Read(reader, binary.LittleEndian, &r.Offset); err != nil {
		return fmt.Errorf("failed to read offset: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Size); err != nil {
		return fmt.Errorf("failed to read size: %w", err)
	}

	return nil
}

// ReadResponse represents a read response
type ReadResponse struct {
	Data []byte // File data
}

// Serialize writes ReadResponse to io.Writer (including header)
func (r *ReadResponse) Serialize(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(r.Data))); err != nil {
		return fmt.Errorf("failed to write count: %w", err)
	}

	if _, err := w.Write(r.Data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	return nil
}

// Deserialize reads ReadResponse from io.Reader (header-excluded data)
func (r *ReadResponse) Deserialize(reader io.Reader) error {
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return fmt.Errorf("failed to read count: %w", err)
	}

	r.Data = make([]byte, int(count))
	if count > 0 {
		if _, err := io.ReadFull(reader, r.Data); err != nil {
			return fmt.Errorf("failed to read data: %w", err)
		}
	}
	return nil
}

// Read request/response - raw structs for wire protocol
// readResponseRaw removed; fields serialized directly
// ReadDirRequest represents a readdir request
type ReadDirRequest struct {
	Fh     unix.FileHandle // Directory file handle
	Offset uint32          // Offset in directory listing
	Count  uint32          // Maximum number of entries to return
}

// Serialize writes ReadDirRequest to io.Writer (including header)
func (r *ReadDirRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Offset); err != nil {
		return fmt.Errorf("failed to write offset: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Count); err != nil {
		return fmt.Errorf("failed to write count: %w", err)
	}

	return nil
}

// Deserialize reads ReadDirRequest from io.Reader (header-excluded data)
func (r *ReadDirRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh

	if err := binary.Read(reader, binary.LittleEndian, &r.Offset); err != nil {
		return fmt.Errorf("failed to read offset: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Count); err != nil {
		return fmt.Errorf("failed to read count: %w", err)
	}

	return nil
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

// Serialize writes ReadDirResponse to io.Writer (including header)
func (r *ReadDirResponse) Serialize(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(r.Entries))); err != nil {
		return fmt.Errorf("failed to write readdir count: %w", err)
	}

	// Write entries
	for _, entry := range r.Entries {
		nameBytes := []byte(entry.Name)

		if _, err := w.Write(encodeFileHandle(entry.Fh)); err != nil {
			return fmt.Errorf("failed to write entry fh: %w", err)
		}
		if err := binary.Write(w, binary.LittleEndian, uint32(len(nameBytes))); err != nil {
			return fmt.Errorf("failed to write entry name length: %w", err)
		}
		if _, err := w.Write(nameBytes); err != nil {
			return fmt.Errorf("failed to write entry name: %w", err)
		}
		if err := binary.Write(w, binary.LittleEndian, &entry.Attr); err != nil {
			return fmt.Errorf("failed to write entry attr: %w", err)
		}
	}

	return nil
}

// Deserialize reads ReadDirResponse from io.Reader (header-excluded data)
func (r *ReadDirResponse) Deserialize(reader io.Reader) error {
	var count uint32
	if err := binary.Read(reader, binary.LittleEndian, &count); err != nil {
		return fmt.Errorf("failed to read response count: %w", err)
	}
	r.Entries = make([]DirEntry, 0, int(count))

	for i := 0; i < int(count); i++ {
		entry := DirEntry{}
		fh, err := decodeFileHandle(reader)
		if err != nil {
			return err
		}
		entry.Fh = fh

		var nameLen uint32
		if err := binary.Read(reader, binary.LittleEndian, &nameLen); err != nil {
			return fmt.Errorf("failed to read entry name length: %w", err)
		}
		nameBytes := make([]byte, nameLen)
		if _, err := io.ReadFull(reader, nameBytes); err != nil {
			return fmt.Errorf("failed to read entry name: %w", err)
		}
		entry.Name = string(nameBytes)

		if err := binary.Read(reader, binary.LittleEndian, &entry.Attr); err != nil {
			return fmt.Errorf("failed to read entry attr: %w", err)
		}

		r.Entries = append(r.Entries, entry)
	}

	return nil
}

// ReadDir request/response - raw structs for wire protocol
// readDirResponseRaw removed; fields serialized directly
// Each directory entry in wire format:
// FhLen (4 bytes) + Fh (variable) + NameLen (4 bytes) + Name (variable) + FileAttr (24 bytes)

// encodeFileHandle encodes file handle to wire format (without mount_id, stored in Session)
// Wire format: handleLen (4 bytes) + handleType (4 bytes) + handleBytes
func encodeFileHandle(fh unix.FileHandle) []byte {
	handleBytes := fh.Bytes()
	size := 8 + len(handleBytes)
	buf := make([]byte, size)

	// Use binary.LittleEndian for serialization
	binary.LittleEndian.PutUint32(buf[0:], uint32(len(handleBytes)))
	binary.LittleEndian.PutUint32(buf[4:], uint32(fh.Type()))
	copy(buf[8:], handleBytes)
	return buf
}

// decodeFileHandle decodes file handle from wire format (without mount_id) and returns unix.FileHandle
func decodeFileHandle(r io.Reader) (unix.FileHandle, error) {
	// Use binary.LittleEndian for deserialization
	var handleLen uint32
	if err := binary.Read(r, binary.LittleEndian, &handleLen); err != nil {
		return unix.FileHandle{}, fmt.Errorf("failed to read fh length: %w", err)
	}

	var handleType int32
	if err := binary.Read(r, binary.LittleEndian, &handleType); err != nil {
		return unix.FileHandle{}, fmt.Errorf("failed to read fh type: %w", err)
	}

	handleBytes := make([]byte, handleLen)

	if _, err := io.ReadFull(r, handleBytes); err != nil {
		return unix.FileHandle{}, fmt.Errorf("failed to read fh bytes: %w", err)
	}

	return unix.NewFileHandle(handleType, handleBytes), nil
}
