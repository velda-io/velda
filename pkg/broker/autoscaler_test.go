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
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type FakeBackend struct {
	// Ordered by creation time.
	workers []WorkerStatus
	pending []string
	lastId  int
	clock   func() time.Time
	// If true, deleted worker names can be reused
	recycleWorkerNames bool
	deletedWorkers     []string
}

func (f *FakeBackend) RequestScaleUp(ctx context.Context) (string, error) {
	var name string
	// Recycle deleted worker names if enabled
	if f.recycleWorkerNames && len(f.deletedWorkers) > 0 {
		name = f.deletedWorkers[0]
		f.deletedWorkers = f.deletedWorkers[1:]
		log.Printf("Fake backend: Scale up (recycled) %s", name)
	} else {
		id := f.lastId
		f.lastId++
		name = fmt.Sprintf("worker-%d", id)
		log.Printf("Fake backend: Scale up %s", name)
	}
	f.workers = append(f.workers, WorkerStatus{
		Name:      name,
		CreatedAt: f.clock(),
	})
	f.pending = append(f.pending, name)

	return name, nil
}

func (f *FakeBackend) GetPending(t *testing.T) string {
	require.NotEmpty(t, f.pending)
	name := f.pending[0]
	f.pending = f.pending[1:]
	return name
}

func (f *FakeBackend) NoPending() bool {
	return len(f.pending) == 0
}

func (f *FakeBackend) RequestDelete(ctx context.Context, workerName string) error {
	for i, v := range f.workers {
		if v.Name == workerName {
			log.Printf("Fake backend: Scale down %s", workerName)
			f.workers = append(f.workers[:i], f.workers[i+1:]...)
			if f.recycleWorkerNames {
				f.deletedWorkers = append(f.deletedWorkers, workerName)
			}
			return nil
		}
	}
	log.Printf("Scale down %s failed", workerName)
	return fmt.Errorf("worker %s not found", workerName)
}

func (f *FakeBackend) ListWorkers(ctx context.Context) ([]WorkerStatus, error) {
	var res []WorkerStatus
	for _, v := range f.workers {
		res = append(res, v)
	}
	return res, nil
}

func (f *FakeBackend) HasWorker(workerName string) bool {
	for _, v := range f.workers {
		if v.Name == workerName {
			return true
		}
	}
	return false
}

