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
)

// TestCompressionSmallPayload verifies that payloads smaller than the threshold are not compressed
func TestCompressionSmallPayload(t *testing.T) {
	// Create a small payload (less than 32KB)
	smallData := make([]byte, 1024)
	for i := range smallData {
		smallData[i] = byte(i % 256)
	}

	resp := &ReadResponse{
		Data: smallData,
	}

	// Serialize with header
	data, err := SerializeWithHeader(OpRead, 42, FlagNone, resp)
	if err != nil {
		t.Fatalf("SerializeWithHeader failed: %v", err)
	}

	// Deserialize header to check flags
	var header Header
	if err := header.Deserialize(bytes.NewReader(data[:HeaderSize])); err != nil {
		t.Fatalf("Failed to deserialize header: %v", err)
	}

	// Verify FlagCompressed is NOT set for small payload
	if header.Flags&FlagCompressed != 0 {
		t.Errorf("Expected FlagCompressed to be clear for small payload, but it was set")
	}

	// Verify the data can be deserialized correctly
	var respDeserialized ReadResponse
	if err := respDeserialized.Deserialize(bytes.NewReader(data[HeaderSize:])); err != nil {
		t.Fatalf("Failed to deserialize response: %v", err)
	}

	if !bytes.Equal(respDeserialized.Data, smallData) {
		t.Errorf("Deserialized data does not match original")
	}
}

// TestCompressionLargePayload verifies that payloads larger than the threshold are compressed
func TestCompressionLargePayload(t *testing.T) {
	// Create a large compressible payload (more than 32KB)
	// Use repeated pattern to ensure good compression ratio
	largeData := make([]byte, 128*1024)
	pattern := []byte("This is a test pattern that should compress well. ")
	for i := 0; i < len(largeData); i++ {
		largeData[i] = pattern[i%len(pattern)]
	}

	resp := &ReadResponse{
		Data: largeData,
	}

	// Serialize with header
	data, err := SerializeWithHeader(OpRead, 42, FlagNone, resp)
	if err != nil {
		t.Fatalf("SerializeWithHeader failed: %v", err)
	}

	// Deserialize header to check flags
	var header Header
	if err := header.Deserialize(bytes.NewReader(data[:HeaderSize])); err != nil {
		t.Fatalf("Failed to deserialize header: %v", err)
	}

	// Verify FlagCompressed IS set for large payload
	if header.Flags&FlagCompressed == 0 {
		t.Errorf("Expected FlagCompressed to be set for large payload, but it was not")
	}

	// Verify compression actually reduced the size
	if len(data) >= len(largeData) {
		t.Errorf("Expected compressed data to be smaller than original, got %d >= %d", len(data), len(largeData))
	}

	t.Logf("Compression ratio: %.2f%% (original: %d, compressed: %d)",
		float64(len(data))*100.0/float64(len(largeData)), len(largeData), len(data))
}

// TestCompressionRoundTrip verifies that compression and decompression preserve data
func TestCompressionRoundTrip(t *testing.T) {
	testCases := []struct {
		name string
		size int
	}{
		{"Small 1KB", 1024},
		{"Medium 16KB", 16 * 1024},
		{"Threshold 32KB", 32 * 1024},
		{"Large 64KB", 64 * 1024},
		{"Very Large 256KB", 256 * 1024},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test data with some pattern
			originalData := make([]byte, tc.size)
			for i := range originalData {
				originalData[i] = byte((i * 7) % 256)
			}

			resp := &ReadResponse{
				Data: originalData,
			}

			// Serialize with header (may or may not compress based on size)
			serialized, err := SerializeWithHeader(OpRead, 99, FlagNone, resp)
			if err != nil {
				t.Fatalf("SerializeWithHeader failed: %v", err)
			}

			// Deserialize header
			var header Header
			if err := header.Deserialize(bytes.NewReader(serialized[:HeaderSize])); err != nil {
				t.Fatalf("Failed to deserialize header: %v", err)
			}

			// Simulate decompression (as done in handleConnection)
			payload := serialized[HeaderSize:]
			if header.Flags&FlagCompressed != 0 {
				// This is what the server/client does
				decoder, err := NewZstdDecoder()
				if err != nil {
					t.Fatalf("Failed to create decoder: %v", err)
				}
				defer decoder.Close()

				decompressed, err := decoder.DecodeAll(payload, nil)
				if err != nil {
					t.Fatalf("Failed to decompress: %v", err)
				}
				payload = decompressed
			}

			// Deserialize the response
			var respDeserialized ReadResponse
			if err := respDeserialized.Deserialize(bytes.NewReader(payload)); err != nil {
				t.Fatalf("Failed to deserialize response: %v", err)
			}

			// Verify data integrity
			if !bytes.Equal(respDeserialized.Data, originalData) {
				t.Errorf("Data mismatch after round-trip (size %d)", tc.size)
			}

			// Log compression info
			if header.Flags&FlagCompressed != 0 {
				t.Logf("Compressed: %d -> %d (%.1f%%)",
					len(originalData), len(serialized),
					float64(len(serialized))*100.0/float64(len(originalData)))
			} else {
				t.Logf("Not compressed (size %d below threshold or not beneficial)", tc.size)
			}
		})
	}
}
