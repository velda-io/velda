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

import "github.com/prometheus/client_golang/prometheus"

// Describe implements prometheus.Collector.
func (s *SchedulerSet) Describe(ch chan<- *prometheus.Desc) {
	describeAutoScalerMetrics(ch)
}

// Collect implements prometheus.Collector and delegates collection to all pools.
func (s *SchedulerSet) Collect(ch chan<- prometheus.Metric) {
	s.mu.Lock()
	pools := make([]*AutoScaledPool, 0, len(s.agents))
	for _, scheduler := range s.agents {
		if scheduler.PoolManager != nil {
			pools = append(pools, scheduler.PoolManager)
		}
	}
	s.mu.Unlock()

	for _, pool := range pools {
		pool.Collect(ch)
	}
}