func TestAutoScaler(t *testing.T) {
	backend := &FakeBackend{
		workers: []WorkerStatus{},
		clock:   time.Now,
	}

	pool := NewAutoScaledPool("pool", AutoScaledPoolConfig{
		Context: context.Background(),
		Backend: backend,
		MinSize: 2,
		MinIdle: 1,
		MaxIdle: 3,
		// Not part of this test.
		IdleDecay:            100 * time.Hour,
		MaxSize:              5,
		DefaultSlotsPerAgent: 1,
	})

	allocateWorker := func(t *testing.T) string {
		pool.mu.Lock()
		assert.NotEmpty(t, pool.idleWorkers)
		var worker string
		for k := range pool.idleWorkers {
			worker = k
			break
		}
		pool.mu.Unlock()
		assert.NotEmpty(t, worker)
		assert.NoError(t, pool.MarkBusy(worker, 0))
		return worker
	}

	// Helper to require a worker from the pool, and assign it to be busy.
	requireWorker := func(t *testing.T) {
		err := pool.RequestWorker()
		assert.NoError(t, err)
		allocateWorker(t)
	}

	inspectChan := make(chan AgentStatusRequest, 100)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case req := <-inspectChan:
				if req.shutdown {
					pool.MarkDeleting(req.target)
				}
			case <-stop:
				return
			}
		}
	}()

	t.Run("InitialWorkers", func(t *testing.T) {
		pool.ReadyForIdleMaintenance()
		// Should trigger a scale-up for min size.
		assert.Equal(t, 2, len(backend.workers))
		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 1)
		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 1)
	})

	t.Run("RequestWorkerAndMaintainSize", func(t *testing.T) {
		requireWorker(t)
		// Should also trigger a scale-up for min-idle.
		assert.Equal(t, 2, len(backend.workers))
	})

	t.Run("RequestWorkerAndScaleUpForIdle", func(t *testing.T) {
		for i := 1; i < 4; i++ {
			requireWorker(t)
			log.Printf("Requesting worker %d: %d idle, %d pending", i, pool.idleSlots, pool.pendingSessions)
			// Should also trigger a scale-up for min-idle.
			assert.Equal(t, i+2, len(backend.workers))
			pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 1)
		}
	})

	t.Run("RequestWorkerMeetMaxSize", func(t *testing.T) {
		requireWorker(t)
		assert.Equal(t, 5, len(backend.workers))
		assert.Equal(t, 5, len(pool.runningWorkers))
	})

	t.Run("RequestWorkerNotAvailable", func(t *testing.T) {
		err := pool.RequestWorker()
		assert.NoError(t, err)
		assert.Equal(t, 5, len(backend.workers))
	})

	// Current the pool is full, with one pending session.
	// Now if one task finishes and the task is assigned, it should
	// cancel a request.
	assert.NoError(t, pool.RequestCancelled())

	t.Run("TaskCompleted", func(t *testing.T) {
		pool.MarkIdle(backend.workers[0].Name, 1)
		pool.MarkIdle(backend.workers[1].Name, 1)
		pool.MarkIdle(backend.workers[2].Name, 1)

		pool.MarkIdle(backend.workers[3].Name, 1)
		// Should trigger a scale-down for max-idle.
		assert.Eventually(t, func() bool {
			return len(backend.workers) == 4
		}, 3*time.Second, 10*time.Millisecond)
	})

	// Currently have 4 workers, 3 are idle.
	// If we enable idle decay, it should scale down to 1(active) + 1(idle).
	t.Run("RemoveIdleToMinimal", func(t *testing.T) {
		oldDecay := pool.idleDecay
		pool.idleDecay = 0
		defer func() {
			pool.idleDecay = oldDecay
		}()
		// We force a scale-down to retrigger the idle decay.
		pool.MarkIdle(backend.workers[3].Name, 1)

		if !assert.Eventually(t, func() bool {
			return len(backend.workers) == 2
		}, 3*time.Second, 10*time.Millisecond) {
			assert.Failf(t, "", "Expected 2 worker, got %d", len(backend.workers))
		}
	})

	assert.Equal(t, 2, len(backend.workers))
	assert.Equal(t, 2, len(pool.idleWorkers))

	t.Run("CancelRequestDuringStarting", func(t *testing.T) {
		// Use the first idle worker.
		requireWorker(t)

		// req 2, will create a new worker.
		assert.NoError(t, pool.RequestWorker())
		newInst2 := backend.GetPending(t)

		// Now simulate 2nd request get the worker recycled from 1st one.
		// The new node would be assumed to be idle after being ready.
		assert.NoError(t, pool.RequestCancelled())
		assert.True(t, backend.NoPending())

		// Req 3
		// This should not trigger a new worker:
		// 1 active, 1 pending and projected to be idle, so minIdle is still met.
		assert.NoError(t, pool.RequestWorker())
		assert.True(t, backend.NoPending())

		// Req 4
		// This will continue to trigger a scale up request, as it will use the
		// only pending worker.
		assert.NoError(t, pool.RequestWorker())
		newInst3 := backend.GetPending(t)

		allocateWorker(t)                                          // Assign to Req 3
		pool.NotifyAgentAvailable(newInst2, false, inspectChan, 1) // Assign to Req 4
		pool.MarkBusy(newInst2, 0)
		pool.NotifyAgentAvailable(newInst3, false, inspectChan, 1)
	})

	assert.Equal(t, 3, len(pool.runningWorkers))
	assert.Equal(t, 1, len(pool.idleWorkers))

	t.Run("MarkLost", func(t *testing.T) {
		// Simulate a worker being killed in the cluster.
		// Should not do anything, because busy workers are lost.
		// It's up to the workload to re-request a new session if retry is needed.
		pool.MarkLost(backend.workers[0].Name)
		backend.RequestDelete(context.Background(), backend.workers[0].Name)
		pool.MarkLost(backend.workers[0].Name)
		backend.RequestDelete(context.Background(), backend.workers[0].Name)
		pool.MarkLost(backend.workers[0].Name)
		backend.RequestDelete(context.Background(), backend.workers[0].Name)

		// Scale up to meet min-size.
		newWorker := backend.GetPending(t)
		pool.NotifyAgentAvailable(newWorker, false, inspectChan, 1)

		// Scale up again to meet min-size.
		pool.MarkLost(backend.workers[0].Name)
		backend.RequestDelete(context.Background(), backend.workers[0].Name)
		newWorker2 := backend.GetPending(t)
		pool.NotifyAgentAvailable(newWorker2, false, inspectChan, 1)

		// Should trigger a scale-up for min-idle & min-size.
		assert.Equal(t, 0, len(pool.runningWorkers))
		assert.Equal(t, 2, len(backend.workers))
	})

	t.Run("Reconnect", func(t *testing.T) {
		backend.RequestScaleUp(context.Background())
		backend.RequestScaleUp(context.Background())
		backend.RequestScaleUp(context.Background())

		inst1 := backend.GetPending(t)
		inst2 := backend.GetPending(t)
		inst3 := backend.GetPending(t)

		pool.Reconnect(context.Background())

		pool.NotifyAgentAvailable(inst1, false, inspectChan, 1)
		pool.NotifyAgentAvailable(inst2, false, inspectChan, 1)
		pool.NotifyAgentAvailable(inst3, false, inspectChan, 1)

		// Should scale down a worker to meet max-idle.

		assert.Eventually(t, func() bool {
			log.Printf("len workers: %d", len(backend.workers))
			return len(backend.workers) == 3
		}, 3*time.Second, 10*time.Millisecond)
	})
}

