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
	"testing"
)

func TestMatchesAnyExclude(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		want     bool
	}{
		// --- Absolute patterns (leading "/") ---------------------------------
		{
			name:     "absolute exact match",
			path:     "tmp",
			patterns: []string{"/tmp"},
			want:     true,
		},
		{
			name:     "absolute subtree match",
			path:     "tmp/foo/bar",
			patterns: []string{"/tmp"},
			want:     true,
		},
		{
			name:     "absolute glob match",
			path:     "tmp/foo",
			patterns: []string{"/tmp/*"},
			want:     true,
		},
		{
			name:     "absolute glob does not match deeper nesting",
			path:     "tmp/foo/bar",
			patterns: []string{"/tmp/*"},
			want:     false,
		},
		{
			name:     "absolute does not match same name at other depth",
			path:     "var/tmp",
			patterns: []string{"/tmp"},
			want:     false,
		},
		{
			name:     "absolute does not match different root",
			path:     "var/cache",
			patterns: []string{"/tmp"},
			want:     false,
		},

		// --- Relative patterns (no leading "/") ------------------------------
		{
			name:     "relative matches top-level exact",
			path:     "tmp",
			patterns: []string{"tmp"},
			want:     true,
		},
		{
			name:     "relative matches nested exact",
			path:     "var/cache",
			patterns: []string{"cache"},
			want:     true,
		},
		{
			name:     "relative matches deeply nested",
			path:     "home/user/cache",
			patterns: []string{"cache"},
			want:     true,
		},
		{
			name:     "relative matches files inside matched directory",
			path:     "var/cache/apt/lists",
			patterns: []string{"cache"},
			want:     true,
		},
		{
			name:     "relative glob matches any depth",
			path:     "home/user/foo.log",
			patterns: []string{"*.log"},
			want:     true,
		},
		{
			name:     "relative glob matches top-level",
			path:     "debug.log",
			patterns: []string{"*.log"},
			want:     true,
		},
		{
			name:     "relative glob matches subtree of glob-matched dir",
			path:     "home/user/logs/server.log",
			patterns: []string{"*.log"},
			want:     true,
		},
		{
			name:     "relative multi-segment pattern matches at depth",
			path:     "var/cache/apt",
			patterns: []string{"cache/apt"},
			want:     true,
		},
		{
			name:     "relative multi-segment pattern subtree",
			path:     "var/cache/apt/lists",
			patterns: []string{"cache/apt"},
			want:     true,
		},

		// --- Dot-prefix semantics: "cache" must NOT match ".cache" -----------
		{
			name:     "dot-prefixed name is not matched by plain pattern",
			path:     "home/user/.cache",
			patterns: []string{"cache"},
			want:     false,
		},
		{
			name:     "files inside dot-prefixed dir not matched by plain pattern",
			path:     "home/user/.cache/chromium/Default",
			patterns: []string{"cache"},
			want:     false,
		},
		{
			name:     "dot pattern matches dot-prefixed name",
			path:     "home/user/.cache",
			patterns: []string{".cache"},
			want:     true,
		},
		{
			name:     "dot pattern subtree",
			path:     "home/user/.cache/foo",
			patterns: []string{".cache"},
			want:     true,
		},
		{
			name:     "dot pattern does not match plain name",
			path:     "home/user/cache",
			patterns: []string{".cache"},
			want:     false,
		},

		// --- Trailing slash normalisation ------------------------------------
		{
			name:     "path with trailing slash still matches",
			path:     "tmp/",
			patterns: []string{"/tmp"},
			want:     true,
		},
		{
			name:     "pattern with trailing slash still matches",
			path:     "tmp",
			patterns: []string{"/tmp/"},
			want:     true,
		},

		// --- No match --------------------------------------------------------
		{
			name:     "no patterns",
			path:     "tmp/foo",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "unrelated path",
			path:     "usr/bin/bash",
			patterns: []string{"/tmp", "cache", "*.log"},
			want:     false,
		},

		// --- Multiple patterns -----------------------------------------------
		{
			name:     "first pattern matches",
			path:     "tmp/work",
			patterns: []string{"/tmp", "/opt"},
			want:     true,
		},
		{
			name:     "second pattern matches",
			path:     "opt/data",
			patterns: []string{"/tmp", "/opt"},
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesAnyExclude(tc.path, tc.patterns)
			if got != tc.want {
				t.Errorf("matchesAnyExclude(%q, %v) = %v, want %v",
					tc.path, tc.patterns, got, tc.want)
			}
		})
	}
}
