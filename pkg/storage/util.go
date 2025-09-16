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
	// "bytes" // Removed unused import
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

// fileWatcher wraps fsnotify functionality with fallback to polling
type fileWatcher struct {
	watcher *fsnotify.Watcher
	path    string
	polling bool
}

// newFileWatcher creates a new file watcher that tries inotify first, falls back to polling
func newFileWatcher(path string) (*fileWatcher, error) {
	fw := &fileWatcher{path: path}

	// Try to create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Failed to create watcher, will use polling
		fw.polling = true
		return fw, nil
	}

	// Try to add the file to the watcher
	err = watcher.Add(path)
	if err != nil {
		// Failed to watch file, close watcher and fall back to polling
		watcher.Close()
		fw.polling = true
		return fw, nil
	}

	fw.watcher = watcher
	return fw, nil
}

// waitForChange waits for file changes using inotify or polling fallback
func (fw *fileWatcher) waitForChange(ctx context.Context) error {
	if fw.polling {
		// Fallback to polling
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return nil
		}
	}

	// Use inotify
	select {
	case <-ctx.Done():
		return ctx.Err()
	case event := <-fw.watcher.Events:
		// Check if it's a write or create event for our file
		if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
			return nil
		}
		// For other events, continue waiting
		return fw.waitForChange(ctx)
	case err := <-fw.watcher.Errors:
		// Error occurred, fall back to polling for this session
		_ = err // Ignore the specific error, just fall back to polling
		fw.close()
		fw.polling = true
		return fw.waitForChange(ctx)
	}
}

// close closes the file watcher
func (fw *fileWatcher) close() {
	if fw.watcher != nil {
		fw.watcher.Close()
		fw.watcher = nil
	}
}

// FileToByteStream opens path and returns a ByteStream that emits file contents in chunks.
// If options.Follow is true the reader will continue to watch for new content after EOF
// using inotify (with fallback to polling) until the provided context is cancelled.
func FileToByteStream(ctx context.Context, path string, options *ReadFileOptions) (ByteStream, error) {
	file, err := os.Open(path)
	if err != nil {
		return ByteStream{}, fmt.Errorf("failed to open file %s: %w", path, err)
	}

	data := make(chan []byte)
	errorO := make(chan error, 1) // buffered so goroutine can exit without blocking if nobody is receiving

	go func() {
		defer close(data)
		defer close(errorO)
		defer file.Close()

		// Determine follow behavior (context is provided by caller)
		follow := false
		if options != nil {
			follow = options.Follow
		}

		var watcher *fileWatcher
		if follow {
			// Set up file watcher for follow mode
			watcher, err = newFileWatcher(path)
			if err != nil {
				errorO <- fmt.Errorf("failed to create file watcher for %s: %w", path, err)
				return
			}
			defer watcher.close()
		}

		buf := make([]byte, 1024*10) // 10KB buffer

		for {
			// Check cancellation before read
			select {
			case <-ctx.Done():
				// propagate cancellation error and exit
				errorO <- ctx.Err()
				return
			default:
			}

			n, err := file.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case data <- chunk:
				case <-ctx.Done():
					errorO <- ctx.Err()
					return
				}
			}

			if err != nil {
				if err == io.EOF {
					if !follow {
						// non-follow mode: report EOF and finish
						errorO <- err
						return
					}
					// follow mode: wait for new data using file watcher or cancellation, then retry
					err = watcher.waitForChange(ctx)
					if err != nil {
						errorO <- err
						return
					}
					continue
				}
				// unexpected error
				errorO <- fmt.Errorf("failed to read file %s: %w", path, err)
				return
			}
		}
	}()

	return ByteStream{Data: data, Err: errorO}, nil
}