func TestAutoScalerWithMultipleSlots(t *testing.T) {
	backend := &FakeBackend{
		workers: []WorkerStatus{},
		clock:   time.Now,
	}

	pool := NewAutoScaledPool("multi-slot-pool", AutoScaledPoolConfig{
		Context:              context.Background(),
		Backend:              backend,
		MinIdle:              2,               // Require 2 idle slots
		MaxIdle:              4,               // Allow up to 4 idle slots
		IdleDecay:            100 * time.Hour, // Not tested here
		MaxSize:              5,               // Max 3 workers
		DefaultSlotsPerAgent: 2,               // Each worker has 2 slots
	})

	inspectChan := make(chan AgentStatusRequest, 100)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case req := <-inspectChan:
				if req.shutdown {
					pool.MarkDeleting(req.target)
				}
			case <-stop:
				return
			}
		}
	}()
	// Request a worker - this will use one slot
	requireWorker := func(t *testing.T) {
		err := pool.RequestWorker()
		assert.NoError(t, err)
	}

	t.Run("InitialWorkers", func(t *testing.T) {
		pool.ReadyForIdleMaintenance()
		// Should trigger a scale-up for min-idle (2 slots),
		// but only need 1 worker since each has 2 slots
		assert.Equal(t, 1, len(backend.workers))
		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 2)

		// Verify we have 2 idle slots as expected
		assert.Equal(t, 2, pool.idleSlots)
	})

	t.Run("RequestWorkerWithMultipleSlots", func(t *testing.T) {

		// Request first worker - will consume one slot from existing worker
		requireWorker(t)
		pool.MarkBusy(backend.workers[0].Name, 1)
		assert.Equal(t, 2, len(backend.workers))

		// Request second worker - will consume the second slot from existing worker
		requireWorker(t)
		pool.MarkBusy(backend.workers[0].Name, 0)

		// Now we're out of slots, and need to scale up to maintain min-idle = 2
		assert.Equal(t, 2, len(backend.workers))
		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 2)
	})

	t.Run("ScaleDownExcessIdleSlots", func(t *testing.T) {
		requireWorker(t)
		pool.MarkBusy(backend.workers[1].Name, 1)

		// Now we have 2 workers(4 total slots), 3 slot busy. Need to scale up to maintain min-idle = 2
		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 2)

		// Mark all workers to be idle
		pool.MarkIdle(backend.workers[0].Name, 2)
		pool.MarkIdle(backend.workers[1].Name, 2)

		// We now have 6 idle slots from 3 workers
		// This is over max-idle (4), so we should scale down
		assert.Eventually(t, func() bool {
			return len(backend.workers) == 2
		}, 3*time.Second, 10*time.Millisecond)

		// We should have 4 idle slots now (2 workers × 2 slots each)
		assert.Equal(t, 4, pool.idleSlots)
	})

	// Test that verify only idle worker will be scaled down even if it exceeds max-idle.
	t.Run("PartialOccupancyTracking", func(t *testing.T) {
		// Request one worker, which will use one slot
		requireWorker(t)
		pool.MarkBusy(backend.workers[0].Name, 1)
		requireWorker(t)
		pool.MarkBusy(backend.workers[0].Name, 0)
		requireWorker(t)
		pool.MarkBusy(backend.workers[1].Name, 1)

		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 2)
		requireWorker(t)
		pool.MarkBusy(backend.workers[1].Name, 0)

		requireWorker(t)
		pool.MarkBusy(backend.workers[2].Name, 1)

		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 2)
		requireWorker(t)
		pool.MarkBusy(backend.workers[2].Name, 0)
		requireWorker(t)
		pool.MarkBusy(backend.workers[3].Name, 1)
		pool.NotifyAgentAvailable(backend.GetPending(t), false, inspectChan, 2)
		requireWorker(t)
		pool.MarkBusy(backend.workers[4].Name, 1)

		// Now we have 5 workers, 8 slots busy. 0-2 are fully used, 3-4 are partially used.
		assert.Equal(t, 5, len(backend.workers))
		assert.Equal(t, 0, pool.pendingSessions)
		assert.Equal(t, 2, pool.idleSlots)

		// Mark 0-2 to be partially used.
		pool.MarkBusy(backend.workers[0].Name, 1)
		pool.MarkBusy(backend.workers[1].Name, 1)
		pool.MarkBusy(backend.workers[2].Name, 1)

		// No workers should be scaled down, despite we have 5 idle slots.
		assert.Equal(t, 5, pool.idleSlots)
		assert.Equal(t, 5, len(backend.workers))
		toDelete := backend.workers[0].Name
		pool.MarkIdle(backend.workers[0].Name, 2)
		// Will scale down worker 0 only.
		assert.Eventually(t, func() bool {
			return len(backend.workers) == 4
		}, 3*time.Second, 10*time.Millisecond)
		assert.False(t, backend.HasWorker(toDelete), "Worker %s should be deleted", toDelete)
	})
}

