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

//go:build linux

package sandboxfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/fileserver"
)

func getOFDLock(fd uintptr, lk *syscall.Flock_t) error {
	return syscall.FcntlFlock(fd, unix.F_OFD_GETLK, lk)
}

func mountDirectFSRWClientForTest(client *DirectFSClient, mountPoint string) (*VeldaServer, error) {
	fh, attr, err := client.Connect()
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	root := &RWNode{client: client, fh: fh, attr: &attr}
	client.registerInodeHandler(attr.Ino, root)

	timeout := 1 * time.Hour
	opts := &fs.Options{
		EntryTimeout: &timeout,
		AttrTimeout:  &timeout,
		MountOptions: fuse.MountOptions{
			AllowOther:  true,
			DirectMount: true,
			Name:        "veldafs-rw",
			FsName:      "directfs-rw",
			Options:     []string{"default_permissions"},
			Debug:       true,
		},
	}

	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to mount: %w", err)
	}

	client.fuseServer = server

	return &VeldaServer{
		Server: server,
		Cache:  client.cache,
		debug:  client.debug,
	}, nil
}

func setupRWDirectFSEnv(t *testing.T) *testEnv {
	checkCapSysAdmin(t)
	env := setupTestEnv(t)

	server := fileserver.NewFileServer(env.srcDir, 4)
	require.NoError(t, server.Start("localhost:0"))
	time.Sleep(100 * time.Millisecond)

	cache, err := NewDirectoryCacheManager(env.cacheDir)
	require.NoError(t, err)

	client := NewDirectFSClient(server.Addr().String(), cache, nil, true)
	veldaServer, err := mountDirectFSRWClientForTest(client, env.mountDir)
	require.NoError(t, err)
	require.NoError(t, veldaServer.WaitMount())
	time.Sleep(150 * time.Millisecond)

	t.Cleanup(func() {
		require.NoError(t, client.Unmount())
		client.Stop()
		server.Stop()
	})

	return env
}

// TestRWFileSequentialWrites tests that sequential writes work
func TestRWFileSequentialWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "sequential.txt")

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Write sequentially in chunks
	chunks := [][]byte{
		[]byte("First chunk "),
		[]byte("Second chunk "),
		[]byte("Third chunk "),
	}

	expectedContent := bytes.Join(chunks, nil)
	totalWritten := 0

	for _, chunk := range chunks {
		n, err := f.Write(chunk)
		require.NoError(t, err)
		totalWritten += n
	}

	// Flush to persist writes
	err = f.Sync()
	require.NoError(t, err)

	// Reopen and read to verify content
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, expectedContent, content)
}

// TestRWFileNonSequentialWrites tests that non-sequential writes abort caching
func TestRWFileNonSequentialWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}
	GlobalCacheMetrics = NewCacheMetrics()

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "nonseq_rw.txt")

	initialAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)

	// Open file for writing
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Write at offset 0
	_, err = f.WriteAt([]byte("start"), 0)
	require.NoError(t, err)

	// Write at non-sequential offset (should abort caching)
	_, err = f.WriteAt([]byte("end"), 100)
	require.NoError(t, err)

	// Flush
	err = f.Sync()
	require.NoError(t, err)

	newAborted := getCounterValue(GlobalCacheMetrics.CacheAborted)
	assert.Greater(t, newAborted, initialAborted, "Non-sequential write should increase CacheAborted")
}

// TestRWFileWriteAndRead tests writing then reading from the same file
func TestRWFileWriteAndRead(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "write_and_read.txt")
	testContent := []byte("Test content for write and read")

	// Write file
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)

	n, err := f.Write(testContent)
	require.NoError(t, err)
	assert.Equal(t, len(testContent), n)

	f.Close()

	// Read file back
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, testContent, content)
}

// TestRWFileMultipleWrites tests multiple write operations to the same file
func TestRWFileMultipleWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "multi_write.txt")

	// Write 1: Initial content
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)

	content1 := []byte("First write")
	_, err = f.Write(content1)
	require.NoError(t, err)
	f.Close()

	// Verify first write
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, content1, content)

	// Write 2: Truncate and write new content
	f, err = os.OpenFile(testFile, os.O_WRONLY|os.O_TRUNC, 0644)
	require.NoError(t, err)

	content2 := []byte("Second write with longer content")
	_, err = f.Write(content2)
	require.NoError(t, err)
	f.Close()

	// Verify second write
	content, err = os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, content2, content)
}

