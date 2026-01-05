// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package sandboxfs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// CacheMetrics tracks cache hit/miss/save statistics
type CacheMetrics struct {
	// Read operations
	fuseLatency   *prometheus.SummaryVec
	CacheHit      prometheus.Counter
	CacheMiss     prometheus.Counter
	CacheNotExist prometheus.Counter

	// Write operations (after file close)
	CacheSaved   prometheus.Counter
	CacheDup     prometheus.Counter
	CacheAborted prometheus.Counter
}

var GlobalCacheMetrics *CacheMetrics

func NewCacheMetrics() *CacheMetrics {
	return &CacheMetrics{
		fuseLatency: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Name: "velda_cache_fuse_latency",
			Help: "Latency of sandboxfs cache operations in seconds",
		}, []string{"name"}),
		CacheHit: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "velda_cache_hit_total",
			Help: "Number of cache hits during file open",
		}),
		CacheMiss: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "velda_cache_miss_total",
			Help: "Number of cache misses (xattr exists but cache file missing)",
		}),
		CacheNotExist: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "velda_cache_not_exist_total",
			Help: "Number of files without cache xattr",
		}),
		CacheSaved: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "velda_cache_saved_total",
			Help: "Number of files successfully cached",
		}),
		CacheDup: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "velda_cache_dup_total",
			Help: "Number of duplicate cache entries (file already cached)",
		}),
		CacheAborted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "velda_cache_aborted_total",
			Help: "Number of cache operations aborted (non-sequential writes, errors, etc.)",
		}),
	}
}

func (m *CacheMetrics) Register() {
	prometheus.MustRegister(m.fuseLatency)
	prometheus.MustRegister(m.CacheHit)
	prometheus.MustRegister(m.CacheMiss)
	prometheus.MustRegister(m.CacheNotExist)
	prometheus.MustRegister(m.CacheSaved)
	prometheus.MustRegister(m.CacheDup)
	prometheus.MustRegister(m.CacheAborted)
}

func (m *CacheMetrics) Unregister() {
	prometheus.Unregister(m.fuseLatency)
	prometheus.Unregister(m.CacheHit)
	prometheus.Unregister(m.CacheMiss)
	prometheus.Unregister(m.CacheNotExist)
	prometheus.Unregister(m.CacheSaved)
	prometheus.Unregister(m.CacheDup)
	prometheus.Unregister(m.CacheAborted)
}

func (m *CacheMetrics) Add(name string, dt time.Duration) {
	m.fuseLatency.WithLabelValues(name).Observe(dt.Seconds())
	m.fuseLatency.WithLabelValues("all").Observe(dt.Seconds())
}
