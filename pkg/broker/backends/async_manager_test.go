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
	"fmt"
	"sync"
	"testing"
	"time"

	"velda.io/velda/pkg/broker"
)

// mockSyncBackend is a mock implementation of SyncBackend for testing
type mockSyncBackend struct {
	mu               sync.Mutex
	workers          map[string]WorkerInfo
	suspendedWks     map[string]WorkerInfo
	namePrefix       string
	createDelay      time.Duration
	deleteDelay      time.Duration
	suspendDelay     time.Duration
	resumeDelay      time.Duration
	createErr        error
	deleteErr        error
	suspendErr       error
	resumeErr        error
	listErr          error
	createCallCount  int
	deleteCallCount  int
	suspendCallCount int
	resumeCallCount  int
	listCallCount    int
}

func newMockSyncBackend() *mockSyncBackend {
	return &mockSyncBackend{
		workers:      make(map[string]WorkerInfo),
		suspendedWks: make(map[string]WorkerInfo),
		namePrefix:   "worker",
	}
}

func (m *mockSyncBackend) GenerateWorkerName() string {
	return fmt.Sprintf("%s-%s", m.namePrefix, time.Now().Format("150405.000"))
}

func (m *mockSyncBackend) CreateWorker(ctx context.Context, name string) (WorkerInfo, error) {
	m.mu.Lock()
	m.createCallCount++
	m.mu.Unlock()

	if m.createDelay > 0 {
		time.Sleep(m.createDelay)
	}

	if m.createErr != nil {
		return WorkerInfo{}, m.createErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	workerInfo := WorkerInfo{
		State: WorkerStateActive,
		Data:  name + "-id",
	}
	m.workers[name] = workerInfo
	return workerInfo, nil
}

func (m *mockSyncBackend) DeleteWorker(ctx context.Context, name string, activeWorker WorkerInfo) error {
	m.mu.Lock()
	m.deleteCallCount++
	m.mu.Unlock()

	if m.deleteDelay > 0 {
		time.Sleep(m.deleteDelay)
	}

	if m.deleteErr != nil {
		return m.deleteErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.workers, name)
	return nil
}

func (m *mockSyncBackend) ListRemoteWorkers(ctx context.Context) (map[string]WorkerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCallCount++

	if m.listErr != nil {
		return nil, m.listErr
	}

	result := make(map[string]WorkerInfo)
	for k, v := range m.workers {
		result[k] = v
	}
	// Include suspended workers in the list
	for k, v := range m.suspendedWks {
		result[k] = v
	}
	return result, nil
}

// Implement Resumable interface
func (m *mockSyncBackend) SuspendWorker(ctx context.Context, name string, activeWorker WorkerInfo) error {
	m.mu.Lock()
	m.suspendCallCount++
	m.mu.Unlock()

	if m.suspendDelay > 0 {
		time.Sleep(m.suspendDelay)
	}

	if m.suspendErr != nil {
		return m.suspendErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.workers, name)
	suspendedInfo := WorkerInfo{
		State: WorkerStateSuspended,
		Data:  activeWorker.Data,
	}
	m.suspendedWks[name] = suspendedInfo
	return nil
}

func (m *mockSyncBackend) ResumeWorker(ctx context.Context, name string, suspendedWorker WorkerInfo) error {
	m.mu.Lock()
	m.resumeCallCount++
	m.mu.Unlock()

	if m.resumeDelay > 0 {
		time.Sleep(m.resumeDelay)
	}

	if m.resumeErr != nil {
		return m.resumeErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.suspendedWks, name)
	resumedInfo := WorkerInfo{
		State: WorkerStateActive,
		Data:  suspendedWorker.Data,
	}
	m.workers[name] = resumedInfo
	return nil
}

func (m *mockSyncBackend) getWorkerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.workers)
}

func TestMakeAsync(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)

	if manager == nil {
		t.Fatal("MakeAsync returned nil")
	}

	// Verify it implements the interface
	var _ broker.ResourcePoolBackend = manager
}

