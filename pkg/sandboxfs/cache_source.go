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
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CacheSource represents a source from which cached files can be fetched
type CacheSource interface {
	// Fetch retrieves a file from the cache source using the given cacheKey (format: size/hash)
	// Returns the file content reader and size, or an error if not found
	Fetch(ctx context.Context, size int64, hash string) (io.ReadCloser, int64, error)

	// Type returns the type of cache source (e.g., "http", "nfs")
	Type() string
}

// HTTPCacheSource implements CacheSource for HTTP-based cache (e.g., Cloudflare R2)
type HTTPCacheSource struct {
	baseURL    string
	httpClient *http.Client
	// minSize, if >0, indicates the minimum file size required to attempt fetch
	minSize int64
}

// NewHTTPCacheSource creates a new HTTP-based cache source
// baseURL should be the base URL of the cache (e.g., "https://cache.example.com")
// Files will be fetched from baseURL/<size>/<hash>
func NewHTTPCacheSource(baseURL string) *HTTPCacheSource {
	// Parse the URL to extract any query params (e.g., min_size) and normalize base path
	parsed, err := url.Parse(baseURL)
	var minSize int64
	base := strings.TrimSuffix(baseURL, "/")

	if err == nil {
		// Extract min_size if present
		if ms := parsed.Query().Get("min_size"); ms != "" {
			if v, err := strconv.ParseInt(ms, 10, 64); err == nil {
				minSize = v
			}
		}

		// Rebuild base without query string: keep scheme, host and path
		base = strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host+parsed.Path, "/")
	}

	return &HTTPCacheSource{
		baseURL: base,
		minSize: minSize,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Fetch retrieves a file from the HTTP cache source
func (h *HTTPCacheSource) Fetch(ctx context.Context, size int64, hash string) (io.ReadCloser, int64, error) {
	// Honor configured minSize: if file is smaller, treat as not present
	if h.minSize > 0 && size < h.minSize {
		return nil, 0, fmt.Errorf("file not found in HTTP cache")
	}

	// Build URL as: <baseURL>/<hash>?size=<size>
	sizeStr := strconv.FormatInt(size, 10)
	reqURL := fmt.Sprintf("%s/%s", h.baseURL, hash)
	q := (url.Values{"size": {sizeStr}}).Encode()
	if q != "" {
		reqURL = reqURL + "?" + q
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch from HTTP cache: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("file not found in HTTP cache")
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("HTTP cache returned status %d", resp.StatusCode)
	}

	respSize := resp.ContentLength
	if respSize < 0 {
		respSize = 0
	}

	return resp.Body, respSize, nil
}

// Type returns the type of this cache source
func (h *HTTPCacheSource) Type() string {
	return "http"
}

// NFSCacheSource implements CacheSource for NFS-based cache
// This is a placeholder for future NFS implementation
type NFSCacheSource struct {
	mountPath string
}

// NewNFSCacheSource creates a new NFS-based cache source
// mountPath should be the path where NFS is mounted
func NewNFSCacheSource(mountPath string) *NFSCacheSource {
	return &NFSCacheSource{
		mountPath: mountPath,
	}
}

// Fetch retrieves a file from the NFS cache source
func (n *NFSCacheSource) Fetch(ctx context.Context, size int64, hash string) (io.ReadCloser, int64, error) {
	// TODO: Implement NFS cache fetch with explicit size/hash
	return nil, 0, fmt.Errorf("NFS cache source not yet implemented")
}

// Type returns the type of this cache source
func (n *NFSCacheSource) Type() string {
	return "nfs"
}

// parseCacheSource parses a cache source string and creates the appropriate CacheSource
// Supported formats:
//   - http://... or https://... - HTTP cache source (files accessed as baseURL/size/hash)
//   - nfs://path - NFS cache source (future)
func parseCacheSource(sourceStr string) (CacheSource, error) {
	if strings.HasPrefix(sourceStr, "http://") || strings.HasPrefix(sourceStr, "https://") {
		return NewHTTPCacheSource(sourceStr), nil
	}

	if strings.HasPrefix(sourceStr, "nfs://") {
		path := strings.TrimPrefix(sourceStr, "nfs://")
		return NewNFSCacheSource(path), nil
	}

	return nil, fmt.Errorf("unsupported cache source format: %s", sourceStr)
}

// initializeCacheSources parses cache source strings and returns initialized CacheSource instances
func initializeCacheSources(sourceStrs []string) []CacheSource {
	var sources []CacheSource

	for _, sourceStr := range sourceStrs {
		source, err := parseCacheSource(sourceStr)
		if err != nil {
			log.Printf("Warning: failed to parse cache source %q: %v", sourceStr, err)
			continue
		}
		sources = append(sources, source)
	}
	// Success message logged via DebugLog when sources are used

	return sources
}
