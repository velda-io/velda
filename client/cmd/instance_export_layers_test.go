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
	"slices"
	"sort"
	"testing"
)

// writeFile creates all necessary parent directories and writes content to
// the given path. Sizes need not be exact; the file content is padded with
// zeros to reach the requested byte count.
func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// rootDirs returns the RootDir of every layer spec, relative to tmpMount.
func rootDirs(layers []*tarLayerSpec, tmpMount string) []string {
	out := make([]string, len(layers))
	for i, l := range layers {
		rel, _ := filepath.Rel(tmpMount, l.RootDir)
		out[i] = rel
	}
	return out
}

// subLayerRels converts a layer's SubLayerDirs to paths relative to tmpMount.
func subLayerRels(l *tarLayerSpec, tmpMount string) []string {
	out := make([]string, len(l.SubLayerDirs))
	for i, d := range l.SubLayerDirs {
		rel, _ := filepath.Rel(tmpMount, d)
		out[i] = rel
	}
	sort.Strings(out)
	return out
}

// TestComputeLayers_ZeroThreshold verifies that a zero/negative threshold
// always returns exactly one layer covering the whole tree.
func TestComputeLayers_ZeroThreshold(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "a", "file.txt"), 1024)
	writeFile(t, filepath.Join(tmp, "b", "file.txt"), 1024)

	for _, thresh := range []int64{0, -1} {
		layers, err := computeLayers(tmp, nil, false, thresh)
		if err != nil {
			t.Fatalf("threshold=%d: unexpected error: %v", thresh, err)
		}
		if len(layers) != 1 {
			t.Fatalf("threshold=%d: want 1 layer, got %d", thresh, len(layers))
		}
		if layers[0].RootDir != tmp {
			t.Errorf("threshold=%d: want RootDir=%s, got %s", thresh, tmp, layers[0].RootDir)
		}
		if len(layers[0].SubLayerDirs) != 0 {
			t.Errorf("threshold=%d: want no SubLayerDirs, got %v", thresh, layers[0].SubLayerDirs)
		}
	}
}

// TestComputeLayers_AllBelowThreshold verifies that when the total filesystem
// size is below the threshold, a single root layer is produced.
func TestComputeLayers_AllBelowThreshold(t *testing.T) {
	tmp := t.TempDir()
	// 30 + 30 = 60 bytes total, threshold = 200
	writeFile(t, filepath.Join(tmp, "dir1", "a.txt"), 30)
	writeFile(t, filepath.Join(tmp, "dir2", "b.txt"), 30)

	layers, err := computeLayers(tmp, nil, false, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("want 1 layer, got %d: %v", len(layers), rootDirs(layers, tmp))
	}
	if layers[0].RootDir != tmp {
		t.Errorf("want root layer, got RootDir=%s", layers[0].RootDir)
	}
	if len(layers[0].SubLayerDirs) != 0 {
		t.Errorf("want no SubLayerDirs, got %v", layers[0].SubLayerDirs)
	}
}

// TestComputeLayers_OneDirectoryExceedsThreshold verifies that a single large
// directory is split into its own layer while remaining content stays in the
// root layer.
//
// Layout (threshold = 100 bytes):
//
//	tmpMount/
//	  small.txt          10 B
//	  big/
//	    file1.txt        60 B
//	    file2.txt        60 B   → big/ total 120 B ≥ 100 → own layer
//
// Expected: 2 layers — root (SubLayerDirs=[big]) and big/.
func TestComputeLayers_OneDirectoryExceedsThreshold(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "small.txt"), 10)
	writeFile(t, filepath.Join(tmp, "big", "file1.txt"), 60)
	writeFile(t, filepath.Join(tmp, "big", "file2.txt"), 60)

	const threshold = 100
	layers, err := computeLayers(tmp, nil, false, threshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("want 2 layers, got %d: %v", len(layers), rootDirs(layers, tmp))
	}

	// After reversal: layers[0] = root, layers[1] = big/
	root, big := layers[0], layers[1]

	if root.RootDir != tmp {
		t.Errorf("layers[0]: want RootDir=%s, got %s", tmp, root.RootDir)
	}
	wantSkip := []string{"big"}
	if got := subLayerRels(root, tmp); !slices.Equal(got, wantSkip) {
		t.Errorf("layers[0] SubLayerDirs: want %v, got %v", wantSkip, got)
	}

	wantBigRoot := filepath.Join(tmp, "big")
	if big.RootDir != wantBigRoot {
		t.Errorf("layers[1]: want RootDir=%s, got %s", wantBigRoot, big.RootDir)
	}
	if len(big.SubLayerDirs) != 0 {
		t.Errorf("layers[1]: want no SubLayerDirs, got %v", big.SubLayerDirs)
	}
	if big.TotalSize < 120 {
		t.Errorf("layers[1]: TotalSize=%d, want ≥ 120", big.TotalSize)
	}
}

