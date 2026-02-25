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
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"sigs.k8s.io/yaml"

	pb "velda.io/velda/pkg/proto"
)

const defaultExportConfigPath = "/.dockerexports"

// loadExportConfig reads the ExportConfig at configPath (or the default
// /.dockerexports if configPath is ""), recursively resolves any include
// entries, and returns a merged ExportConfig. If the default path does not
// exist the function returns an empty config without error.
//
// visited tracks already-processed sources to break include cycles.
func loadExportConfig(configPath string) (*pb.ExportConfig, error) {
	if configPath == "" {
		configPath = defaultExportConfigPath
	}
	visited := make(map[string]bool)
	return loadExportConfigFrom(configPath, visited, true /*isRoot*/)
}

// loadExportConfigFrom loads and merges an ExportConfig from a single source.
// isRoot controls whether a missing file is silently ignored (true for the
// default config path) or returns an error (false for explicit includes).
func loadExportConfigFrom(source string, visited map[string]bool, isRoot bool) (*pb.ExportConfig, error) {
	if visited[source] {
		return &pb.ExportConfig{}, nil
	}
	visited[source] = true

	data, err := readConfigSource(source)
	if err != nil {
		if isRoot && os.IsNotExist(err) {
			// Default config file simply doesn't exist – that's fine.
			return &pb.ExportConfig{}, nil
		}
		return nil, fmt.Errorf("loading export config %q: %w", source, err)
	}

	cfg, err := parseExportConfigYAML(data)
	if err != nil {
		return nil, fmt.Errorf("parsing export config %q: %w", source, err)
	}

	// Resolve and merge all includes first so that the current config's
	// settings override them.
	merged := &pb.ExportConfig{}
	for _, inc := range cfg.Include {
		// Relative local paths are resolved relative to the parent config's
		// directory (only meaningful for file-based sources).
		resolved := resolveIncludePath(inc, source)
		incCfg, err := loadExportConfigFrom(resolved, visited, false)
		if err != nil {
			return nil, err
		}
		mergeExportConfig(merged, incCfg)
	}
	// Apply this config on top.
	mergeExportConfig(merged, cfg)
	return merged, nil
}

// readConfigSource fetches raw bytes from a local path or an HTTPS URL.
func readConfigSource(source string) ([]byte, error) {
	if strings.HasPrefix(source, "https://") {
		resp, err := http.Get(source) //nolint:gosec // intentional user-supplied URL
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, source)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(source)
}

// parseExportConfigYAML converts YAML bytes to an ExportConfig proto message.
// It uses sigs.k8s.io/yaml to first transcode YAML → JSON, then protojson to
// unmarshal into the proto struct (preserving field name conventions).
func parseExportConfigYAML(data []byte) (*pb.ExportConfig, error) {
	jsonBytes, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, err
	}
	cfg := &pb.ExportConfig{}
	if err := protojson.Unmarshal(jsonBytes, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// mergeExportConfig appends src's fields into dst in place.
// Repeated fields are concatenated; later entries win for any future scalar
// fields introduced in the schema.
func mergeExportConfig(dst, src *pb.ExportConfig) {
	dst.Exclude = append(dst.Exclude, src.Exclude...)
	// strip_times is OR'd: if any included config enables it, it stays enabled.
	if src.StripTimes {
		dst.StripTimes = true
	}
	// include is only used during loading; no need to propagate it.
}

// resolveIncludePath makes a relative include path absolute relative to the
// directory of parent (only when parent is a local file path, not a URL).
func resolveIncludePath(inc, parent string) string {
	if strings.HasPrefix(inc, "https://") || strings.HasPrefix(inc, "/") {
		return inc
	}
	if strings.HasPrefix(parent, "https://") {
		// Relative paths inside a remote config are not supported.
		return inc
	}
	return path.Join(path.Dir(parent), inc)
}

// matchesAnyExclude reports whether the given tar-relative path (no leading
// slash) matches at least one of the glob patterns in the exclusion list.
//
// Pattern semantics:
//   - Leading "/" → absolute: the pattern (with the slash stripped) is matched
//     against the full tar-relative path anchored at the root.
//   - No leading "/" → relative: the pattern is compared against every
//     trailing path segment (and sub-path from that segment onward), so the
//     pattern matches at any depth in the tree.
//     e.g. "cache" matches "var/cache" and "home/user/cache",
//     but NOT "home/user/.cache" (dot makes it a different name).
//     e.g. "*.log" matches any .log file at any depth.
//
// In both cases a matched directory implicitly excludes its entire subtree.
func matchesAnyExclude(tarPath string, patterns []string) bool {
	// Normalise: remove trailing slash so directory entries are compared
	// the same way as file entries.
	p := strings.TrimSuffix(tarPath, "/")
	for _, pattern := range patterns {
		pat := strings.TrimSuffix(pattern, "/")
		if strings.HasPrefix(pat, "/") {
			// Absolute pattern: match anchored at the root.
			pat = strings.TrimPrefix(pat, "/")
			if matchGlobAndSubtree(pat, p) {
				return true
			}
		} else {
			// Relative pattern: try against every trailing sub-path so the
			// pattern can match at any depth in the tree.
			// e.g. "cache" matches "var/cache" and "home/user/cache"
			//      but NOT "home/user/.cache" (dot is part of the name).
			remaining := p
			for {
				if matchGlobAndSubtree(pat, remaining) {
					return true
				}
				idx := strings.IndexByte(remaining, '/')
				if idx < 0 {
					break
				}
				remaining = remaining[idx+1:]
			}
		}
	}
	return false
}

// matchGlobAndSubtree returns true if path.Match(pat, p) is true, or if p
// starts with pat+"/" (meaning p lives inside the matched directory).
func matchGlobAndSubtree(pat, p string) bool {
	if ok, _ := path.Match(pat, p); ok {
		return true
	}
	return strings.HasPrefix(p, pat+"/")
}