// TestRWFileLargeWrite tests writing large content
func TestRWFileLargeWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "large_write.txt")

	// Create large content (10 MB)
	largeContent := make([]byte, 10*1024*1024)
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}

	// Write large file
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	totalWritten := 0
	for totalWritten < len(largeContent) {
		n, err := f.Write(largeContent[totalWritten:])
		require.NoError(t, err)
		totalWritten += n
	}

	// Sync/flush
	err = f.Sync()
	require.NoError(t, err)

	// Verify file size
	stat, err := os.Stat(testFile)
	require.NoError(t, err)
	assert.Equal(t, int64(len(largeContent)), stat.Size())
}

// TestRWFileFlushSemantics tests flush operation semantics
func TestRWFileFlushSemantics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "flush_test.txt")

	// Open file
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)

	// Write some data
	data := []byte("Test data before flush")
	_, err = f.Write(data)
	require.NoError(t, err)

	// Multiple flushes should be safe
	err = f.Sync()
	require.NoError(t, err)

	err = f.Sync()
	require.NoError(t, err)

	f.Close()

	// Verify content is persisted
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, data, content)
}

// TestRWFileAppendMode tests opening file in append mode
func TestRWFileAppendMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "append_test.txt")

	// Initial write
	err := os.WriteFile(testFile, []byte("Initial"), 0644)
	require.NoError(t, err)

	// Append
	f, err := os.OpenFile(testFile, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	appendContent := []byte(" Appended")
	_, err = f.Write(appendContent)
	require.NoError(t, err)

	f.Close()

	// Verify
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "Initial Appended", string(content))
}

// TestRWFileWriteAfterRead tests writing after reading the same file
func TestRWFileWriteAfterRead(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "read_write.txt")

	// Initial write
	initialContent := []byte("Initial content")
	err := os.WriteFile(testFile, initialContent, 0644)
	require.NoError(t, err)

	// Read
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, initialContent, content)

	// Now write again (truncate)
	newContent := []byte("New content after read")
	err = os.WriteFile(testFile, newContent, 0644)
	require.NoError(t, err)

	// Verify new content
	content, err = os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, newContent, content)
}

// TestRWFileConcurrentWrites tests multiple concurrent writes to different files
func TestRWFileConcurrentWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	numFiles := 10
	done := make(chan error, numFiles)

	for i := 0; i < numFiles; i++ {
		go func(idx int) {
			testFile := filepath.Join(env.mountDir, "concurrent_"+string(rune('a'+idx))+".txt")
			content := []byte("Content for file " + string(rune('a'+idx)))

			err := os.WriteFile(testFile, content, 0644)
			if err != nil {
				done <- err
				return
			}

			readBack, err := os.ReadFile(testFile)
			if err != nil {
				done <- err
				return
			}

			if !bytes.Equal(readBack, content) {
				done <- os.ErrInvalid
				return
			}

			done <- nil
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numFiles; i++ {
		err := <-done
		require.NoError(t, err)
	}
}

// TestRWFilePartialRead tests reading partial content from written file
func TestRWFilePartialRead(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "partial_read.txt")
	fullContent := []byte("This is a test file for partial reads")

	// Write full content
	err := os.WriteFile(testFile, fullContent, 0644)
	require.NoError(t, err)

	// Read partial content
	f, err := os.Open(testFile)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 10)
	n, err := f.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "This is a ", string(buf[:n]))

	// Seek and read again
	_, err = f.Seek(10, io.SeekStart)
	require.NoError(t, err)

	n, err = f.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "test file ", string(buf[:n]))
}

// TestRWFileSeekAndWrite tests seeking and writing at different offsets
func TestRWFileSeekAndWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "seek_write.txt")

	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Write at offset 0
	_, err = f.WriteAt([]byte("Start"), 0)
	require.NoError(t, err)

	// Write at offset 10 (sequential from 5)
	_, err = f.WriteAt([]byte("Middle"), 5)
	require.NoError(t, err)

	f.Close()

	// Verify content
	stat, err := os.Stat(testFile)
	require.NoError(t, err)
	assert.Greater(t, stat.Size(), int64(5), "File should have content at offset >= 5")
}

