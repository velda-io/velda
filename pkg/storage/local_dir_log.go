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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// LocalDirLogDb reads task logs from a local directory instead of from a remote
// instance. Logs are expected to be at {logDir}/{taskId}/stdout and stderr.
// This is used when the server has log_dir configured and the agent streams logs
// via the PushLogs RPC.
type LocalDirLogDb struct {
	logDir string
}

func NewLocalDirLogDb(logDir string) *LocalDirLogDb {
	return &LocalDirLogDb{logDir: logDir}
}

func (l *LocalDirLogDb) GetTaskLogs(ctx context.Context, instanceId int64, taskId string, options *ReadFileOptions) (stdout ByteStream, stderr ByteStream, err error) {
	stdout = readLocalFile(ctx, filepath.Join(l.logDir, taskId, "stdout"), options.Follow)
	stderr = readLocalFile(ctx, filepath.Join(l.logDir, taskId, "stderr"), options.Follow)
	return
}

// readLocalFile reads a file and sends chunks to a ByteStream.
// When follow is true it tails the file (waits for new data after EOF) until
// ctx is cancelled.
func readLocalFile(ctx context.Context, path string, follow bool) ByteStream {
	bs := ByteStream{
		Data: make(chan []byte, 64),
		Err:  make(chan error, 1),
	}
	go func() {
		defer close(bs.Data)
		var watcher *fsnotify.Watcher
		if follow {
			var err error
			watcher, err = fsnotify.NewWatcher()
			if err != nil {
				bs.Err <- fmt.Errorf("create watcher: %w", err)
				return
			}
			defer watcher.Close()
		}

		// Wait for file to exist when following (task may not have started yet).
		if follow {
			if err := waitForFile(ctx, path, watcher); err != nil {
				bs.Err <- err
				return
			}
		}

		f, err := os.Open(path)
		if os.IsNotExist(err) {
			// File doesn't exist and we're not following — just return EOF.
			bs.Err <- io.EOF
			return
		}
		if err != nil {
			bs.Err <- fmt.Errorf("open %s: %w", path, err)
			return
		}
		defer f.Close()

		if follow {
			if err := watcher.Add(path); err != nil {
				bs.Err <- fmt.Errorf("watch %s: %w", path, err)
				return
			}
		}

		buf := make([]byte, 32*1024)
		for {
			n, readErr := f.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				select {
				case bs.Data <- chunk:
				case <-ctx.Done():
					bs.Err <- ctx.Err()
					return
				}
			}
			if readErr == io.EOF {
				if !follow {
					bs.Err <- io.EOF
					return
				}
				// Follow mode: wait for new data or context cancellation.
				if err := waitForData(ctx, watcher); err != nil {
					bs.Err <- err
					return
				}
				continue
			}
			if readErr != nil {
				bs.Err <- readErr
				return
			}
		}
	}()
	return bs
}

// waitForFile blocks until the file at path exists or ctx is cancelled.
func waitForFile(ctx context.Context, path string, watcher *fsnotify.Watcher) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err == nil {
		_ = watcher.Add(dir)
	}
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-watcher.Events:
		case <-watcher.Errors:
		case <-time.After(500 * time.Millisecond):
			// Fallback poll in case inotify misses the event.
		}
	}
}

// waitForData waits until the watcher reports new data or ctx is cancelled.
func waitForData(ctx context.Context, watcher *fsnotify.Watcher) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-watcher.Events:
		return nil
	case err := <-watcher.Errors:
		return fmt.Errorf("watcher error: %w", err)
	case <-time.After(500 * time.Millisecond):
		// Fallback poll.
		return nil
	}
}