func TestDeleteUnknownWorkers(t *testing.T) {
	backend := &FakeBackend{
		workers: []WorkerStatus{},
		clock:   time.Now,
	}

	pool := NewAutoScaledPool("pool", AutoScaledPoolConfig{
		Context: context.Background(),
		Backend: backend,
		MinIdle: 0,
		MaxIdle: 3,
		// Not part of this test.
		IdleDecay:        100 * time.Hour,
		KillUnknownAfter: 100 * time.Microsecond,
		MaxSize:          5,
	})

	backend.RequestScaleUp(context.Background())
	backend.RequestScaleUp(context.Background())

	assert.NoError(t, pool.Reconnect(context.Background()))
	// unknown worker that are never connected to the agent.
	_ = backend.GetPending(t)
	lost := backend.GetPending(t)
	pool.NotifyAgentAvailable(lost, false, nil, 1)
	pool.MarkLost(lost)
	pool.ReadyForIdleMaintenance()

	time.Sleep(150 * time.Millisecond)

	assert.NoError(t, pool.Reconnect(context.Background()))
	// Should delete the lost worker, as it was not seen for 300ms.
	assert.Equal(t, 0, len(backend.workers), time.Since(pool.lastKnownTime[lost]))
}

func TestCancelledDuringPending(t *testing.T) {
	backend := &FakeBackend{
		workers: []WorkerStatus{},
		clock:   time.Now,
	}

	pool := NewAutoScaledPool("pool", AutoScaledPoolConfig{
		Context:              context.Background(),
		Backend:              backend,
		MinIdle:              0,
		MaxIdle:              0,
		IdleDecay:            0 * time.Hour,
		MaxSize:              2,
		DefaultSlotsPerAgent: 1,
	})

	pool.ReadyForIdleMaintenance()
	pool.RequestWorker()
	pool.RequestCancelled()
	// Verify worker is deleted from backend
	assert.Eventually(t, func() bool {
		return len(backend.workers) == 0
	}, 3*time.Second, 10*time.Millisecond)
}
