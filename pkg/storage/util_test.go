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
package storage

import (
	"context"
	"io"
	"os"
	"testing"
	"time"
)

func TestFileToByteStreamFollowMode(t *testing.T) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "test_follow_*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Write initial content
	initialContent := "Initial content\n"
	if _, err := tmpFile.WriteString(initialContent); err != nil {
		t.Fatalf("Failed to write initial content: %v", err)
	}
	tmpFile.Sync()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start reading in follow mode
	options := &ReadFileOptions{Follow: true}
	stream, err := FileToByteStream(ctx, tmpFile.Name(), options)
	if err != nil {
		t.Fatalf("Failed to create byte stream: %v", err)
	}

	// Read the initial content
	var receivedData []byte
	timer := time.NewTimer(100 * time.Millisecond)

readLoop:
	for {
		select {
		case data, ok := <-stream.Data:
			if !ok {
				break readLoop
			}
			receivedData = append(receivedData, data...)
			timer.Reset(100 * time.Millisecond)
		case err := <-stream.Err:
			if err != nil && err != io.EOF && err != context.DeadlineExceeded {
				t.Fatalf("Unexpected error from stream: %v", err)
			}
			if err == context.DeadlineExceeded {
				break readLoop
			}
		case <-timer.C:
			// No more data for a while, move to next phase
			break readLoop
		}
	}

	if string(receivedData) != initialContent {
		t.Errorf("Expected initial content %q, got %q", initialContent, string(receivedData))
	}

	// Write more content to test follow functionality
	additionalContent := "Additional content\n"
	go func() {
		time.Sleep(50 * time.Millisecond)
		if _, err := tmpFile.WriteString(additionalContent); err != nil {
			t.Errorf("Failed to write additional content: %v", err)
		}
		tmpFile.Sync()
	}()

	// Continue reading to get the additional content
	receivedData = receivedData[:0] // Reset
	timer.Reset(200 * time.Millisecond)

readLoop2:
	for {
		select {
		case data, ok := <-stream.Data:
			if !ok {
				break readLoop2
			}
			receivedData = append(receivedData, data...)
		case err := <-stream.Err:
			if err != nil && err != context.DeadlineExceeded {
				t.Fatalf("Unexpected error from stream: %v", err)
			}
			if err == context.DeadlineExceeded {
				break readLoop2
			}
		case <-timer.C:
			break readLoop2
		}
	}

	if string(receivedData) != additionalContent {
		t.Errorf("Expected additional content %q, got %q", additionalContent, string(receivedData))
	}
}

func TestFileWatcherFallbackToPolling(t *testing.T) {
	// Create a temporary file
	tmpFile, err := os.CreateTemp("", "test_watcher_*.log")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// Test creating a watcher
	watcher, err := newFileWatcher(tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to create file watcher: %v", err)
	}
	defer watcher.close()

	// The watcher should either use inotify or fallback to polling
	// This test mainly ensures the watcher can be created without errors
	if watcher == nil {
		t.Error("File watcher should not be nil")
	}
}