func TestRequestScaleUp(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Request scale up
	name, err := manager.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp failed: %v", err)
	}

	if name == "" {
		t.Fatal("RequestScaleUp returned empty name")
	}

	// Name should start with prefix
	if len(name) < 8 || name[:6] != "worker" {
		t.Errorf("Expected name to start with 'worker', got: %s", name)
	}

	// Wait for creation to complete
	time.Sleep(100 * time.Millisecond)

	if backend.createCallCount != 1 {
		t.Errorf("Expected 1 create call, got %d", backend.createCallCount)
	}

	if backend.getWorkerCount() != 1 {
		t.Errorf("Expected 1 worker, got %d", backend.getWorkerCount())
	}
}

func TestRequestScaleUpMultiple(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Request multiple scale ups
	names := make([]string, 5)
	for i := 0; i < 5; i++ {
		name, err := manager.RequestScaleUp(ctx)
		if err != nil {
			t.Fatalf("RequestScaleUp %d failed: %v", i, err)
		}
		names[i] = name
	}

	// Wait for all creations to complete
	time.Sleep(200 * time.Millisecond)

	// Check all names are unique
	nameSet := make(map[string]bool)
	for _, name := range names {
		if nameSet[name] {
			t.Errorf("Duplicate name generated: %s", name)
		}
		nameSet[name] = true
	}

	if backend.createCallCount != 5 {
		t.Errorf("Expected 5 create calls, got %d", backend.createCallCount)
	}

	if backend.getWorkerCount() != 5 {
		t.Errorf("Expected 5 workers, got %d", backend.getWorkerCount())
	}
}

func TestRequestScaleUpWithError(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createErr = fmt.Errorf("create error")
	manager := MakeAsync(backend)

	ctx := context.Background()

	name, err := manager.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp should not fail synchronously: %v", err)
	}

	// Name should still be returned even though creation will fail async
	if name == "" {
		t.Fatal("RequestScaleUp returned empty name")
	}

	// Wait for async operation
	time.Sleep(100 * time.Millisecond)

	// Worker should not be created due to error
	if backend.getWorkerCount() != 0 {
		t.Errorf("Expected 0 workers due to error, got %d", backend.getWorkerCount())
	}
}

func TestRequestDelete(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-test1"] = WorkerInfo{State: WorkerStateActive, Data: "worker-test1-id"}
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Call ListWorkers to populate activeWorkers cache
	_, _ = manager.ListWorkers(ctx)

	err := manager.RequestDelete(ctx, "worker-test1")
	if err != nil {
		t.Fatalf("RequestDelete failed: %v", err)
	}

	// Wait for deletion to complete
	time.Sleep(100 * time.Millisecond)

	if backend.deleteCallCount != 1 {
		t.Errorf("Expected 1 delete call, got %d", backend.deleteCallCount)
	}

	if backend.getWorkerCount() != 0 {
		t.Errorf("Expected 0 workers after delete, got %d", backend.getWorkerCount())
	}
}

func TestRequestDeleteWaitsForCreation(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createDelay = 200 * time.Millisecond
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Start creation
	name, err := manager.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp failed: %v", err)
	}

	// Immediately request deletion
	time.Sleep(10 * time.Millisecond)
	err = manager.RequestDelete(ctx, name)
	if err != nil {
		t.Fatalf("RequestDelete failed: %v", err)
	}

	// Wait for both operations to complete
	time.Sleep(400 * time.Millisecond)

	// Creation should complete before deletion
	if backend.createCallCount != 1 {
		t.Errorf("Expected 1 create call, got %d", backend.createCallCount)
	}

	if backend.deleteCallCount != 1 {
		t.Errorf("Expected 1 delete call, got %d", backend.deleteCallCount)
	}

	// Final state should have no workers
	if backend.getWorkerCount() != 0 {
		t.Errorf("Expected 0 workers, got %d", backend.getWorkerCount())
	}
}

func TestListWorkers(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-1"] = WorkerInfo{State: WorkerStateActive, Data: "id-1"}
	backend.workers["worker-2"] = WorkerInfo{State: WorkerStateActive, Data: "id-2"}
	manager := MakeAsync(backend)

	ctx := context.Background()

	workers, err := manager.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}

	if len(workers) != 2 {
		t.Errorf("Expected 2 workers, got %d", len(workers))
	}

	// Check worker names
	names := make(map[string]bool)
	for _, w := range workers {
		names[w.Name] = true
	}

	if !names["worker-1"] || !names["worker-2"] {
		t.Errorf("Missing expected workers. Got: %v", names)
	}
}

