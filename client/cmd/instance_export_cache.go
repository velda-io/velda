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
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"syscall"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"golang.org/x/sys/unix"
)

// xattrLayerCache is the extended attribute name written to the layer root dir.
// Its value is a JSON-encoded layerCacheEntry.
const xattrLayerCache = "user.velda.layer_cache"

// layerCacheEntry is the JSON structure stored in the xattr.
type layerCacheEntry struct {
	// MerkleHash is the Merkle-tree hash of all file/dir attributes in the layer.
	// It is used as a cache validation: if the current Merkle hash matches this value the
	// DigestID is used directly without re-streaming.
	MerkleHash string `json:"k"`
	// DigestID is the sha256 digest of the compressed blob.
	DigestID string `json:"g"`
}

// merkleHashLeaf computes the Merkle leaf hash for a non-directory entry
// (regular file, symlink, hard link, etc.) using its attributes only.
func merkleHashLeaf(hasher io.Writer, path string, info fs.FileInfo) error {
	st := info.Sys().(*syscall.Stat_t)
	var mtime int64
	mtime = st.Mtim.Sec*1_000_000_000 + int64(st.Mtim.Nsec)

	linkTarget := ""
	if info.Mode()&os.ModeSymlink != 0 {
		var err error
		linkTarget, err = os.Readlink(path)
		if err != nil {
			return err
		}
	}

	// Include inode-level attributes but NOT file content: fast cache key.
	_, err := fmt.Fprintf(hasher, "leaf\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%s\n",
		info.Mode(), st.Uid, st.Gid, st.Size, mtime, st.Nlink, linkTarget)
	return err
}

// ─── xattr cache helpers ──────────────────────────────────────────────────────

// readLayerXattrCache reads the layer cache entry from the extended attributes of
// rootDir.  If the Merkle hash matches currentMerkle, the cached DigestID is
// returned with ok=true.  Otherwise ok=false and the caller should fall back to
// a full re-stream and cache update.
func readLayerXattrCache(rootDir, currentMerkle string) (digestID v1.Hash, ok bool) {
	buf := make([]byte, 4096)
	n, err := unix.Getxattr(rootDir, xattrLayerCache, buf)
	if err != nil || n == 0 {
		return
	}
	var entry layerCacheEntry
	if err := json.Unmarshal(buf[:n], &entry); err != nil {
		return
	}
	if entry.MerkleHash != currentMerkle {
		return // stale
	}
	digestID, err = v1.NewHash(entry.DigestID)
	if err != nil {
		return
	}
	ok = true
	return
}

// writeLayerXattrCache saves the (diffID, digestID, size, mediaType) for the
// current Merkle hash to the extended attributes of rootDir.  Failures are
// silently ignored since the cache is advisory.
func writeLayerXattrCache(rootDir, merkleHash string, digestID v1.Hash) {
	entry := layerCacheEntry{
		MerkleHash: merkleHash,
		DigestID:   digestID.String(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("Warning: failed to marshal layer cache entry for %s: %v", rootDir, err)
		return
	}
	// Use flags=0 (create or replace).
	err = unix.Setxattr(rootDir, xattrLayerCache, data, 0)
	if err != nil {
		log.Printf("Warning: failed to update layer cache xattr for %s: %v", rootDir, err)
		return
	}
}

// uploadLayerSpec uploads a layer to the registry and returns a v1.Layer
// with known hashes, then update the xattr cache on success for future use.
func uploadLayerSpec(spec *tarLayerSpec, repo name.Repository, pushOption remote.Option, progress chan<- v1.Update) (v1.Layer, error) {
	merkle := spec.MerkleHash

	rc, err := spec.Open()
	if err != nil {
		close(progress)
		return nil, fmt.Errorf("opening layer stream: %w", err)
	}

	sl := stream.NewLayer(rc)
	if err := remote.WriteLayer(repo, sl, pushOption, remote.WithProgress(progress)); err != nil {
		return nil, fmt.Errorf("uploading layer: %w", err)
	}

	digestID, err := sl.Digest()
	if err != nil {
		return nil, fmt.Errorf("reading layer digest after upload: %w", err)
	}
	if merkle != "" {
		writeLayerXattrCache(spec.RootDir, merkle, digestID)
	}

	return sl, nil
}
