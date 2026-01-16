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
	"testing"

	"golang.org/x/sys/unix"
)

// TestHeaderSerialization tests Header serialization/deserialization
func TestHeaderSerialization(t *testing.T) {
	tests := []struct {
		name   string
		header Header
	}{
		{
			name: "basic header",
			header: Header{
				Opcode: OpMount,
				Size:   100,
				Seq:    12345,
			},
		},
		{
			name: "zero values",
			header: Header{
				Opcode: 0,
				Size:   0,
				Seq:    0,
			},
		},
		{
			name: "max values",
			header: Header{
				Opcode: 0xFFFFFFFF,
				Size:   0xFFFFFFFF,
				Flags:  0xFFFFFFFF,
				Seq:    0xFFFFFFFF,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize
			if err := tt.header.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Check serialized size
			if buf.Len() != HeaderSize {
				t.Errorf("Expected serialized size %d, got %d", HeaderSize, buf.Len())
			}

			// Deserialize
			var deserialized Header
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if deserialized != tt.header {
				t.Errorf("Deserialized header mismatch:\nExpected: %+v\nGot:      %+v", tt.header, deserialized)
			}
		})
	}
}

// TestMountRequestSerialization tests MountRequest serialization/deserialization
func TestMountRequestSerialization(t *testing.T) {
	tests := []struct {
		name string
		req  MountRequest
	}{
		{
			name: "basic mount request",
			req: MountRequest{
				Version: ProtocolVersion,
				Flags:   FlagReadOnly,
				Path:    "/data",
			},
		},
		{
			name: "no flags",
			req: MountRequest{
				Version: ProtocolVersion,
				Flags:   FlagNone,
				Path:    "/",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize (body only, no header for request deserialization)
			tmpBuf := bytes.Buffer{}
			if err := tt.req.Serialize(&tmpBuf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}
			buf = tmpBuf

			// Deserialize
			var deserialized MountRequest
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare (skip header comparison)
			if deserialized.Version != tt.req.Version || deserialized.Flags != tt.req.Flags || deserialized.Path != tt.req.Path {
				t.Errorf("Deserialized request mismatch:\nExpected: %+v\nGot:      %+v", tt.req, deserialized)
			}
		})
	}
}

// TestMountResponseSerialization tests MountResponse serialization/deserialization
func TestMountResponseSerialization(t *testing.T) {
	// Create a file handle for testing
	fh := unix.NewFileHandle(1, []byte{0x01, 0x02, 0x03, 0x04})

	// Create test file attributes
	testAttr := FileAttr{
		Dev:      123,
		Ino:      456,
		Nlink:    1,
		Mode:     0755,
		Uid:      1000,
		Gid:      1000,
		Size:     4096,
		Blksize:  4096,
		Blocks:   8,
		Mtim:     1234567890,
		Ctim:     1234567890,
		MtimNsec: 0,
		CtimNsec: 0,
	}

	tests := []struct {
		name string
		resp MountResponse
	}{
		{
			name: "successful mount",
			resp: MountResponse{
				Version: ProtocolVersion,
				Flags:   FlagReadOnly,
				Fh:      fh,
				Attr:    testAttr,
			},
		},
		{
			name: "empty file handle",
			resp: MountResponse{
				Version: ProtocolVersion,
				Flags:   FlagNone,
				Fh:      unix.NewFileHandle(0, []byte{}),
				Attr:    testAttr,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize (body only)
			if err := tt.resp.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Deserialize
			var deserialized MountResponse
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if deserialized.Version != tt.resp.Version {
				t.Errorf("Version mismatch: expected %d, got %d", tt.resp.Version, deserialized.Version)
			}
			if deserialized.Flags != tt.resp.Flags {
				t.Errorf("Flags mismatch: expected %d, got %d", tt.resp.Flags, deserialized.Flags)
			}
			if !bytes.Equal(deserialized.Fh.Bytes(), tt.resp.Fh.Bytes()) || deserialized.Fh.Type() != tt.resp.Fh.Type() {
				t.Errorf("FileHandle mismatch: expected %v, got %v", tt.resp.Fh, deserialized.Fh)
			}
			if deserialized.Attr != tt.resp.Attr {
				t.Errorf("Attr mismatch: expected %+v, got %+v", tt.resp.Attr, deserialized.Attr)
			}
		})
	}
}