func TestListWorkersIncludesCreating(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createDelay = 200 * time.Millisecond
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Start creation (will be slow)
	name, err := manager.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp failed: %v", err)
	}

	// List workers while creation is in progress
	time.Sleep(50 * time.Millisecond)
	workers, err := manager.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}

	// Should include the creating worker
	found := false
	for _, w := range workers {
		if w.Name == name {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Creating worker %s not found in list", name)
	}
}

func TestListWorkersExcludesTerminating(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-1"] = WorkerInfo{State: WorkerStateActive, Data: "id-1"}
	backend.deleteDelay = 200 * time.Millisecond
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Call ListWorkers first to populate activeWorkers cache
	_, _ = manager.ListWorkers(ctx)

	// Start deletion (will be slow)
	err := manager.RequestDelete(ctx, "worker-1")
	if err != nil {
		t.Fatalf("RequestDelete failed: %v", err)
	}

	// List workers while deletion is in progress
	time.Sleep(50 * time.Millisecond)
	workers, err := manager.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}

	// Should not include the terminating worker
	for _, w := range workers {
		if w.Name == "worker-1" {
			t.Errorf("Terminating worker should not be in list")
		}
	}
}

func TestListWorkersWithError(t *testing.T) {
	backend := newMockSyncBackend()
	backend.listErr = fmt.Errorf("list error")
	manager := MakeAsync(backend)

	ctx := context.Background()

	_, err := manager.ListWorkers(ctx)
	if err == nil {
		t.Fatal("Expected error from ListWorkers")
	}

	if err.Error() != "list error" {
		t.Errorf("Expected 'list error', got: %v", err)
	}
}

func TestWaitForLastOperation(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createDelay = 100 * time.Millisecond
	mgr := MakeAsync(backend).(*asyncBackendManager)

	ctx := context.Background()

	// Start an operation
	_, err := mgr.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp failed: %v", err)
	}

	start := time.Now()

	// Wait for it to complete
	err = mgr.WaitForLastOperation(ctx)
	if err != nil {
		t.Fatalf("WaitForLastOperation failed: %v", err)
	}

	elapsed := time.Since(start)

	// Should have waited at least the create delay
	if elapsed < 100*time.Millisecond {
		t.Errorf("WaitForLastOperation returned too quickly: %v", elapsed)
	}

	if backend.createCallCount != 1 {
		t.Errorf("Expected 1 create call, got %d", backend.createCallCount)
	}
}

func TestWaitForLastOperationNoOp(t *testing.T) {
	backend := newMockSyncBackend()
	mgr := MakeAsync(backend).(*asyncBackendManager)

	ctx := context.Background()

	// Wait without any operations - should return immediately
	start := time.Now()
	err := mgr.WaitForLastOperation(ctx)
	if err != nil {
		t.Fatalf("WaitForLastOperation failed: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed > 10*time.Millisecond {
		t.Errorf("WaitForLastOperation took too long with no ops: %v", elapsed)
	}
}

func TestConcurrentOperations(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)

	ctx := context.Background()

	// Start multiple operations concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.RequestScaleUp(ctx)
			if err != nil {
				t.Errorf("RequestScaleUp failed: %v", err)
			}
		}()
	}

	wg.Wait()

	// Wait for all operations to complete
	time.Sleep(200 * time.Millisecond)

	if backend.createCallCount != 10 {
		t.Errorf("Expected 10 create calls, got %d", backend.createCallCount)
	}

	workers, err := manager.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}

	if len(workers) != 10 {
		t.Errorf("Expected 10 workers, got %d", len(workers))
	}
}

func TestMakeAsyncResumable(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 3)

	if manager == nil {
		t.Fatal("MakeAsyncResumable returned nil")
	}

	// Verify it implements the interface
	var _ broker.ResourcePoolBackend = manager

	// Verify maxSuspended is set
	mgr := manager.(*asyncBackendManager)
	if mgr.maxSuspended != 3 {
		t.Errorf("Expected maxSuspended=3, got %d", mgr.maxSuspended)
	}
	if mgr.resumable == nil {
		t.Error("Expected resumable to be set")
	}
}

