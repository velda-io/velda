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
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/sys/unix"
)

const (
	// MaxPacketSize is the maximum size of a packet (5MB)
	MaxPacketSize = 5 * 1024 * 1024
	// MaxStringSize is the maximum size of a string field
	MaxStringSize = MaxPacketSize - 100 // 2.5MB to allow for other data
	// MaxArrayCount is the maximum number of entries in arrays
	MaxArrayCount = 100000 // reasonable limit for directory entries
	// MaxFileHandleSize is the maximum size of a file handle
	MaxFileHandleSize = 1024 // file handles are typically small
)

// writeVarint writes a uint64 as a varint
func writeVarint(w io.Writer, v uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	_, err := w.Write(buf[:n])
	return err
}

// readVarint reads a varint and validates it against maxValue
func readVarint(r io.Reader, maxValue uint64) (uint64, error) {
	v, err := binary.ReadUvarint(ioByteReader{r})
	if err != nil {
		return 0, err
	}
	if v > maxValue {
		return 0, fmt.Errorf("varint value %d exceeds maximum %d", v, maxValue)
	}
	return v, nil
}

// ioByteReader wraps io.Reader to implement io.ByteReader
type ioByteReader struct {
	io.Reader
}

func (r ioByteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, err := r.Reader.Read(b[:])
	return b[0], err
}

// writeString writes a string with varint length prefix
func writeString(w io.Writer, s string) error {
	if len(s) > MaxStringSize {
		return fmt.Errorf("string length %d exceeds maximum %d", len(s), MaxStringSize)
	}
	if err := writeVarint(w, uint64(len(s))); err != nil {
		return fmt.Errorf("failed to write string length: %w", err)
	}
	if len(s) > 0 {
		if _, err := w.Write([]byte(s)); err != nil {
			return fmt.Errorf("failed to write string data: %w", err)
		}
	}
	return nil
}

// readString reads a string with varint length prefix and validates size
func readString(r io.Reader) (string, error) {
	length, err := readVarint(r, MaxStringSize)
	if err != nil {
		return "", fmt.Errorf("failed to read string length: %w", err)
	}
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("failed to read string data: %w", err)
	}
	return string(buf), nil
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

