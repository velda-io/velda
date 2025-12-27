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
package backends

import (
	"context"
	"log"
	"sync"

	"velda.io/velda/pkg/broker"
)

// WorkerState represents the state of a worker instance
type WorkerState string

const (
	WorkerStateActive    WorkerState = "active"
	WorkerStateSuspended WorkerState = "suspended"
)

// WorkerInfo contains information about a worker instance
type WorkerInfo struct {
	// State is the current state of the worker (active or suspended)
	State WorkerState
	// Data is backend-specific data about the worker (e.g., instance ID, bid ID, etc.)
	Data interface{}
}

// Resumable is an optional interface that backends can implement to support suspend/resume
type Resumable interface {
	// SuspendWorker suspends a worker instance
	SuspendWorker(ctx context.Context, name string, activeWorker WorkerInfo) error
	// ResumeWorker resumes a suspended worker instance
	ResumeWorker(ctx context.Context, name string, suspendedWorker WorkerInfo) error
}

// SyncBackend defines the synchronous operations that each backend must implement
type SyncBackend interface {
	// GenerateWorkerName generates a unique name for a new worker
	GenerateWorkerName() string

	// CreateWorker synchronously creates a worker with the given name
	// Returns WorkerInfo containing state and backend-specific data
	CreateWorker(ctx context.Context, name string) (WorkerInfo, error)

	// DeleteWorker synchronously deletes the worker with the given name
	// activeWorker contains the current cache of the specific active worker
	DeleteWorker(ctx context.Context, name string, activeWorker WorkerInfo) error

	// ListRemoteWorkers lists workers from the remote provider
	// Returns a map of worker name to WorkerInfo (includes state)
	ListRemoteWorkers(ctx context.Context) (map[string]WorkerInfo, error)
}

// asyncBackendManager manages async operations and local caching for backends
type asyncBackendManager struct {
	backend      SyncBackend
	resumable    Resumable // nil if backend doesn't support resume
	maxSuspended int       // max number of workers to keep suspended (0 = no suspend)

	mu                 sync.RWMutex
	activeWorkers      map[string]WorkerInfo // worker name -> worker info
	suspendedWorkers   map[string]WorkerInfo // suspended worker name -> worker info
	creatingWorkers    map[string]chan struct{}
	terminatingWorkers map[string]struct{}
	suspendingWorkers  map[string]struct{}
	resumingWorkers    map[string]struct{}

	lastOp chan struct{}
}

// MakeAsync wraps a SyncBackend with async operation management (without suspend/resume)
func MakeAsync(backend SyncBackend) broker.ResourcePoolBackend {
	return &asyncBackendManager{
		backend:            backend,
		resumable:          nil,
		maxSuspended:       0,
		activeWorkers:      make(map[string]WorkerInfo),
		suspendedWorkers:   make(map[string]WorkerInfo),
		creatingWorkers:    make(map[string]chan struct{}),
		terminatingWorkers: make(map[string]struct{}),
		suspendingWorkers:  make(map[string]struct{}),
		resumingWorkers:    make(map[string]struct{}),
	}
}

// MakeAsyncResumable wraps a SyncBackend with async operation management and suspend/resume support
// maxSuspended is the maximum number of workers to keep suspended (0 = no suspend, always delete)
func MakeAsyncResumable(backend SyncBackend, maxSuspended int) broker.ResourcePoolBackend {
	// Check if backend implements Resumable interface
	resumable, _ := backend.(Resumable)

	return &asyncBackendManager{
		backend:            backend,
		resumable:          resumable,
		maxSuspended:       maxSuspended,
		activeWorkers:      make(map[string]WorkerInfo),
		suspendedWorkers:   make(map[string]WorkerInfo),
		creatingWorkers:    make(map[string]chan struct{}),
		terminatingWorkers: make(map[string]struct{}),
		suspendingWorkers:  make(map[string]struct{}),
		resumingWorkers:    make(map[string]struct{}),
	}
}

// RequestScaleUp implements broker.ResourcePoolBackend
func (m *asyncBackendManager) RequestScaleUp(ctx context.Context) (string, error) {
	m.mu.Lock()

	// Try to resume a suspended worker first if resumable backend
	if m.resumable != nil && len(m.suspendedWorkers) > 0 {
		// Pick first suspended worker
		var name string
		var workerInfo WorkerInfo
		for n, w := range m.suspendedWorkers {
			name = n
			workerInfo = w
			break
		}

		// Mark as resuming
		delete(m.suspendedWorkers, name)
		m.resumingWorkers[name] = struct{}{}

		op := make(chan struct{})
		lastOp := make(chan struct{})
		m.lastOp = lastOp
		m.creatingWorkers[name] = op
		m.mu.Unlock()

		go func() {
			defer func() {
				m.mu.Lock()
				delete(m.creatingWorkers, name)
				delete(m.resumingWorkers, name)
				m.mu.Unlock()
				close(op)
				close(lastOp)
			}()

			err := m.resumable.ResumeWorker(ctx, name, workerInfo)
			if err != nil {
				log.Printf("Failed to resume worker %s: %v", name, err)
				return
			}

			// Update state to active
			workerInfo.State = WorkerStateActive

			m.mu.Lock()
			m.activeWorkers[name] = workerInfo
			m.mu.Unlock()
		}()

		return name, nil
	}

	// Generate unique name from backend, checking for duplicates
	var name string
	for {
		name = m.backend.GenerateWorkerName()
		if _, exists := m.creatingWorkers[name]; !exists {
			if _, exists := m.activeWorkers[name]; !exists {
				if _, exists := m.suspendedWorkers[name]; !exists {
					break
				}
			}
		}
	}

	op := make(chan struct{})
	lastOp := make(chan struct{})
	m.lastOp = lastOp
	m.creatingWorkers[name] = op
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.creatingWorkers, name)
			m.mu.Unlock()
			close(op)
			close(lastOp)
		}()

		workerInfo, err := m.backend.CreateWorker(ctx, name)
		if err != nil {
			log.Printf("Failed to create worker %s: %v", name, err)
			return
		}

		// Update active workers cache
		m.mu.Lock()
		m.activeWorkers[name] = workerInfo
		m.mu.Unlock()
	}()

	return name, nil
}

