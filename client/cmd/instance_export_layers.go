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
	"archive/tar"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// defaultLayerSizeThreshold is the default uncompressed-byte threshold that
// triggers splitting off a new image layer during bottom-up analysis (200 MiB).
const defaultLayerSizeThreshold int64 = 200 << 20

// tarLayerSpec describes a single container-image layer produced by the
// automatic bottom-up layering algorithm.
//
// The layer streams all entries reachable from RootDir EXCEPT the directories
// listed in SubLayerDirs — those directories belong to sibling/child layers
// created earlier in the stack.
type tarLayerSpec struct {
	// RootDir is the absolute path (inside tmpMount) that this layer is
	// rooted at.  The walk starts here.
	RootDir string

	// SubLayerDirs is the set of absolute paths (inside tmpMount) that must
	// be skipped while streaming this layer because they are covered by other
	// layers.  Paths may be at any depth below RootDir; the walker skips the
	// shallowest matching entry and all of its descendants automatically.
	SubLayerDirs []string

	// Description is a human-readable label for the layer shown during export
	// (e.g. "/usr" or "/" for the root layer).  Set automatically by
	// computeLayers from the directory path relative to the filesystem root.
	Description string

	// TotalSize is the approximate uncompressed byte size of this layer's
	// content, as measured during the bottom-up analysis pass.  It is used
	// only for informational display; it estimates the sum of all regular-file
	// sizes that will be streamed into this layer.
	TotalSize int64

	// tmpMount is the bind-mounted root prefix used to derive tar-relative
	// entry names (same value shared by all layers in one export run).
	tmpMount string

	excludePatterns []string
	stripTimes      bool
}

// Open is the tarball.Opener implementation consumed by
// github.com/google/go-containerregistry/pkg/v1/tarball.LayerFromOpener.
// It starts a background goroutine that streams the tar content for this
// layer through a pipe; the returned ReadCloser is the read end.
func (l *tarLayerSpec) Open() (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(l.stream(pw))
	}()
	return pr, nil
}

