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
package cases

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testScpCommand(t *testing.T, r Runner) {
	// Create a temporary test file
	testContent := "This is a test file for SCP command testing"
	tmpFile, err := os.CreateTemp("", "velda-scp-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	t.Cleanup(func() { assert.NoError(t, os.Remove(tmpFile.Name())) })

	if _, err := tmpFile.WriteString(testContent); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}
	instanceName := r.CreateTestInstance(t, "scp-test-instance", "")

	// Test case 1: Basic file upload to instance
	// Note: This assumes a test instance is available, otherwise this will fail
	t.Run("BasicFileUpload", func(t *testing.T) {
		destPath := "/scp-test-file.txt"

		// Upload the file to the instance
		require.NoError(t, runVelda("scp", "-u", "root", tmpFile.Name(), fmt.Sprintf("%s:%s", instanceName, destPath)))

		// Download the file back to verify contents
		downloadDir, err := os.MkdirTemp("", "velda-scp-verification")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(downloadDir)

		require.NoError(t, runVelda("scp", "-u", "root", fmt.Sprintf("%s:%s", instanceName, destPath), downloadDir))

		// Read the downloaded file
		downloadedContent, err := os.ReadFile(filepath.Join(downloadDir, filepath.Base(destPath)))
		if err != nil {
			t.Fatalf("Failed to read downloaded file: %v", err)
		}

		assert.Equal(t, testContent, string(downloadedContent), "File content should match original")
	})

	// Test case 2: Test with recursive flag
	t.Run("RecursiveUpload", func(t *testing.T) {
		// Create a temporary directory with multiple files
		tmpDir, err := os.MkdirTemp("", "velda-scp-test-dir")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		// Create a few files in the directory
		for i := 1; i <= 3; i++ {
			file, err := os.Create(filepath.Join(tmpDir, fmt.Sprintf("file%d.txt", i)))
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}
			_, _ = file.WriteString(fmt.Sprintf("Content of file %d", i))
			_ = file.Close()
		}

		destPath := "/scp-test-dir"

		// Upload directory recursively
		require.NoError(t, runVelda("scp", "-u", "root", "-r", tmpDir, fmt.Sprintf("%s:%s", instanceName, destPath)))

		// Download directory back to verify
		downloadDir, err := os.MkdirTemp("", "velda-scp-download-verify")
		if err != nil {
			t.Fatalf("Failed to create temp directory for download: %v", err)
		}
		defer os.RemoveAll(downloadDir)

		require.NoError(t, runVelda("scp", "-u", "root", "-r", fmt.Sprintf("%s:%s", instanceName, destPath), downloadDir))

		// Count the files in the downloaded directory
		var fileCount int
		err = filepath.Walk(filepath.Join(downloadDir, filepath.Base(destPath)), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				fileCount++
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Failed to walk downloaded directory: %v", err)
		}

		assert.Equal(t, 3, fileCount, "Should have downloaded 3 files")
	})
	// Test case 3: Test preserve options
	t.Run("PreserveOptions", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "velda-scp-preserve-*.txt")
		if err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		content := "Preserve test file content"
		if _, err := tmpFile.WriteString(content); err != nil {
			t.Fatalf("Failed to write to temp file: %v", err)
		}
		if err := tmpFile.Close(); err != nil {
			t.Fatalf("Failed to close temp file: %v", err)
		}

		// Set custom permissions and timestamps
		origMode := os.FileMode(0640)
		if err := os.Chmod(tmpFile.Name(), origMode); err != nil {
			t.Fatalf("Failed to chmod: %v", err)
		}
		atime := time.Now().Add(-2 * time.Hour)
		mtime := time.Now().Add(-1 * time.Hour)
		if err := os.Chtimes(tmpFile.Name(), atime, mtime); err != nil {
			t.Fatalf("Failed to chtimes: %v", err)
		}

		destPath := "/scp-preserve-test.txt"

		// Upload with preserve flags
		require.NoError(t, runVelda("scp", "-u", "root", "-p", tmpFile.Name(), fmt.Sprintf("%s:%s", instanceName, destPath)))

		// Download back with preserve flags
		downloadDir, err := os.MkdirTemp("", "velda-scp-preserve-download")
		if err != nil {
			t.Fatalf("Failed to create temp directory: %v", err)
		}
		defer os.RemoveAll(downloadDir)

		require.NoError(t, runVelda("scp", "-u", "root", "-p", fmt.Sprintf("%s:%s", instanceName, destPath), downloadDir))

		downloadedPath := filepath.Join(downloadDir, filepath.Base(destPath))
		stat, err := os.Stat(downloadedPath)
		if err != nil {
			t.Fatalf("Failed to stat downloaded file: %v", err)
		}
		assert.Equal(t, origMode, stat.Mode().Perm(), "File mode should be preserved")

		// Check timestamps (allowing 2s slack for transfer)
		info, err := os.Stat(downloadedPath)
		if err != nil {
			t.Fatalf("Failed to stat downloaded file: %v", err)
		}
		dt := info.ModTime().Sub(mtime)
		assert.LessOrEqual(t, dt.Abs().Seconds(), 2.0, "Modification time should be preserved")

		// Check content
		data, err := os.ReadFile(downloadedPath)
		if err != nil {
			t.Fatalf("Failed to read downloaded file: %v", err)
		}
		assert.Equal(t, content, string(data), "File content should match")
	})
}