// TestComputeLayers_MultipleDirectoriesExceedThreshold verifies that several
// sibling directories each exceeding the threshold each get their own layer.
//
// Layout (threshold = 50 bytes):
//
//	tmpMount/
//	  a/
//	    file.txt   60 B   → own layer
//	  b/
//	    file.txt   60 B   → own layer
//	  c/
//	    file.txt   60 B   → own layer
//	  root.txt     5  B   → stays in root layer
//
// Expected: 4 layers total.
func TestComputeLayers_MultipleDirectoriesExceedThreshold(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "a", "file.txt"), 60)
	writeFile(t, filepath.Join(tmp, "b", "file.txt"), 60)
	writeFile(t, filepath.Join(tmp, "c", "file.txt"), 60)
	writeFile(t, filepath.Join(tmp, "root.txt"), 5)

	const threshold = 50
	layers, err := computeLayers(tmp, nil, false, threshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 4 {
		t.Fatalf("want 4 layers, got %d: %v", len(layers), rootDirs(layers, tmp))
	}

	// Root layer must be first and have all three big dirs in SubLayerDirs.
	root := layers[0]
	if root.RootDir != tmp {
		t.Errorf("layers[0]: want root, got RootDir=%s", root.RootDir)
	}
	gotSkip := subLayerRels(root, tmp)
	wantSkip := []string{"a", "b", "c"}
	sort.Strings(gotSkip)
	if !slices.Equal(gotSkip, wantSkip) {
		t.Errorf("root SubLayerDirs: want %v, got %v", wantSkip, gotSkip)
	}

	// The other three layers must each be one of a/, b/, c/.
	gotRoots := rootDirs(layers[1:], tmp)
	sort.Strings(gotRoots)
	wantRoots := []string{"a", "b", "c"}
	if !slices.Equal(gotRoots, wantRoots) {
		t.Errorf("leaf layer roots: want %v, got %v", wantRoots, gotRoots)
	}
}

// TestComputeLayers_NestedDirectories verifies the bottom-up splitting logic
// when a large child directory triggers its own layer so its parent, which
// would otherwise exceed the threshold only by counting the child, stays below.
//
// Layout (threshold = 100 bytes):
//
//	tmpMount/
//	  outer/
//	    inner/
//	      file.txt   120 B   → inner/ own layer (≥ 100)
//	    sibling.txt   20 B   → stays in outer/ (unalloc from inner is 0)
//	  top.txt          5 B
//
// After inner/ is claimed: outer/ has only 20 B unallocated → below threshold.
// Root layer ends up with: outer/ content (20 B + 5 B top.txt), SubLayerDirs=[inner/].
// Expected: 2 layers — root (covers outer/ minus inner/) and inner/.
func TestComputeLayers_NestedDirectories(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "outer", "inner", "file.txt"), 120)
	writeFile(t, filepath.Join(tmp, "outer", "sibling.txt"), 20)
	writeFile(t, filepath.Join(tmp, "top.txt"), 5)

	const threshold = 100
	layers, err := computeLayers(tmp, nil, false, threshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("want 2 layers, got %d: %v", len(layers), rootDirs(layers, tmp))
	}

	root, inner := layers[0], layers[1]

	if root.RootDir != tmp {
		t.Errorf("layers[0]: want root RootDir=%s, got %s", tmp, root.RootDir)
	}
	wantSkip := []string{filepath.Join("outer", "inner")}
	if got := subLayerRels(root, tmp); !slices.Equal(got, wantSkip) {
		t.Errorf("root SubLayerDirs: want %v, got %v", wantSkip, got)
	}

	wantInnerRoot := filepath.Join(tmp, "outer", "inner")
	if inner.RootDir != wantInnerRoot {
		t.Errorf("layers[1]: want RootDir=%s, got %s", wantInnerRoot, inner.RootDir)
	}
}

// TestComputeLayers_ExcludePatterns verifies that excluded paths are not
// counted toward the layer-split threshold.
//
// Layout (threshold = 50 bytes):
//
//	tmpMount/
//	  excluded/
//	    big.txt   200 B   ← excluded via pattern
//	  real/
//	    file.txt   20 B
//
// Without the exclusion, excluded/ would become its own layer.
// With the exclusion, total counted size is only 20 B → single root layer.
func TestComputeLayers_ExcludePatterns(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "excluded", "big.txt"), 200)
	writeFile(t, filepath.Join(tmp, "real", "file.txt"), 20)

	const threshold = 50
	layers, err := computeLayers(tmp, []string{"/excluded"}, false, threshold)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("want 1 layer (excluded dir should not split), got %d: %v", len(layers), rootDirs(layers, tmp))
	}
	if layers[0].RootDir != tmp {
		t.Errorf("want root layer, got %s", layers[0].RootDir)
	}
}