// TestLookupRequestSerialization tests LookupRequest serialization/deserialization
func TestLookupRequestSerialization(t *testing.T) {
	fh := unix.NewFileHandle(1, []byte{0xAA, 0xBB, 0xCC})

	tests := []struct {
		name string
		req  LookupRequest
	}{
		{
			name: "basic lookup",
			req: LookupRequest{
				ParentFh: fh,
				Name:     "testfile.txt",
			},
		},
		{
			name: "empty name",
			req: LookupRequest{
				ParentFh: fh,
				Name:     "",
			},
		},
		{
			name: "long name",
			req: LookupRequest{
				ParentFh: fh,
				Name:     "very_long_filename_with_many_characters_to_test_serialization.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize (body only)
			tmpBuf := bytes.Buffer{}
			if err := tt.req.Serialize(&tmpBuf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}
			buf = tmpBuf

			// Deserialize
			var deserialized LookupRequest
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if deserialized.Name != tt.req.Name {
				t.Errorf("Name mismatch: expected %q, got %q", tt.req.Name, deserialized.Name)
			}
			if !bytes.Equal(deserialized.ParentFh.Bytes(), tt.req.ParentFh.Bytes()) || deserialized.ParentFh.Type() != tt.req.ParentFh.Type() {
				t.Errorf("FileHandle mismatch")
			}
		})
	}
}