func TestSuspendWorker(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 3)
	ctx := context.Background()

	// Create a worker
	name, err := manager.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp failed: %v", err)
	}

	// Wait for creation
	time.Sleep(50 * time.Millisecond)

	// Request delete - should suspend instead
	err = manager.RequestDelete(ctx, name)
	if err != nil {
		t.Fatalf("RequestDelete failed: %v", err)
	}

	// Wait for suspend
	time.Sleep(50 * time.Millisecond)

	if backend.deleteCallCount != 0 {
		t.Errorf("Expected 0 delete calls, got %d", backend.deleteCallCount)
	}
	if backend.suspendCallCount != 1 {
		t.Errorf("Expected 1 suspend call, got %d", backend.suspendCallCount)
	}

	// Check suspended workers
	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	if len(mgr.suspendedWorkers) != 1 {
		t.Errorf("Expected 1 suspended worker, got %d", len(mgr.suspendedWorkers))
	}
	if len(mgr.activeWorkers) != 0 {
		t.Errorf("Expected 0 active workers, got %d", len(mgr.activeWorkers))
	}
	mgr.mu.RUnlock()
}

func TestResumeWorker(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 3)
	ctx := context.Background()

	// Create and suspend a worker
	name1, _ := manager.RequestScaleUp(ctx)
	time.Sleep(50 * time.Millisecond)
	manager.RequestDelete(ctx, name1)
	time.Sleep(50 * time.Millisecond)

	// Now request scale up - should resume the suspended worker
	name2, err := manager.RequestScaleUp(ctx)
	if err != nil {
		t.Fatalf("RequestScaleUp failed: %v", err)
	}

	// Should resume existing worker
	if name2 != name1 {
		t.Errorf("Expected to resume worker %s, got %s", name1, name2)
	}

	// Wait for resume
	time.Sleep(50 * time.Millisecond)

	if backend.createCallCount != 1 {
		t.Errorf("Expected 1 create call, got %d", backend.createCallCount)
	}
	if backend.resumeCallCount != 1 {
		t.Errorf("Expected 1 resume call, got %d", backend.resumeCallCount)
	}

	// Check workers state
	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	if len(mgr.suspendedWorkers) != 0 {
		t.Errorf("Expected 0 suspended workers, got %d", len(mgr.suspendedWorkers))
	}
	if len(mgr.activeWorkers) != 1 {
		t.Errorf("Expected 1 active worker, got %d", len(mgr.activeWorkers))
	}
	mgr.mu.RUnlock()
}

func TestSuspendMaxLimit(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 2) // Max 2 suspended
	ctx := context.Background()

	// Create 3 workers
	var names []string
	for i := 0; i < 3; i++ {
		name, _ := manager.RequestScaleUp(ctx)
		names = append(names, name)
	}
	time.Sleep(100 * time.Millisecond)

	// Delete all 3 - first 2 should suspend, 3rd should delete
	for _, name := range names {
		manager.RequestDelete(ctx, name)
	}
	time.Sleep(100 * time.Millisecond)

	if backend.suspendCallCount != 2 {
		t.Errorf("Expected 2 suspend calls, got %d", backend.suspendCallCount)
	}
	if backend.deleteCallCount != 1 {
		t.Errorf("Expected 1 delete call, got %d", backend.deleteCallCount)
	}

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	if len(mgr.suspendedWorkers) != 2 {
		t.Errorf("Expected 2 suspended workers, got %d", len(mgr.suspendedWorkers))
	}
	mgr.mu.RUnlock()
}

func TestListWorkersExcludesSuspended(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-1"] = WorkerInfo{State: WorkerStateActive, Data: "id-1"}
	backend.suspendedWks["worker-2"] = WorkerInfo{State: WorkerStateSuspended, Data: "id-2"}
	manager := MakeAsyncResumable(backend, 5)

	ctx := context.Background()
	workers, err := manager.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}

	// Should only include active workers
	if len(workers) != 1 {
		t.Errorf("Expected 1 worker, got %d", len(workers))
	}
	if len(workers) > 0 && workers[0].Name != "worker-1" {
		t.Errorf("Expected worker-1, got %s", workers[0].Name)
	}
}