// RequestDelete initiates an async delete operation
// If the backend is resumable and we haven't reached maxSuspended, suspends the worker instead
func (m *asyncBackendManager) RequestDelete(ctx context.Context, workerName string) error {
	var creatingOp chan struct{}
	m.mu.Lock()
	creatingOp = m.creatingWorkers[workerName]

	// Decide whether to suspend or terminate
	// Count both suspended and suspending workers to avoid exceeding the limit
	totalSuspended := len(m.suspendedWorkers) + len(m.suspendingWorkers)
	shouldSuspend := m.resumable != nil && m.maxSuspended > 0 && totalSuspended < m.maxSuspended

	if shouldSuspend {
		m.suspendingWorkers[workerName] = struct{}{}
	} else {
		m.terminatingWorkers[workerName] = struct{}{}
	}

	op := make(chan struct{})
	m.lastOp = op
	m.mu.Unlock()

	go func() {
		defer close(op)

		// Wait for creation to complete if in progress
		if creatingOp != nil {
			<-creatingOp
		}

		// Get current active workers snapshot
		m.mu.RLock()
		workerInfo, exists := m.activeWorkers[workerName]
		m.mu.RUnlock()

		if !exists {
			log.Printf("Worker %s not found in active workers", workerName)
			m.mu.Lock()
			if shouldSuspend {
				delete(m.suspendingWorkers, workerName)
			} else {
				delete(m.terminatingWorkers, workerName)
			}
			m.mu.Unlock()
			return
		}

		if shouldSuspend {
			// Suspend the worker
			err := m.resumable.SuspendWorker(ctx, workerName, workerInfo)
			if err != nil {
				log.Printf("Failed to suspend worker %s: %v", workerName, err)
				m.mu.Lock()
				delete(m.suspendingWorkers, workerName)
				m.mu.Unlock()
				return
			}

			// Update state to suspended
			workerInfo.State = WorkerStateSuspended

			// Move from active to suspended
			m.mu.Lock()
			delete(m.activeWorkers, workerName)
			delete(m.suspendingWorkers, workerName)
			m.suspendedWorkers[workerName] = workerInfo
			m.mu.Unlock()
		} else {
			// Terminate the worker
			err := m.backend.DeleteWorker(ctx, workerName, workerInfo)
			if err != nil {
				log.Printf("Failed to delete worker %s: %v", workerName, err)
				m.mu.Lock()
				delete(m.terminatingWorkers, workerName)
				m.mu.Unlock()
				return
			}

			// Remove from active workers cache
			m.mu.Lock()
			delete(m.activeWorkers, workerName)
			delete(m.terminatingWorkers, workerName)
			m.mu.Unlock()
		}
	}()

	return nil
}

// ListWorkers returns the combined list of workers (creating + remote - terminating - suspending)
func (m *asyncBackendManager) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	seen := make(map[string]struct{})
	var res []broker.WorkerStatus

	// Add creating/resuming workers
	m.mu.RLock()
	for creatingWorker := range m.creatingWorkers {
		res = append(res, broker.WorkerStatus{Name: creatingWorker})
		seen[creatingWorker] = struct{}{}
	}
	// Mark terminating/suspending workers as seen (exclude them)
	for terminatingWorker := range m.terminatingWorkers {
		seen[terminatingWorker] = struct{}{}
	}
	for suspendingWorker := range m.suspendingWorkers {
		seen[suspendingWorker] = struct{}{}
	}
	m.mu.RUnlock()

	// List remote workers
	remoteWorkers, err := m.backend.ListRemoteWorkers(ctx)
	if err != nil {
		return nil, err
	}

	// Update active and suspended workers cache based on remote state
	m.mu.Lock()
	// Clear existing cache
	m.activeWorkers = make(map[string]WorkerInfo)

	// Rebuild cache from remote workers, separating active from suspended
	for workerName, workerInfo := range remoteWorkers {
		if workerInfo.State == WorkerStateSuspended {
			// Don't overwrite if already in suspended cache (might be resuming)
			if _, resuming := m.resumingWorkers[workerName]; !resuming {
				m.suspendedWorkers[workerName] = workerInfo
			}
		} else {
			m.activeWorkers[workerName] = workerInfo
		}
	}
	m.mu.Unlock()

	// Add remote workers to result (only active ones)
	for workerName, workerInfo := range remoteWorkers {
		if _, exists := seen[workerName]; exists {
			continue
		}
		if workerInfo.State == WorkerStateActive {
			res = append(res, broker.WorkerStatus{Name: workerName})
			seen[workerName] = struct{}{}
		}
	}

	return res, nil
}

// WaitForLastOperation waits for the last async operation to complete
func (m *asyncBackendManager) WaitForLastOperation(ctx context.Context) error {
	m.mu.RLock()
	op := m.lastOp
	m.mu.RUnlock()

	if op != nil {
		<-op
	}
	return nil
}