// TestLookupResponseSerialization tests LookupResponse serialization/deserialization
func TestLookupResponseSerialization(t *testing.T) {
	fh := unix.NewFileHandle(2, []byte{0x11, 0x22, 0x33, 0x44})
	attr := FileAttr{
		Dev:      123,
		Ino:      456,
		Nlink:    1,
		Mode:     0644,
		Uid:      1000,
		Gid:      1000,
		Size:     4096,
		Blksize:  4096,
		Blocks:   8,
		Mtim:     1234567890,
		MtimNsec: 987654321,
		Ctim:     1234567890,
		CtimNsec: 111222333,
	}
	copy(attr.Sha256[:], []byte("test_sha256_hash_32_bytes_long!!"))

	tests := []struct {
		name string
		resp LookupResponse
	}{
		{
			name: "basic lookup response",
			resp: LookupResponse{
				Fh:   fh,
				Attr: attr,
			},
		},
		{
			name: "zero attributes",
			resp: LookupResponse{
				Fh:   fh,
				Attr: FileAttr{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize
			if err := tt.resp.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Deserialize
			var deserialized LookupResponse
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare file handle
			if !bytes.Equal(deserialized.Fh.Bytes(), tt.resp.Fh.Bytes()) || deserialized.Fh.Type() != tt.resp.Fh.Type() {
				t.Errorf("FileHandle mismatch")
			}

			// Compare attributes
			if deserialized.Attr != tt.resp.Attr {
				t.Errorf("Attr mismatch:\nExpected: %+v\nGot:      %+v", tt.resp.Attr, deserialized.Attr)
			}
		})
	}
}

// TestReadRequestSerialization tests ReadRequest serialization/deserialization
func TestReadRequestSerialization(t *testing.T) {
	fh := unix.NewFileHandle(3, []byte{0xFF, 0xEE, 0xDD})

	tests := []struct {
		name string
		req  ReadRequest
	}{
		{
			name: "basic read",
			req: ReadRequest{
				Fh:     fh,
				Offset: 0,
				Size:   4096,
			},
		},
		{
			name: "read with offset",
			req: ReadRequest{
				Fh:     fh,
				Offset: 1024,
				Size:   512,
			},
		},
		{
			name: "zero size read",
			req: ReadRequest{
				Fh:     fh,
				Offset: 0,
				Size:   0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize
			if err := tt.req.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Deserialize
			var deserialized ReadRequest
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if !bytes.Equal(deserialized.Fh.Bytes(), tt.req.Fh.Bytes()) || deserialized.Fh.Type() != tt.req.Fh.Type() {
				t.Errorf("FileHandle mismatch")
			}
			if deserialized.Offset != tt.req.Offset {
				t.Errorf("Offset mismatch: expected %d, got %d", tt.req.Offset, deserialized.Offset)
			}
			if deserialized.Size != tt.req.Size {
				t.Errorf("Size mismatch: expected %d, got %d", tt.req.Size, deserialized.Size)
			}
		})
	}
}

// TestReadResponseSerialization tests ReadResponse serialization/deserialization
func TestReadResponseSerialization(t *testing.T) {
	tests := []struct {
		name string
		resp ReadResponse
	}{
		{
			name: "basic read response",
			resp: ReadResponse{
				Data: []byte("Hello, World!"),
			},
		},
		{
			name: "empty data",
			resp: ReadResponse{
				Data: []byte{},
			},
		},
		{
			name: "large data",
			resp: ReadResponse{
				Data: make([]byte, 8192),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize
			if err := tt.resp.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Deserialize
			var deserialized ReadResponse
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if !bytes.Equal(deserialized.Data, tt.resp.Data) {
				t.Errorf("Data mismatch: expected len=%d, got len=%d", len(tt.resp.Data), len(deserialized.Data))
			}
		})
	}
}

// TestReadDirRequestSerialization tests ReadDirRequest serialization/deserialization
func TestReadDirRequestSerialization(t *testing.T) {
	fh := unix.NewFileHandle(4, []byte{0x55, 0x66})

	tests := []struct {
		name string
		req  ReadDirRequest
	}{
		{
			name: "basic readdir",
			req: ReadDirRequest{
				Fh: fh,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize
			if err := tt.req.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Deserialize
			var deserialized ReadDirRequest
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if !bytes.Equal(deserialized.Fh.Bytes(), tt.req.Fh.Bytes()) || deserialized.Fh.Type() != tt.req.Fh.Type() {
				t.Errorf("FileHandle mismatch")
			}

		})
	}
}

// TestReadDirResponseSerialization tests ReadDirResponse serialization/deserialization
func TestReadDirResponseSerialization(t *testing.T) {
	fh1 := unix.NewFileHandle(5, []byte{0xAA})
	fh2 := unix.NewFileHandle(6, []byte{0xBB, 0xCC})

	attr1 := FileAttr{
		Dev:  1,
		Ino:  100,
		Mode: 0755,
		Size: 4096,
	}

	attr2 := FileAttr{
		Dev:  1,
		Ino:  101,
		Mode: 0644,
		Size: 1024,
	}

	tests := []struct {
		name string
		resp ReadDirResponse
	}{
		{
			name: "basic readdir response",
			resp: ReadDirResponse{
				Entries: []DirEntry{
					{
						Fh:   fh1,
						Name: "file1.txt",
						Attr: attr1,
					},
					{
						Fh:   fh2,
						Name: "file2.txt",
						Attr: attr2,
					},
				},
			},
		},
		{
			name: "empty entries",
			resp: ReadDirResponse{
				Entries: []DirEntry{},
			},
		},
		{
			name: "single entry",
			resp: ReadDirResponse{
				Entries: []DirEntry{
					{
						Fh:   fh1,
						Name: "single.txt",
						Attr: attr1,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			// Serialize
			if err := tt.resp.Serialize(&buf); err != nil {
				t.Fatalf("Serialize failed: %v", err)
			}

			// Deserialize
			var deserialized ReadDirResponse
			if err := deserialized.Deserialize(&buf); err != nil {
				t.Fatalf("Deserialize failed: %v", err)
			}

			// Compare
			if len(deserialized.Entries) != len(tt.resp.Entries) {
				t.Fatalf("Entries length mismatch: expected %d, got %d", len(tt.resp.Entries), len(deserialized.Entries))
			}

			for i := range tt.resp.Entries {
				expected := tt.resp.Entries[i]
				got := deserialized.Entries[i]

				if !bytes.Equal(got.Fh.Bytes(), expected.Fh.Bytes()) || got.Fh.Type() != expected.Fh.Type() {
					t.Errorf("Entry %d: FileHandle mismatch", i)
				}
				if got.Name != expected.Name {
					t.Errorf("Entry %d: Name mismatch: expected %q, got %q", i, expected.Name, got.Name)
				}
				if got.Attr != expected.Attr {
					t.Errorf("Entry %d: Attr mismatch", i)
				}
			}
		})
	}
}

// TestFileHandleEncoding tests encodeFileHandle/decodeFileHandle
func TestFileHandleEncoding(t *testing.T) {
	tests := []struct {
		name string
		fh   unix.FileHandle
	}{
		{
			name: "basic file handle",
			fh:   unix.NewFileHandle(1, []byte{0x01, 0x02, 0x03}),
		},
		{
			name: "empty bytes",
			fh:   unix.NewFileHandle(0, []byte{}),
		},
		{
			name: "large file handle",
			fh:   unix.NewFileHandle(123, make([]byte, 128)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encode
			encoded := encodeFileHandle(tt.fh)

			// Decode
			decoded, err := decodeFileHandle(bytes.NewBuffer(encoded))
			if err != nil {
				t.Fatalf("decodeFileHandle failed: %v", err)
			}

			// Compare
			if !bytes.Equal(decoded.Bytes(), tt.fh.Bytes()) {
				t.Errorf("Bytes mismatch: expected %v, got %v", tt.fh.Bytes(), decoded.Bytes())
			}
			if decoded.Type() != tt.fh.Type() {
				t.Errorf("Type mismatch: expected %d, got %d", tt.fh.Type(), decoded.Type())
			}
		})
	}
}

// TestSerializeWithHeader tests the SerializeWithHeader function
func TestSerializeWithHeader(t *testing.T) {
	tests := []struct {
		name  string
		op    uint32
		flags uint32
		seq   uint32
		resp  Serializable
	}{
		{
			name: "ReadResponse",
			op:   OpRead,
			seq:  42,
			resp: &ReadResponse{
				Data: []byte("test data"),
			},
		},
		{
			name: "Empty ReadResponse",
			op:   OpRead,
			seq:  100,
			resp: &ReadResponse{
				Data: []byte{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Serialize with header
			data, err := SerializeWithHeader(0, tt.seq, tt.resp)
			if err != nil {
				t.Fatalf("SerializeWithHeader failed: %v", err)
			}

			// Deserialize header
			var header Header
			buf := bytes.NewReader(data)
			if err := header.Deserialize(buf); err != nil {
				t.Fatalf("Failed to deserialize header: %v", err)
			}

			// Verify header fields
			if header.Opcode != 0 {
				t.Errorf("Header Opcode should be 0 for response, got %d", header.Opcode)
			}
			if int(header.Size) != len(data) {
				t.Errorf("Header Size mismatch: expected %d, got %d", len(data), header.Size)
			}
			if header.Seq != tt.seq {
				t.Errorf("Header Seq mismatch: expected %d, got %d", tt.seq, header.Seq)
			}
		})
	}
}

// TestInvalidDeserialization tests error handling for invalid data
func TestInvalidDeserialization(t *testing.T) {
	t.Run("truncated header", func(t *testing.T) {
		buf := bytes.NewReader([]byte{0x01, 0x02})
		var header Header
		if err := header.Deserialize(buf); err == nil {
			t.Error("Expected error for truncated header, got nil")
		}
	})

	t.Run("truncated file handle", func(t *testing.T) {
		data := []byte{0x01, 0x00, 0x00, 0x00} // handleLen=1, but no data
		_, err := decodeFileHandle(bytes.NewBuffer(data))
		if err == nil {
			t.Error("Expected error for truncated file handle, got nil")
		}
	})

	t.Run("invalid file handle size", func(t *testing.T) {
		data := []byte{0x01}
		_, err := decodeFileHandle(bytes.NewBuffer(data))
		if err == nil {
			t.Error("Expected error for invalid file handle size, got nil")
		}
	})
}

// TestReadlinkSerialization tests ReadlinkRequest and ReadlinkResponse serialization/deserialization
func TestReadlinkSerialization(t *testing.T) {
	t.Run("ReadlinkRequest", func(t *testing.T) {
		fh := unix.NewFileHandle(1, []byte{0x01, 0x02, 0x03, 0x04})
		req := ReadlinkRequest{
			Fh: fh,
		}

		var buf bytes.Buffer

		// Serialize
		if err := req.Serialize(&buf); err != nil {
			t.Fatalf("Serialize failed: %v", err)
		}

		// Deserialize
		var deserialized ReadlinkRequest
		if err := deserialized.Deserialize(&buf); err != nil {
			t.Fatalf("Deserialize failed: %v", err)
		}

		// Compare file handles
		if deserialized.Fh.Type() != req.Fh.Type() {
			t.Errorf("Fh Type mismatch: expected %d, got %d", req.Fh.Type(), deserialized.Fh.Type())
		}
		if !bytes.Equal(deserialized.Fh.Bytes(), req.Fh.Bytes()) {
			t.Errorf("Fh Bytes mismatch: expected %v, got %v", req.Fh.Bytes(), deserialized.Fh.Bytes())
		}
	})

	t.Run("ReadlinkResponse", func(t *testing.T) {
		tests := []struct {
			name   string
			target string
		}{
			{
				name:   "simple path",
				target: "/tmp/target",
			},
			{
				name:   "relative path",
				target: "../target",
			},
			{
				name:   "absolute path",
				target: "/usr/bin/target",
			},
			{
				name:   "empty target",
				target: "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := ReadlinkResponse{
					Target: tt.target,
				}

				var buf bytes.Buffer

				// Serialize
				if err := resp.Serialize(&buf); err != nil {
					t.Fatalf("Serialize failed: %v", err)
				}

				// Deserialize
				var deserialized ReadlinkResponse
				if err := deserialized.Deserialize(&buf); err != nil {
					t.Fatalf("Deserialize failed: %v", err)
				}

				// Compare
				if deserialized.Target != resp.Target {
					t.Errorf("Target mismatch: expected %q, got %q", resp.Target, deserialized.Target)
				}
			})
		}
	})
}