// Serialize writes MountRequest to io.Writer (including header)
func (r *MountRequest) Serialize(w io.Writer) error {
	if err := binary.Write(w, binary.LittleEndian, r.Version); err != nil {
		return fmt.Errorf("failed to write mount version: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Flags); err != nil {
		return fmt.Errorf("failed to write mount flags: %w", err)
	}
	// Write path
	if err := writeString(w, r.Path); err != nil {
		return fmt.Errorf("failed to write path: %w", err)
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
	// Read path
	path, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read path: %w", err)
	}
	r.Path = path
	return nil
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
	// Write file attributes
	if err := binary.Write(w, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to write attr: %w", err)
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
	// Read file attributes
	if err := binary.Read(reader, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to read attr: %w", err)
	}
	return nil
}

// Serialize writes LookupRequest to io.Writer (including header)
func (r *LookupRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
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

	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	return nil
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

// Serialize writes ReadResponse to io.Writer (including header)
func (r *ReadResponse) Serialize(w io.Writer) error {
	if len(r.Data) > MaxPacketSize {
		return fmt.Errorf("data size %d exceeds maximum %d", len(r.Data), MaxPacketSize)
	}
	if err := writeVarint(w, uint64(len(r.Data))); err != nil {
		return fmt.Errorf("failed to write count: %w", err)
	}

	if len(r.Data) > 0 {
		if _, err := w.Write(r.Data); err != nil {
			return fmt.Errorf("failed to write data: %w", err)
		}
	}

	return nil
}

// Deserialize reads ReadResponse from io.Reader (header-excluded data)
func (r *ReadResponse) Deserialize(reader io.Reader) error {
	count, err := readVarint(reader, MaxPacketSize)
	if err != nil {
		return fmt.Errorf("failed to read count: %w", err)
	}

	if count > 0 {
		r.Data = make([]byte, count)
		if _, err := io.ReadFull(reader, r.Data); err != nil {
			return fmt.Errorf("failed to read data: %w", err)
		}
	} else {
		r.Data = nil
	}
	return nil
}

// Serialize writes ReadDirRequest to io.Writer (including header)
func (r *ReadDirRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
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
	return nil
}

// Serialize writes ReadDirResponse to io.Writer (including header)
func (r *ReadDirResponse) Serialize(w io.Writer) error {
	if len(r.Entries) > MaxArrayCount {
		return fmt.Errorf("entry count %d exceeds maximum %d", len(r.Entries), MaxArrayCount)
	}
	if err := writeVarint(w, uint64(len(r.Entries))); err != nil {
		return fmt.Errorf("failed to write readdir count: %w", err)
	}

	// Write entries
	for _, entry := range r.Entries {
		if _, err := w.Write(encodeFileHandle(entry.Fh)); err != nil {
			return fmt.Errorf("failed to write entry fh: %w", err)
		}
		if err := writeString(w, entry.Name); err != nil {
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
	count, err := readVarint(reader, MaxArrayCount)
	if err != nil {
		return fmt.Errorf("failed to read response count: %w", err)
	}
	r.Entries = make([]DirEntry, 0, int(count))

	for i := uint64(0); i < count; i++ {
		entry := DirEntry{}
		fh, err := decodeFileHandle(reader)
		if err != nil {
			return err
		}
		entry.Fh = fh

		name, err := readString(reader)
		if err != nil {
			return fmt.Errorf("failed to read entry name: %w", err)
		}
		entry.Name = name

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

	// Validate handle length
	if handleLen > MaxFileHandleSize {
		return unix.FileHandle{}, fmt.Errorf("file handle length %d exceeds maximum %d", handleLen, MaxFileHandleSize)
	}

	var handleType int32
	if err := binary.Read(r, binary.LittleEndian, &handleType); err != nil {
		return unix.FileHandle{}, fmt.Errorf("failed to read fh type: %w", err)
	}

	handleBytes := make([]byte, handleLen)

	if handleLen > 0 {
		if _, err := io.ReadFull(r, handleBytes); err != nil {
			return unix.FileHandle{}, fmt.Errorf("failed to read fh bytes: %w", err)
		}
	}

	return unix.NewFileHandle(handleType, handleBytes), nil
}

// Serialize writes DirDataNotification to io.Writer
func (n *DirDataNotification) Serialize(w io.Writer) error {
	// Write inode number
	if err := binary.Write(w, binary.LittleEndian, n.Ino); err != nil {
		return fmt.Errorf("failed to write ino: %w", err)
	}

	// Write number of entries
	if len(n.Entries) > MaxArrayCount {
		return fmt.Errorf("entry count %d exceeds maximum %d", len(n.Entries), MaxArrayCount)
	}
	if err := writeVarint(w, uint64(len(n.Entries))); err != nil {
		return fmt.Errorf("failed to write entry count: %w", err)
	}

	// Write entries (same format as ReadDirResponse)
	for _, entry := range n.Entries {
		if _, err := w.Write(encodeFileHandle(entry.Fh)); err != nil {
			return fmt.Errorf("failed to write entry fh: %w", err)
		}
		if err := writeString(w, entry.Name); err != nil {
			return fmt.Errorf("failed to write entry name: %w", err)
		}
		if err := binary.Write(w, binary.LittleEndian, &entry.Attr); err != nil {
			return fmt.Errorf("failed to write entry attr: %w", err)
		}
	}

	return nil
}

// Deserialize reads DirDataNotification from io.Reader
func (n *DirDataNotification) Deserialize(reader io.Reader) error {
	// Read inode number
	if err := binary.Read(reader, binary.LittleEndian, &n.Ino); err != nil {
		return fmt.Errorf("failed to read ino: %w", err)
	}

	// Read number of entries
	count, err := readVarint(reader, MaxArrayCount)
	if err != nil {
		return fmt.Errorf("failed to read entry count: %w", err)
	}

	n.Entries = make([]DirEntry, 0, int(count))

	// Read entries (same format as ReadDirResponse)
	for i := uint64(0); i < count; i++ {
		entry := DirEntry{}
		fh, err := decodeFileHandle(reader)
		if err != nil {
			return err
		}
		entry.Fh = fh

		name, err := readString(reader)
		if err != nil {
			return fmt.Errorf("failed to read entry name: %w", err)
		}
		entry.Name = name

		if err := binary.Read(reader, binary.LittleEndian, &entry.Attr); err != nil {
			return fmt.Errorf("failed to read entry attr: %w", err)
		}

		n.Entries = append(n.Entries, entry)
	}

	return nil
}

// Serialize writes ReadlinkRequest to io.Writer
func (r *ReadlinkRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	return nil
}

// Deserialize reads ReadlinkRequest from io.Reader
func (r *ReadlinkRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	return nil
}

// Serialize writes ReadlinkResponse to io.Writer
func (r *ReadlinkResponse) Serialize(w io.Writer) error {
	if err := writeString(w, r.Target); err != nil {
		return fmt.Errorf("failed to write target: %w", err)
	}
	return nil
}

// Deserialize reads ReadlinkResponse from io.Reader
func (r *ReadlinkResponse) Deserialize(reader io.Reader) error {
	target, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read target: %w", err)
	}
	r.Target = target
	return nil
}

// --- Write operation serialization ---

// Serialize writes CreateRequest to io.Writer
func (r *CreateRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Flags); err != nil {
		return fmt.Errorf("failed to write flags: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Mode); err != nil {
		return fmt.Errorf("failed to write mode: %w", err)
	}
	return nil
}

// Deserialize reads CreateRequest from io.Reader
func (r *CreateRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	if err := binary.Read(reader, binary.LittleEndian, &r.Flags); err != nil {
		return fmt.Errorf("failed to read flags: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Mode); err != nil {
		return fmt.Errorf("failed to read mode: %w", err)
	}
	return nil
}

// Serialize writes CreateResponse to io.Writer
func (r *CreateResponse) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to write attr: %w", err)
	}
	return nil
}

// Deserialize reads CreateResponse from io.Reader
func (r *CreateResponse) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to read attr: %w", err)
	}
	return nil
}

// Serialize writes WriteRequest to io.Writer
func (r *WriteRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Offset); err != nil {
		return fmt.Errorf("failed to write offset: %w", err)
	}
	if err := writeVarint(w, uint64(len(r.Data))); err != nil {
		return fmt.Errorf("failed to write data length: %w", err)
	}
	if len(r.Data) > 0 {
		if _, err := w.Write(r.Data); err != nil {
			return fmt.Errorf("failed to write data: %w", err)
		}
	}
	return nil
}

// Deserialize reads WriteRequest from io.Reader
func (r *WriteRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.Offset); err != nil {
		return fmt.Errorf("failed to read offset: %w", err)
	}
	count, err := readVarint(reader, MaxPacketSize)
	if err != nil {
		return fmt.Errorf("failed to read data length: %w", err)
	}
	if count > 0 {
		r.Data = make([]byte, count)
		if _, err := io.ReadFull(reader, r.Data); err != nil {
			return fmt.Errorf("failed to read data: %w", err)
		}
	}
	return nil
}

