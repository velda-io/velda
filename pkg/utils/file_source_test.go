// Copyright 2026 Velda Inc
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
package utils

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAWSSecretURI(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantRegion  string
		wantSecret  string
		wantVersion string
		expectErr   bool
	}{
		{
			name:        "version omitted uses current",
			input:       "awssecret://us-west-2/my-secret",
			wantRegion:  "us-west-2",
			wantSecret:  "my-secret",
			wantVersion: awsCurrentVersionTag,
		},
		{
			name:        "explicit version",
			input:       "awssecret://us-east-1/my-secret/v1",
			wantRegion:  "us-east-1",
			wantSecret:  "my-secret",
			wantVersion: "v1",
		},
		{
			name:        "long-name",
			input:       "awssecret://us-east-1/my/long/secret/name/v2",
			wantRegion:  "us-east-1",
			wantSecret:  "my/long/secret/name",
			wantVersion: "v2",
		},
		{
			name:        "encoded-name",
			input:       "awssecret://us-east-1/my%2Flong%2Fsecret%2Fname/v2",
			wantRegion:  "us-east-1",
			wantSecret:  "my/long/secret/name",
			wantVersion: "v2",
		},
		{
			name:        "long-name-without-version",
			input:       "awssecret://us-east-1/my/long/secret/name/",
			wantRegion:  "us-east-1",
			wantSecret:  "my/long/secret/name",
			wantVersion: awsCurrentVersionTag,
		},
		{
			name:      "missing region",
			input:     "awssecret:///my-secret/v1",
			expectErr: true,
		},
		{
			name:      "missing secret name",
			input:     "awssecret://us-east-1",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := parseAWSSecretURI(tt.input)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAWSSecretURI() error = %v", err)
			}
			if ref.region != tt.wantRegion {
				t.Fatalf("region = %q, want %q", ref.region, tt.wantRegion)
			}
			if ref.name != tt.wantSecret {
				t.Fatalf("secret = %q, want %q", ref.name, tt.wantSecret)
			}
			if ref.version != tt.wantVersion {
				t.Fatalf("version = %q, want %q", ref.version, tt.wantVersion)
			}
		})
	}
}

func TestLoadFileOrSecretLocalFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "cert.pem")
	want := []byte("local file content")
	if err := os.WriteFile(path, want, 0o644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	got, err := LoadFileOrSecret(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadFileOrSecret() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("content mismatch: got %q, want %q", string(got), string(want))
	}
}
