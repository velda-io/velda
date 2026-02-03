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
package sandboxfs

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// VeldaMountOptions wraps FUSE mount options and Velda-specific settings
type VeldaMountOptions struct {
	FuseOptions  *fs.Options
	SnapshotMode bool
	NoCacheMode  bool
	DirectFSMode bool
}

type MountOptions func(*VeldaMountOptions)

// WithSnapshotMode enables snapshot mode for maximum IO performance.
// In snapshot mode, all caching is maximized:
// - Infinite entry and attribute timeouts
// - All file content, stats, and symlinks are aggressively cached
// - Best suited for read-only snapshot workloads where data doesn't change
func WithSnapshotMode() MountOptions {
	return func(opts *VeldaMountOptions) {
		opts.SnapshotMode = true
		opts.FuseOptions.EnableSymlinkCaching = true
		opts.FuseOptions.ExplicitDataCacheControl = true
	}
}

// WithNoCacheMode enables no-cache mode.
// In no-cache mode:
// - Cache is never read from or written to
// - Only cache keys (xattr) are set during write operations
// - All reads go directly to the underlying file
// - Useful for write-heavy workloads where cache overhead should be avoided
func WithNoCacheMode() MountOptions {
	return func(opts *VeldaMountOptions) {
		opts.NoCacheMode = true
	}
}

// WithDirectFSMode enables DirectFS mode for remote snapshot serving.
// In DirectFS mode:
// - Connects to a remote fileserver using custom protocol
// - Uses Linux file handles for efficient access
// - All data cached using content-addressable storage
// - Kernel-level caching with persistent inodes
// - Optimized for serving filesystem snapshots over network
func WithDirectFSMode() MountOptions {
	return func(opts *VeldaMountOptions) {
		opts.DirectFSMode = true
		opts.FuseOptions.EnableSymlinkCaching = true
		opts.FuseOptions.ExplicitDataCacheControl = true
	}
}

// WithFuseOption allows setting FUSE-specific options
func WithFuseOption(fn func(*fs.Options)) MountOptions {
	return func(opts *VeldaMountOptions) {
		fn(opts.FuseOptions)
	}
}

// VeldaServer wraps a FUSE server and provides access to cache manager for testing
type VeldaServer struct {
	*fuse.Server
	Cache *DirectoryCacheManager
}

// MountWorkDir mounts a workspace directory with caching support
// cacheDir is the directory where cached files will be stored
func MountWorkDir(baseDir, workspaceDir, cacheDir string, options ...MountOptions) (*VeldaServer, error) {
	// Create cache manager
	cache, err := NewDirectoryCacheManager(cacheDir)
	if err != nil {
		return nil, err
	}

	// Initialize cache metrics
	if GlobalCacheMetrics == nil {
		cacheMetrics := NewCacheMetrics()
		cacheMetrics.Register()
		GlobalCacheMetrics = cacheMetrics
	}

	timeout := 60 * time.Second
	negativeTimeout := 10 * time.Second

	// Create VeldaMountOptions with default FUSE options
	veldaOpts := &VeldaMountOptions{
		FuseOptions: &fs.Options{
			EntryTimeout:    &timeout,
			AttrTimeout:     &timeout,
			NegativeTimeout: &negativeTimeout,
			MountOptions: fuse.MountOptions{
				AllowOther:         true,
				DisableReadDirPlus: true,
				DirectMount:        true,
				Name:               "veldafs",
				MaxWrite:           1024 * 1024,
				EnableLocks:        true,
				DirectMountFlags:   syscall.MS_MGC_VAL,
				Options:            []string{"default_permissions"},
			},
		},
		SnapshotMode: false,
		NoCacheMode:  false,
	}

	// Apply user-provided options
	for _, opt := range options {
		opt(veldaOpts)
	}

	// In snapshot mode, use infinite timeouts for maximum caching
	if veldaOpts.SnapshotMode {
		infiniteTimeout := 365 * 24 * time.Hour // 1 year
		veldaOpts.FuseOptions.EntryTimeout = &infiniteTimeout
		veldaOpts.FuseOptions.AttrTimeout = &infiniteTimeout
		veldaOpts.FuseOptions.NegativeTimeout = &infiniteTimeout
	}

	var rootNode fs.InodeEmbedder
	// Handle DirectFS mode
	if veldaOpts.DirectFSMode {
		client := NewDirectFSClient(baseDir, cache)
		rootNode, err = client.Connect()
		if err != nil {
			return nil, fmt.Errorf("failed to connect to DirectFS server: %w", err)
		}
	} else {
		// Create cached loopback root
		rootNode, err = NewCachedLoopbackRoot(baseDir, cache, veldaOpts.SnapshotMode, veldaOpts.NoCacheMode)
		if err != nil {
			return nil, err
		}
	}

	server, err := fs.Mount(workspaceDir, rootNode, veldaOpts.FuseOptions)
	if err != nil {
		return nil, err
	}

	server.RecordLatencies(GlobalCacheMetrics)

	veldaServer := &VeldaServer{
		Server: server,
		Cache:  cache,
	}

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		<-sig
		log.Printf("Unmounting %s", workspaceDir)
		for {
			err := server.Unmount()
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	return veldaServer, nil
}