// Serialize writes WriteResponse to io.Writer
func (r *WriteResponse) Serialize(w io.Writer) error {
	return binary.Write(w, binary.LittleEndian, r.Size)
}

// Deserialize reads WriteResponse from io.Reader
func (r *WriteResponse) Deserialize(reader io.Reader) error {
	return binary.Read(reader, binary.LittleEndian, &r.Size)
}

// Serialize writes MkdirRequest to io.Writer
func (r *MkdirRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Mode); err != nil {
		return fmt.Errorf("failed to write mode: %w", err)
	}
	return nil
}

// Deserialize reads MkdirRequest from io.Reader
func (r *MkdirRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	if err := binary.Read(reader, binary.LittleEndian, &r.Mode); err != nil {
		return fmt.Errorf("failed to read mode: %w", err)
	}
	return nil
}

// Serialize writes MkdirResponse to io.Writer
func (r *MkdirResponse) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to write attr: %w", err)
	}
	return nil
}

// Deserialize reads MkdirResponse from io.Reader
func (r *MkdirResponse) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to read attr: %w", err)
	}
	return nil
}

// Serialize writes UnlinkRequest to io.Writer
func (r *UnlinkRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	return nil
}

// Deserialize reads UnlinkRequest from io.Reader
func (r *UnlinkRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	return nil
}

