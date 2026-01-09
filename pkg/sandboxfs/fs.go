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

type MountOptions func(*fs.Options)

// VeldaServer wraps a FUSE server and provides access to cache manager for testing
type VeldaServer struct {
	*fuse.Server
	Cache *DirectoryCacheManager
	Root  *CachedLoopbackNode
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

	// Create cached loopback root
	rootNode, err := NewCachedLoopbackRoot(baseDir, cache)
	if err != nil {
		return nil, err
	}

	// Type assert to get the actual root node for worker management
	cachedRoot, ok := rootNode.(*CachedLoopbackNode)
	if !ok {
		return nil, fmt.Errorf("failed to assert root node type")
	}

	timeout := 60 * time.Second
	negativeTimeout := 10 * time.Second
	option := &fs.Options{
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
		},
	}
	for _, opt := range options {
		opt(option)
	}

	server, err := fs.Mount(workspaceDir, rootNode, option)
	if err != nil {
		return nil, err
	}

	server.RecordLatencies(GlobalCacheMetrics)

	veldaServer := &VeldaServer{
		Server: server,
		Cache:  cache,
		Root:   cachedRoot,
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