// TestRWFileWriteQueueBackpressure tests backpressure handling in write queue
func TestRWFileWriteQueueBackpressure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "backpressure_test.txt")

	// Write should respect backpressure limits
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Write multiple chunks without flush
	for i := 0; i < 100; i++ {
		chunk := make([]byte, 1024) // 1KB chunks
		for j := range chunk {
			chunk[j] = byte(i % 256)
		}
		_, err := f.Write(chunk)
		require.NoError(t, err)
	}

	// Final flush should complete
	err = f.Sync()
	require.NoError(t, err)

	stat, err := os.Stat(testFile)
	require.NoError(t, err)
	assert.Equal(t, int64(100*1024), stat.Size())
}

// TestRWFileErrorHandling tests error handling in write operations
func TestRWFileErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	t.Run("write to read-only file", func(t *testing.T) {
		testFile := filepath.Join(env.mountDir, "readonly.txt")

		// Create file
		err := os.WriteFile(testFile, []byte("content"), 0644)
		require.NoError(t, err)

		// Try to open for write
		f, err := os.OpenFile(testFile, os.O_RDONLY, 0644)
		if err == nil {
			defer f.Close()
			// Writing to read-only file handle should fail
			_, writeErr := f.Write([]byte("new"))
			assert.Error(t, writeErr)
		}
	})

	t.Run("multiple flush should not error", func(t *testing.T) {
		testFile := filepath.Join(env.mountDir, "multi_flush.txt")

		f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
		require.NoError(t, err)
		defer f.Close()

		_, err = f.Write([]byte("test"))
		require.NoError(t, err)

		// Multiple syncs should be safe
		for i := 0; i < 5; i++ {
			err = f.Sync()
			require.NoError(t, err)
		}
	})
}

// TestRWFileEmptyWrite tests writing empty content
func TestRWFileEmptyWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "empty_write.txt")

	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Empty write
	n, err := f.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Write actual content
	_, err = f.Write([]byte("content"))
	require.NoError(t, err)

	f.Close()

	// Verify
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, "content", string(content))
}

// TestRWFileDataIntegrity tests that written data maintains integrity
func TestRWFileDataIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "data_integrity.txt")

	// Create predictable content
	content := bytes.Repeat([]byte("Hello, World! "), 1000)

	// Write
	err := os.WriteFile(testFile, content, 0644)
	require.NoError(t, err)

	// Read back
	readBack, err := os.ReadFile(testFile)
	require.NoError(t, err)

	// Verify byte-for-byte equality
	assert.Equal(t, content, readBack)
	assert.Equal(t, len(content), len(readBack))
}

func TestRWFileLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)
	testFile := filepath.Join(env.mountDir, "locking.txt")

	f1, err := os.OpenFile(testFile, os.O_CREATE|os.O_RDWR, 0644)
	require.NoError(t, err)
	defer f1.Close()

	// Use OFD write lock on f1
	writeLock := syscall.Flock_t{
		Type:   syscall.F_WRLCK,
		Whence: io.SeekStart,
		Start:  0,
		Len:    0,
	}
	require.NoError(t, syscall.FcntlFlock(f1.Fd(), unix.F_OFD_SETLK, &writeLock))

	f2, err := os.OpenFile(testFile, os.O_RDWR, 0644)
	require.NoError(t, err)
	defer f2.Close()

	// Check the lock from f2 - should see f1's write lock
	conflict := syscall.Flock_t{
		Type:   syscall.F_WRLCK,
		Whence: io.SeekStart,
		Start:  0,
		Len:    0,
	}
	require.NoError(t, getOFDLock(f2.Fd(), &conflict))
	assert.Equal(t, int16(syscall.F_WRLCK), conflict.Type)

	// Try to set a read lock on f2 - should fail because f1 has write lock
	readLock := syscall.Flock_t{
		Type:   syscall.F_RDLCK,
		Whence: io.SeekStart,
		Start:  0,
		Len:    0,
	}
	err = syscall.FcntlFlock(f2.Fd(), unix.F_OFD_SETLK, &readLock)
	assert.Error(t, err, "Setting read lock should fail due to write lock")
	assert.True(t, errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK),
		"Lock conflict should return EAGAIN or EWOULDBLOCK, got: %v", err)

	// Unlock from f1
	unlock := syscall.Flock_t{
		Type:   syscall.F_UNLCK,
		Whence: io.SeekStart,
		Start:  0,
		Len:    0,
	}
	require.NoError(t, syscall.FcntlFlock(f1.Fd(), unix.F_OFD_SETLK, &unlock))

	// Now setting read lock on f2 should succeed
	require.NoError(t, syscall.FcntlFlock(f2.Fd(), unix.F_OFD_SETLK, &readLock))

	// Unlock from f2
	require.NoError(t, syscall.FcntlFlock(f2.Fd(), unix.F_OFD_SETLK, &unlock))
}

