//go:build !clionly && linux

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

package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// merkleForTree calls computeLayers with a high threshold (single-layer) and
// returns the MerkleHash on the single resulting spec.
func merkleForTree(t *testing.T, root string) string {
	t.Helper()
	layers, err := computeLayers(root, nil, true, 0)
	if err != nil {
		t.Fatalf("computeLayers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(layers))
	}
	return layers[0].MerkleHash
}

// warmXattrCache forces a full streaming pass through the layer so that the
// cachingLayer goroutine has a chance to write the xattr, then waits up to
// one second for the attribute to appear.
func warmXattrCache(t *testing.T, layer v1.Layer, rootDir, merkle string) {
	t.Helper()
	if _, err := layer.DiffID(); err != nil {
		t.Fatalf("DiffID: %v", err)
	}
	// The goroutine inside cachingLayer.DiffID calls Digest()+Size() and writes
	// the xattr.  Both methods return immediately (tarball caches them after the
	// first stream), so the goroutine should finish well within 1 s.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := readLayerXattrCache(rootDir, merkle); ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("xattr cache was not written within 1 s")
}

// ─── Merkle hash stability ────────────────────────────────────────────────────

// TestMerkleHash_Stable verifies that scanning the same tree twice yields the
// same Merkle hash.
func TestMerkleHash_Stable(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "hello.txt"), 100)
	writeFile(t, filepath.Join(root, "b", "world.txt"), 200)

	h1 := merkleForTree(t, root)
	h2 := merkleForTree(t, root)
	if h1 == "" {
		t.Fatal("expected non-empty Merkle hash")
	}
	if h1 != h2 {
		t.Fatalf("hash is not stable: %q != %q", h1, h2)
	}
}

// TestMerkleHash_ChangesOnNewFile verifies that adding a file invalidates the hash.
func TestMerkleHash_ChangesOnNewFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "existing.txt"), 50)

	before := merkleForTree(t, root)
	writeFile(t, filepath.Join(root, "new.txt"), 50)
	after := merkleForTree(t, root)

	if before == after {
		t.Fatal("hash did not change after adding a file")
	}
}

// TestMerkleHash_ChangesOnSize verifies that changing a file's size (which
// also updates mtime) invalidates the hash.
func TestMerkleHash_ChangesOnSize(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.bin")
	writeFile(t, path, 100)

	before := merkleForTree(t, root)

	// Overwrite with a different size, guaranteeing stat change.
	if err := os.WriteFile(path, make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	after := merkleForTree(t, root)

	if before == after {
		t.Fatal("hash did not change after changing file size")
	}
}

// TestMerkleHash_ChangesOnPermission verifies that chmod invalidates the hash.
func TestMerkleHash_ChangesOnPermission(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.sh")
	writeFile(t, path, 10)

	before := merkleForTree(t, root)

	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	after := merkleForTree(t, root)

	if before == after {
		t.Fatal("hash did not change after chmod")
	}
}

// TestMerkleHash_ChangesOnMtime verifies that touching a file's mtime
// invalidates the hash when stripTimes=false.
func TestMerkleHash_ChangesOnMtime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	writeFile(t, path, 10)

	before := merkleForTree(t, root)

	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	after := merkleForTree(t, root)

	if before == after {
		t.Fatal("hash did not change after mtime update (stripTimes=false)")
	}
}

// TestMerkleHash_StripTimesIgnoresMtime verifies that when stripTimes=true a
// mtime-only change should still affect the merkle hash,
// since mtime is used as approximation for file content changes.
func TestMerkleHash_StripTimesIgnoresMtime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	writeFile(t, path, 10)

	before := merkleForTree(t, root)

	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	after := merkleForTree(t, root)

	if before == after {
		t.Fatal("hash doens't changed after mtime update ")
	}
}

// TestMerkleHash_ExcludePatterns verifies that excluded entries do not
// contribute to the hash and that identical trees with exclusions produce the
// same hash.
func TestMerkleHash_ExcludePatterns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), 10)
	writeFile(t, filepath.Join(root, "drop.log"), 10)

	// Compute hash without exclusion (includes drop.log).
	hashAll := merkleForTree(t, root)

	// Compute hash excluding *.log — drop.log is ignored.
	layers, err := computeLayers(root, []string{"*.log"}, false, 0)
	if err != nil {
		t.Fatalf("computeLayers: %v", err)
	}
	hashExcl := layers[0].MerkleHash

	if hashAll == hashExcl {
		t.Fatal("exclusion pattern had no effect on hash")
	}

	// Two calls with the same exclusion must agree.
	layers2, err := computeLayers(root, []string{"*.log"}, false, 0)
	if err != nil {
		t.Fatalf("computeLayers (2nd): %v", err)
	}
	if hashExcl != layers2[0].MerkleHash {
		t.Fatal("hash with same exclusion is not stable")
	}
}

// ─── xattr cache round-trip ───────────────────────────────────────────────────

// TestXattrCache_RoundTrip writes a cache entry and reads it back.
func TestXattrCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	digestID, _ := v1.NewHash("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	const merkle = "deadbeef1234"

	writeLayerXattrCache(dir, merkle, digestID)

	gotDigest, ok := readLayerXattrCache(dir, merkle)
	if !ok {
		t.Fatal("readLayerXattrCache: expected cache hit")
	}
	if gotDigest != digestID {
		t.Errorf("DigestID: want %v, got %v", digestID, gotDigest)
	}
}

// TestXattrCache_StaleHash verifies that a different Merkle hash causes a miss.
func TestXattrCache_StaleHash(t *testing.T) {
	dir := t.TempDir()

	digestID, _ := v1.NewHash("sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	writeLayerXattrCache(dir, "originalHash", digestID)

	_, ok := readLayerXattrCache(dir, "differentHash")
	if ok {
		t.Fatal("expected cache miss for different Merkle hash, got hit")
	}
}

// TestXattrCache_EmptyDir verifies that reading from a dir with no xattr
// returns ok=false without error.
func TestXattrCache_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, ok := readLayerXattrCache(dir, "anyhash")
	if ok {
		t.Fatal("expected cache miss on fresh directory")
	}
}