// stream writes a POSIX-compatible tar archive for this layer's content.
func (l *tarLayerSpec) stream(w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	hardLinks := make(map[inodeKey]string)

	// Build a fast-lookup set of directories to skip.
	skipSet := make(map[string]bool, len(l.SubLayerDirs))
	for _, d := range l.SubLayerDirs {
		skipSet[filepath.Clean(d)] = true
	}

	mountSlash := filepath.ToSlash(l.tmpMount)

	return filepath.WalkDir(l.RootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Skip directories (and their entire subtrees) that belong to other layers.
		if d.IsDir() && path != l.RootDir && skipSet[filepath.Clean(path)] {
			return filepath.SkipDir
		}

		// Apply user-defined exclusion patterns.
		relFromMount := strings.TrimPrefix(filepath.ToSlash(path), mountSlash)
		relFromMount = strings.TrimPrefix(relFromMount, "/")
		if relFromMount != "" && matchesAnyExclude(relFromMount, l.excludePatterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		var st syscall.Stat_t
		if err := syscall.Lstat(path, &st); err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.Mode()&os.ModeSocket != 0 || info.Mode()&os.ModeNamedPipe != 0 || info.Mode()&os.ModeDevice != 0 {
			// Skip sockets, FIFOs, and device files since they can't be reliably
			// recreated in the target environment.  This is consistent with Docker
			// and BuildKit's handling of these file types.
			return nil
		}

		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if linkTarget, err = os.Readlink(path); err != nil {
				return err
			}
		}

		hdr, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}

		// Derive the tar-relative entry name by stripping the tmpMount prefix.
		hdr.Name = strings.TrimPrefix(filepath.ToSlash(path), mountSlash)
		if hdr.Name == "" {
			hdr.Name = "."
		}
		if d.IsDir() && !strings.HasSuffix(hdr.Name, "/") {
			hdr.Name += "/"
		}

		// Preserve numeric owner; drop name strings (UID/GID are canonical).
		hdr.Uid = int(st.Uid)
		hdr.Gid = int(st.Gid)
		hdr.Uname = ""
		hdr.Gname = ""

		if l.stripTimes {
			hdr.ModTime = time.Time{}
			hdr.AccessTime = time.Time{}
			hdr.ChangeTime = time.Time{}
		}

		// Record hard-links so subsequent occurrences are emitted as TypeLink.
		if hdr.Typeflag == tar.TypeReg && st.Nlink > 1 {
			key := inodeKey{st.Dev, st.Ino}
			if firstPath, seen := hardLinks[key]; seen {
				hdr.Typeflag = tar.TypeLink
				hdr.Linkname = firstPath
				hdr.Size = 0
				return tw.WriteHeader(hdr)
			}
			hardLinks[key] = hdr.Name
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if hdr.Typeflag == tar.TypeReg && hdr.Size > 0 {
			err := func() error {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				if _, err := io.Copy(tw, f); err != nil {
					return err
				}
				return nil
			}()
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// computeLayers performs a bottom-up analysis of the filesystem rooted at
// tmpMount and splits it into one or more image layers.
//
// Algorithm
//
//  1. Walk dirs depth-first (post-order / bottom-up).
//  2. For each directory accumulate the total unallocated bytes: files directly
//     inside it, plus the unallocated remainder bubbled up from any child
//     directory that did NOT reach the threshold on its own.
//  3. If the accumulated size ≥ sizeThreshold, emit a layer for this directory
//     (recording the child directories that already have their own layers as
//     SubLayerDirs to skip) and reset the contribution to this dir's parent to 0.
//  4. After the recursive pass a root layer is always emitted for any remaining
//     unallocated content (and to guarantee the root "." entry is covered).
//  5. The collected layers are in bottom-up (leaf-first) order; reverse them
//     so the base layer is first and leaf layers are last.
//
// When sizeThreshold ≤ 0 the entire tree is returned as a single layer
// (equivalent to the old single-opener behaviour).
func computeLayers(tmpMount string, excludePatterns []string, stripTimes bool, sizeThreshold int64) ([]*tarLayerSpec, error) {
	if sizeThreshold <= 0 {
		return []*tarLayerSpec{{
			RootDir:         tmpMount,
			Description:     "/",
			tmpMount:        tmpMount,
			excludePatterns: excludePatterns,
			stripTimes:      stripTimes,
		}}, nil
	}

	var layers []*tarLayerSpec
	unalloc, skipDirs, err := analyzeDir(tmpMount, tmpMount, excludePatterns, stripTimes, sizeThreshold, &layers)
	if err != nil {
		return nil, err
	}

	// Ensure the root directory (tmpMount / ".") always appears in some layer.
	// This is needed even when unalloc==0 but the root itself never triggered the
	// threshold (e.g. all files live in large subdirectories).
	rootAlreadyCovered := false
	for _, l := range layers {
		if l.RootDir == tmpMount {
			rootAlreadyCovered = true
			break
		}
	}
	if !rootAlreadyCovered || unalloc > 0 {
		layers = append(layers, &tarLayerSpec{
			RootDir:         tmpMount,
			Description:     "/",
			TotalSize:       unalloc,
			SubLayerDirs:    skipDirs,
			tmpMount:        tmpMount,
			excludePatterns: excludePatterns,
			stripTimes:      stripTimes,
		})
	}

	// Reverse from bottom-up to top-down (root layer first).
	for i, j := 0, len(layers)-1; i < j; i, j = i+1, j-1 {
		layers[i], layers[j] = layers[j], layers[i]
	}

	return layers, nil
}

// analyzeDir recursively walks dir in post-order (children before parent) and
// appends a tarLayerSpec to *out whenever the accumulated unallocated size of
// the subtree meets threshold.
//
// Returns:
//   - unallocated: bytes in this subtree not yet assigned to any layer.
//     This is 0 when dir itself became a layer (all local content is covered).
//   - skipDirsForParent: the set of absolute paths that the parent must add to
//     its own SubLayerDirs.  When dir itself created a layer this is [dir];
//     otherwise it is the union of sub-layer dirs collected from children,
//     passed up so the parent can skip them during streaming.
func analyzeDir(
	dir, tmpMount string,
	excludePatterns []string,
	stripTimes bool,
	threshold int64,
	out *[]*tarLayerSpec,
) (unallocated int64, skipDirsForParent []string, err error) {

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, nil, err
	}

	var size int64       // bytes in this subtree not yet covered by a child layer
	var skipSet []string // sub-layer dirs accumulated from children

	for _, e := range entries {
		child := filepath.Join(dir, e.Name())

		// Check exclusion patterns before doing any stat work.
		relPath := strings.TrimPrefix(filepath.ToSlash(child), filepath.ToSlash(tmpMount))
		relPath = strings.TrimPrefix(relPath, "/")
		if relPath != "" && matchesAnyExclude(relPath, excludePatterns) {
			continue
		}

		if e.IsDir() {
			subUnalloc, subSkip, subErr := analyzeDir(child, tmpMount, excludePatterns, stripTimes, threshold, out)
			if subErr != nil {
				return 0, nil, subErr
			}
			// Accumulate child skip dirs so we can forward them to the layer
			// (or further up) as needed.
			skipSet = append(skipSet, subSkip...)
			// Add the child's unallocated remainder to our own accumulator.
			size += subUnalloc
		} else {
			info, ieErr := e.Info()
			if ieErr != nil {
				// Unreadable entry — skip silently (consistent with buildRootTar).
				continue
			}
			size += info.Size()
		}
	}

	if size >= threshold {
		// This directory's unallocated content meets the threshold → emit a layer.
		// Build a human-readable description: the path relative to the FS root ("/").
		desc := "/" + strings.TrimPrefix(filepath.ToSlash(dir), filepath.ToSlash(tmpMount)+"/")
		layer := &tarLayerSpec{
			RootDir:         dir,
			Description:     desc,
			TotalSize:       size,
			SubLayerDirs:    skipSet,
			tmpMount:        tmpMount,
			excludePatterns: excludePatterns,
			stripTimes:      stripTimes,
		}
		*out = append(*out, layer)
		// Signal to parent: this dir is fully covered; parent must skip it.
		return 0, []string{dir}, nil
	}

	// Threshold not reached: bubble unallocated size and skip-set up to parent.
	return size, skipSet, nil
}