// TestRWFileDeleteAfterRead tests that reads complete even after file is deleted
// This verifies that the FD token persists after the backend file handle becomes invalid
func TestRWFileDeleteAfterRead(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "delete_after_read.txt")
	content := []byte("This is test content for deletion after read")

	// Create and write file
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = f.Write(content)
	require.NoError(t, err)
	f.Close()

	// Open file for reading
	f, err = os.OpenFile(testFile, os.O_RDONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	// Delete file while it's still open
	err = os.Remove(testFile)
	require.NoError(t, err, "Should be able to delete open file")

	// Read from the open file should still work - the fd token should persist
	buf := make([]byte, len(content))
	n, err := f.Read(buf)
	require.NoError(t, err, "Read should succeed even after file deletion")
	assert.Equal(t, len(content), n, "Should read all content")
	assert.Equal(t, content, buf, "Content should match")

	// Verify file is indeed deleted from filesystem
	_, err = os.Stat(testFile)
	assert.True(t, errors.Is(err, os.ErrNotExist), "File should not exist")
}

// TestRWFileAttrModifications tests chmod, chown, and truncate operations
func TestRWFileAttrModifications(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping RWFile test in short mode")
	}

	env := setupRWDirectFSEnv(t)

	testFile := filepath.Join(env.mountDir, "attr_mod.txt")
	content := []byte("Test content with some data")

	// Create file
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = f.Write(content)
	require.NoError(t, err)
	f.Close()

	// Get initial attributes
	stat1, err := os.Stat(testFile)
	require.NoError(t, err)
	initialMode := stat1.Mode()
	initialSize := stat1.Size()

	// Test chmod
	newMode := os.FileMode(0600)
	err = os.Chmod(testFile, newMode)
	require.NoError(t, err, "chmod should succeed")

	stat2, err := os.Stat(testFile)
	require.NoError(t, err)
	// Compare permission bits (ignoring file type bits)
	assert.Equal(t, newMode, stat2.Mode()&0777, "Permissions should change after chmod")
	assert.NotEqual(t, initialMode&0777, stat2.Mode()&0777, "Mode bits should differ from initial")

	// Test truncate
	truncateSize := int64(10)
	err = os.Truncate(testFile, truncateSize)
	require.NoError(t, err, "truncate should succeed")

	stat3, err := os.Stat(testFile)
	require.NoError(t, err)
	assert.Equal(t, truncateSize, stat3.Size(), "Size should match truncate target")
	assert.Less(t, stat3.Size(), initialSize, "Truncated size should be less than initial")

	// Verify file size reflects truncation
	f, err = os.OpenFile(testFile, os.O_RDONLY, 0644)
	require.NoError(t, err)
	defer f.Close()

	n, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, int(truncateSize), len(n), "Read size should match truncated size")

	// Test chown (if running as root)
	if os.Geteuid() == 0 {
		err = os.Chown(testFile, 0, 0)
		require.NoError(t, err, "chown should succeed as root")

		stat4, err := os.Stat(testFile)
		require.NoError(t, err)
		assert.Equal(t, uint32(0), uint32(stat4.Sys().(*syscall.Stat_t).Uid), "UID should be 0")
		assert.Equal(t, uint32(0), uint32(stat4.Sys().(*syscall.Stat_t).Gid), "GID should be 0")
	}
}
