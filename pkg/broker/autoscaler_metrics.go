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
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	autoscalerWorkersDesc = prometheus.NewDesc(
		"velda_autoscaler_workers",
		"Current number of workers grouped by autoscaler status.",
		[]string{"pool", "status"},
		nil,
	)
	autoscalerLastEventTimestampDesc = prometheus.NewDesc(
		"velda_autoscaler_last_event_timestamp_seconds",
		"Unix timestamp of the last autoscaler event by event type.",
		[]string{"pool", "event"},
		nil,
	)
	autoscalerWorkerSlotsDesc = prometheus.NewDesc(
		"velda_autoscaler_worker_slots",
		"Autoscaler worker slot counters.",
		[]string{"pool", "kind"},
		nil,
	)
)

func describeAutoScalerMetrics(ch chan<- *prometheus.Desc) {
	ch <- autoscalerWorkersDesc
	ch <- autoscalerLastEventTimestampDesc
	ch <- autoscalerWorkerSlotsDesc
}

// Describe implements prometheus.Collector.
func (p *AutoScaledPool) Describe(ch chan<- *prometheus.Desc) {
	describeAutoScalerMetrics(ch)
}

// Collect implements prometheus.Collector.
// All autoscaler metrics are snapshotted under one lock for consistency.
func (p *AutoScaledPool) Collect(ch chan<- prometheus.Metric) {
	p.mu.Lock()
	poolName := p.name
	workersByStatus := make(map[WorkerStatusCode]int, len(p.workersByStatus))
	for status, workers := range p.workersByStatus {
		workersByStatus[status] = len(workers)
	}
	availableSlots := p.idleSlots
	pendingSessions := p.pendingSessions
	lastScaleUpAttemptAt := p.lastScaleUpAttemptAt
	lastAllocationErrorAt := p.lastAllocationErrorAt
	lastResourceExhaustedAt := p.lastResourceExhaustedAt
	lastStartupFailureAt := p.lastStartupFailureAt
	p.mu.Unlock()

	for status := WorkerStatusCode(0); status < WorkerStatusMax; status++ {
		if status == WorkerStatusNone {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			autoscalerWorkersDesc,
			prometheus.GaugeValue,
			float64(workersByStatus[status]),
			poolName,
			status.String(),
		)
	}

	ch <- prometheus.MustNewConstMetric(
		autoscalerWorkerSlotsDesc,
		prometheus.GaugeValue,
		float64(availableSlots),
		poolName,
		"available",
	)
	ch <- prometheus.MustNewConstMetric(
		autoscalerWorkerSlotsDesc,
		prometheus.GaugeValue,
		float64(pendingSessions),
		poolName,
		"requested",
	)

	emitLastEventTimestamp := func(event string, ts time.Time) {
		if ts.IsZero() {
			return
		}
		ch <- prometheus.MustNewConstMetric(
			autoscalerLastEventTimestampDesc,
			prometheus.GaugeValue,
			float64(ts.Unix()),
			poolName,
			event,
		)
	}

	emitLastEventTimestamp("scale_up_attempt", lastScaleUpAttemptAt)
	emitLastEventTimestamp("allocation_error", lastAllocationErrorAt)
	emitLastEventTimestamp("resource_exhausted", lastResourceExhaustedAt)
	emitLastEventTimestamp("startup_failure", lastStartupFailureAt)
}
