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
package broker

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func findMetricValue(t *testing.T, mfs []*dto.MetricFamily, name string, labels map[string]string) float64 {
	t.Helper()
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			labelMap := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				labelMap[label.GetName()] = label.GetValue()
			}
			match := true
			for k, v := range labels {
				if labelMap[k] != v {
					match = false
					break
				}
			}
			if match {
				require.NotNil(t, metric.Gauge)
				return metric.Gauge.GetValue()
			}
		}
	}
	require.Failf(t, "metric not found", "name=%s labels=%v", name, labels)
	return 0
}

func TestAutoScaledPoolCollectorExportsMetrics(t *testing.T) {
	pool := NewAutoScaledPool("pool-metrics", AutoScaledPoolConfig{Context: context.Background()})

	pool.mu.Lock()
	pool.setWorkerStatusLocked("worker-idle", WorkerStatusIdle, 2)
	pool.setWorkerStatusLocked("worker-running", WorkerStatusRunning, 0)
	pool.setWorkerStatusLocked("worker-pending", WorkerStatusPending, 1)
	pool.pendingSessions = 2
	pool.lastScaleUpAttemptAt = time.Unix(100, 0)
	pool.lastAllocationErrorAt = time.Unix(200, 0)
	pool.lastResourceExhaustedAt = time.Unix(300, 0)
	pool.lastStartupFailureAt = time.Unix(400, 0)
	pool.mu.Unlock()

	reg := prometheus.NewRegistry()
	reg.MustRegister(pool)
	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.Equal(t, float64(1), findMetricValue(t, mfs, "velda_autoscaler_workers", map[string]string{"pool": "pool-metrics", "status": "Idle"}))
	require.Equal(t, float64(1), findMetricValue(t, mfs, "velda_autoscaler_workers", map[string]string{"pool": "pool-metrics", "status": "Running"}))
	require.Equal(t, float64(1), findMetricValue(t, mfs, "velda_autoscaler_workers", map[string]string{"pool": "pool-metrics", "status": "Pending"}))

	require.Equal(t, float64(3), findMetricValue(t, mfs, "velda_autoscaler_worker_slots", map[string]string{"pool": "pool-metrics", "kind": "available"}))
	require.Equal(t, float64(2), findMetricValue(t, mfs, "velda_autoscaler_worker_slots", map[string]string{"pool": "pool-metrics", "kind": "requested"}))

	require.Equal(t, float64(100), findMetricValue(t, mfs, "velda_autoscaler_last_event_timestamp_seconds", map[string]string{"pool": "pool-metrics", "event": "scale_up_attempt"}))
	require.Equal(t, float64(200), findMetricValue(t, mfs, "velda_autoscaler_last_event_timestamp_seconds", map[string]string{"pool": "pool-metrics", "event": "allocation_error"}))
	require.Equal(t, float64(300), findMetricValue(t, mfs, "velda_autoscaler_last_event_timestamp_seconds", map[string]string{"pool": "pool-metrics", "event": "resource_exhausted"}))
	require.Equal(t, float64(400), findMetricValue(t, mfs, "velda_autoscaler_last_event_timestamp_seconds", map[string]string{"pool": "pool-metrics", "event": "startup_failure"}))
}

func TestSchedulerSetCollectorDelegatesToPools(t *testing.T) {
	schedulers := NewSchedulerSet(context.Background())
	poolScheduler, err := schedulers.GetOrCreatePool("pool-a")
	require.NoError(t, err)

	poolScheduler.PoolManager.mu.Lock()
	poolScheduler.PoolManager.setWorkerStatusLocked("worker-1", WorkerStatusIdle, 1)
	poolScheduler.PoolManager.mu.Unlock()

	reg := prometheus.NewRegistry()
	reg.MustRegister(schedulers)
	mfs, err := reg.Gather()
	require.NoError(t, err)

	require.Equal(t, float64(1), findMetricValue(t, mfs, "velda_autoscaler_workers", map[string]string{"pool": "pool-a", "status": "Idle"}))
	require.Equal(t, float64(1), findMetricValue(t, mfs, "velda_autoscaler_worker_slots", map[string]string{"pool": "pool-a", "kind": "available"}))
}