// Serialize writes UnlinkResponse to io.Writer
func (r *UnlinkResponse) Serialize(w io.Writer) error { return nil }

// Deserialize reads UnlinkResponse from io.Reader
func (r *UnlinkResponse) Deserialize(reader io.Reader) error { return nil }

// Serialize writes RmdirRequest to io.Writer
func (r *RmdirRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	return nil
}

// Deserialize reads RmdirRequest from io.Reader
func (r *RmdirRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	return nil
}

// Serialize writes RmdirResponse to io.Writer
func (r *RmdirResponse) Serialize(w io.Writer) error { return nil }

// Deserialize reads RmdirResponse from io.Reader
func (r *RmdirResponse) Deserialize(reader io.Reader) error { return nil }

// Serialize writes RenameRequest to io.Writer
func (r *RenameRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.OldParentFh)); err != nil {
		return fmt.Errorf("failed to write old parent fh: %w", err)
	}
	if err := writeString(w, r.OldName); err != nil {
		return fmt.Errorf("failed to write old name: %w", err)
	}
	if _, err := w.Write(encodeFileHandle(r.NewParentFh)); err != nil {
		return fmt.Errorf("failed to write new parent fh: %w", err)
	}
	if err := writeString(w, r.NewName); err != nil {
		return fmt.Errorf("failed to write new name: %w", err)
	}
	return nil
}

// Deserialize reads RenameRequest from io.Reader
func (r *RenameRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.OldParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read old name: %w", err)
	}
	r.OldName = name
	fh2, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.NewParentFh = fh2
	name2, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read new name: %w", err)
	}
	r.NewName = name2
	return nil
}

// Serialize writes RenameResponse to io.Writer
func (r *RenameResponse) Serialize(w io.Writer) error { return nil }

// Deserialize reads RenameResponse from io.Reader
func (r *RenameResponse) Deserialize(reader io.Reader) error { return nil }

// Serialize writes SetattrRequest to io.Writer
func (r *SetattrRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Valid); err != nil {
		return fmt.Errorf("failed to write valid: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Mode); err != nil {
		return fmt.Errorf("failed to write mode: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Uid); err != nil {
		return fmt.Errorf("failed to write uid: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Gid); err != nil {
		return fmt.Errorf("failed to write gid: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Size); err != nil {
		return fmt.Errorf("failed to write size: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Mtime); err != nil {
		return fmt.Errorf("failed to write mtime: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.Atime); err != nil {
		return fmt.Errorf("failed to write atime: %w", err)
	}
	return nil
}

// Deserialize reads SetattrRequest from io.Reader
func (r *SetattrRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.Valid); err != nil {
		return fmt.Errorf("failed to read valid: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Mode); err != nil {
		return fmt.Errorf("failed to read mode: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Uid); err != nil {
		return fmt.Errorf("failed to read uid: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Gid); err != nil {
		return fmt.Errorf("failed to read gid: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Size); err != nil {
		return fmt.Errorf("failed to read size: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Mtime); err != nil {
		return fmt.Errorf("failed to read mtime: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.Atime); err != nil {
		return fmt.Errorf("failed to read atime: %w", err)
	}
	return nil
}

// Serialize writes SetattrResponse to io.Writer
func (r *SetattrResponse) Serialize(w io.Writer) error {
	return binary.Write(w, binary.LittleEndian, &r.Attr)
}

// Deserialize reads SetattrResponse from io.Reader
func (r *SetattrResponse) Deserialize(reader io.Reader) error {
	return binary.Read(reader, binary.LittleEndian, &r.Attr)
}

// Serialize writes SymlinkRequest to io.Writer
func (r *SymlinkRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	if err := writeString(w, r.Target); err != nil {
		return fmt.Errorf("failed to write target: %w", err)
	}
	return nil
}

// Deserialize reads SymlinkRequest from io.Reader
func (r *SymlinkRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	target, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read target: %w", err)
	}
	r.Target = target
	return nil
}

// Serialize writes SymlinkResponse to io.Writer
func (r *SymlinkResponse) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to write attr: %w", err)
	}
	return nil
}

// Deserialize reads SymlinkResponse from io.Reader
func (r *SymlinkResponse) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to read attr: %w", err)
	}
	return nil
}

// Serialize writes LinkRequest to io.Writer
func (r *LinkRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.ParentFh)); err != nil {
		return fmt.Errorf("failed to write parent fh: %w", err)
	}
	if err := writeString(w, r.Name); err != nil {
		return fmt.Errorf("failed to write name: %w", err)
	}
	if _, err := w.Write(encodeFileHandle(r.TargetFh)); err != nil {
		return fmt.Errorf("failed to write target fh: %w", err)
	}
	return nil
}

// Deserialize reads LinkRequest from io.Reader
func (r *LinkRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.ParentFh = fh
	name, err := readString(reader)
	if err != nil {
		return fmt.Errorf("failed to read name: %w", err)
	}
	r.Name = name
	tfh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.TargetFh = tfh
	return nil
}

// Serialize writes LinkResponse to io.Writer
func (r *LinkResponse) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to write attr: %w", err)
	}
	return nil
}

// Deserialize reads LinkResponse from io.Reader
func (r *LinkResponse) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.Attr); err != nil {
		return fmt.Errorf("failed to read attr: %w", err)
	}
	return nil
}

// Serialize writes FlushRequest to io.Writer
func (r *FlushRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.LastWriteReqID); err != nil {
		return fmt.Errorf("failed to write last_write_req_id: %w", err)
	}
	if err := binary.Write(w, binary.LittleEndian, r.HasSha256); err != nil {
		return fmt.Errorf("failed to write has_sha256: %w", err)
	}
	if _, err := w.Write(r.Sha256[:]); err != nil {
		return fmt.Errorf("failed to write sha256: %w", err)
	}
	return nil
}

// Deserialize reads FlushRequest from io.Reader
func (r *FlushRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	if err := binary.Read(reader, binary.LittleEndian, &r.LastWriteReqID); err != nil {
		return fmt.Errorf("failed to read last_write_req_id: %w", err)
	}
	if err := binary.Read(reader, binary.LittleEndian, &r.HasSha256); err != nil {
		return fmt.Errorf("failed to read has_sha256: %w", err)
	}
	if _, err := io.ReadFull(reader, r.Sha256[:]); err != nil {
		return fmt.Errorf("failed to read sha256: %w", err)
	}
	return nil
}

// Serialize writes FlushResponse to io.Writer
func (r *FlushResponse) Serialize(w io.Writer) error {
	return binary.Write(w, binary.LittleEndian, &r.Attr)
}

// Deserialize reads FlushResponse from io.Reader
func (r *FlushResponse) Deserialize(reader io.Reader) error {
	return binary.Read(reader, binary.LittleEndian, &r.Attr)
}

// Serialize writes GetattrRequest to io.Writer
func (r *GetattrRequest) Serialize(w io.Writer) error {
	if _, err := w.Write(encodeFileHandle(r.Fh)); err != nil {
		return fmt.Errorf("failed to write fh: %w", err)
	}
	return nil
}

// Deserialize reads GetattrRequest from io.Reader
func (r *GetattrRequest) Deserialize(reader io.Reader) error {
	fh, err := decodeFileHandle(reader)
	if err != nil {
		return err
	}
	r.Fh = fh
	return nil
}

// Serialize writes GetattrResponse to io.Writer
func (r *GetattrResponse) Serialize(w io.Writer) error {
	return binary.Write(w, binary.LittleEndian, &r.Attr)
}

// Deserialize reads GetattrResponse from io.Reader
func (r *GetattrResponse) Deserialize(reader io.Reader) error {
	return binary.Read(reader, binary.LittleEndian, &r.Attr)
}
