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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	require.NotNil(t, manager)
	var _ broker.ResourcePoolBackend = manager
}

func TestRequestScaleUp(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)
	ctx := context.Background()

	name, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, name)
	assert.True(t, len(name) >= 7 && name[:6] == "worker", "expected name to start with 'worker', got: %s", name)

	time.Sleep(100 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 1, backend.createCallCount)
	mgr.mu.RUnlock()
	assert.Equal(t, 1, backend.getWorkerCount())
}

func TestRequestScaleUpMultiple(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)
	ctx := context.Background()

	names := make([]string, 5)
	for i := 0; i < 5; i++ {
		name, err := manager.RequestScaleUp(ctx)
		require.NoErrorf(t, err, "RequestScaleUp %d failed", i)
		names[i] = name
	}

	time.Sleep(200 * time.Millisecond)

	nameSet := make(map[string]bool)
	for _, name := range names {
		assert.False(t, nameSet[name], "duplicate name generated: %s", name)
		nameSet[name] = true
	}

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 5, backend.createCallCount)
	mgr.mu.RUnlock()
	assert.Equal(t, 5, backend.getWorkerCount())
}

func TestRequestScaleUpWithError(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createErr = fmt.Errorf("create error")
	manager := MakeAsync(backend)
	ctx := context.Background()

	name, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err, "RequestScaleUp should not fail synchronously")
	assert.NotEmpty(t, name)

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, backend.getWorkerCount())
}

func TestRequestScaleUpWithErrorEmitsEvent(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createErr = status.Error(codes.ResourceExhausted, "resource exhausted")
	mgr := MakeAsync(backend).(*asyncBackendManager)
	ctx := context.Background()

	name, err := mgr.RequestScaleUp(ctx)
	require.NoError(t, err, "RequestScaleUp should not fail synchronously")
	require.NotEmpty(t, name)

	select {
	case event := <-mgr.Events():
		assert.Equal(t, name, event.WorkerName)
		assert.Equal(t, broker.ResourcePoolEventTypeResourceExhausted, event.EventType)
		require.NotNil(t, event.Err)
		st, ok := status.FromError(event.Err)
		require.True(t, ok, "expected grpc status error, got %T", event.Err)
		assert.Equal(t, codes.ResourceExhausted, st.Code())
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for creation error event")
	}
}

func TestResumeWorkerErrorEmitsEvent(t *testing.T) {
	backend := newMockSyncBackend()
	backend.resumeErr = fmt.Errorf("resume failed")
	mgr := MakeAsyncResumable(backend, 1).(*asyncBackendManager)
	ctx := context.Background()

	backend.suspendedWks["worker-suspended"] = WorkerInfo{State: WorkerStateSuspended, Data: "id"}
	mgr.suspendedWorkers["worker-suspended"] = WorkerInfo{State: WorkerStateSuspended, Data: "id"}

	name, err := mgr.RequestScaleUp(ctx)
	require.NoError(t, err, "RequestScaleUp should not fail synchronously")
	assert.Equal(t, "worker-suspended", name)

	select {
	case event := <-mgr.Events():
		assert.Equal(t, "worker-suspended", event.WorkerName)
		assert.Equal(t, broker.ResourcePoolEventTypeStartupFailure, event.EventType)
		assert.ErrorIs(t, event.Err, backend.resumeErr)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resume error event")
	}
}

func TestRequestDelete(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-test1"] = WorkerInfo{State: WorkerStateActive, Data: "worker-test1-id"}
	manager := MakeAsync(backend)
	ctx := context.Background()

	_, _ = manager.ListWorkers(ctx)

	err := manager.RequestDelete(ctx, "worker-test1")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 1, backend.deleteCallCount)
	mgr.mu.RUnlock()
	assert.Equal(t, 0, backend.getWorkerCount())
}

func TestRequestDeleteWaitsForCreation(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createDelay = 200 * time.Millisecond
	manager := MakeAsync(backend)
	ctx := context.Background()

	name, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	err = manager.RequestDelete(ctx, name)
	require.NoError(t, err)

	time.Sleep(400 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 1, backend.createCallCount)
	assert.Equal(t, 1, backend.deleteCallCount)
	mgr.mu.RUnlock()
	assert.Equal(t, 0, backend.getWorkerCount())
}

func TestListWorkers(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-1"] = WorkerInfo{State: WorkerStateActive, Data: "id-1"}
	backend.workers["worker-2"] = WorkerInfo{State: WorkerStateActive, Data: "id-2"}
	manager := MakeAsync(backend)
	ctx := context.Background()

	workers, err := manager.ListWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, workers, 2)

	names := make(map[string]bool)
	for _, w := range workers {
		names[w.Name] = true
	}
	assert.True(t, names["worker-1"] && names["worker-2"], "missing expected workers, got: %v", names)
}

func TestListWorkersIncludesCreating(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createDelay = 200 * time.Millisecond
	manager := MakeAsync(backend)
	ctx := context.Background()

	name, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	workers, err := manager.ListWorkers(ctx)
	require.NoError(t, err)

	found := false
	for _, w := range workers {
		if w.Name == name {
			found = true
			break
		}
	}
	assert.True(t, found, "creating worker %s not found in list", name)
}

func TestListWorkersExcludesTerminating(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-1"] = WorkerInfo{State: WorkerStateActive, Data: "id-1"}
	backend.deleteDelay = 200 * time.Millisecond
	manager := MakeAsync(backend)
	ctx := context.Background()

	_, _ = manager.ListWorkers(ctx)

	err := manager.RequestDelete(ctx, "worker-1")
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	workers, err := manager.ListWorkers(ctx)
	require.NoError(t, err)

	for _, w := range workers {
		assert.NotEqual(t, "worker-1", w.Name, "terminating worker should not be in list")
	}
}

func TestListWorkersWithError(t *testing.T) {
	backend := newMockSyncBackend()
	backend.listErr = fmt.Errorf("list error")
	manager := MakeAsync(backend)
	ctx := context.Background()

	_, err := manager.ListWorkers(ctx)
	require.Error(t, err)
	assert.EqualError(t, err, "list error")
}

func TestWaitForLastOperation(t *testing.T) {
	backend := newMockSyncBackend()
	backend.createDelay = 100 * time.Millisecond
	mgr := MakeAsync(backend).(*asyncBackendManager)
	ctx := context.Background()

	_, err := mgr.RequestScaleUp(ctx)
	require.NoError(t, err)

	start := time.Now()
	err = mgr.WaitForLastOperation(ctx)
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "WaitForLastOperation returned too quickly: %v", elapsed)
	assert.Equal(t, 1, backend.createCallCount)
}

func TestWaitForLastOperationNoOp(t *testing.T) {
	backend := newMockSyncBackend()
	mgr := MakeAsync(backend).(*asyncBackendManager)
	ctx := context.Background()

	start := time.Now()
	err := mgr.WaitForLastOperation(ctx)
	require.NoError(t, err)

	elapsed := time.Since(start)
	assert.Less(t, elapsed, 10*time.Millisecond, "WaitForLastOperation took too long with no ops: %v", elapsed)
}

func TestConcurrentOperations(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsync(backend)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.RequestScaleUp(ctx)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	time.Sleep(200 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 10, backend.createCallCount)
	mgr.mu.RUnlock()

	workers, err := manager.ListWorkers(ctx)
	require.NoError(t, err)
	assert.Len(t, workers, 10)
}

func TestMakeAsyncResumable(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 3)

	require.NotNil(t, manager)
	var _ broker.ResourcePoolBackend = manager

	mgr := manager.(*asyncBackendManager)
	assert.Equal(t, 3, mgr.maxSuspended)
	assert.NotNil(t, mgr.resumable)
}

func TestSuspendWorker(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 3)
	ctx := context.Background()

	name, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	err = manager.RequestDelete(ctx, name)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 0, backend.deleteCallCount)
	assert.Equal(t, 1, backend.suspendCallCount)
	assert.Len(t, mgr.suspendedWorkers, 1)
	assert.Empty(t, mgr.activeWorkers)
	mgr.mu.RUnlock()
}

func TestResumeWorker(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 3)
	ctx := context.Background()

	name1, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, manager.RequestDelete(ctx, name1))
	time.Sleep(50 * time.Millisecond)

	name2, err := manager.RequestScaleUp(ctx)
	require.NoError(t, err)
	assert.Equal(t, name1, name2, "expected to resume existing worker")

	time.Sleep(50 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 1, backend.createCallCount)
	assert.Equal(t, 1, backend.resumeCallCount)
	assert.Empty(t, mgr.suspendedWorkers)
	assert.Len(t, mgr.activeWorkers, 1)
	mgr.mu.RUnlock()
}

func TestSuspendMaxLimit(t *testing.T) {
	backend := newMockSyncBackend()
	manager := MakeAsyncResumable(backend, 2)
	ctx := context.Background()

	var names []string
	for i := 0; i < 3; i++ {
		name, err := manager.RequestScaleUp(ctx)
		require.NoError(t, err)
		names = append(names, name)
	}
	time.Sleep(100 * time.Millisecond)

	for _, name := range names {
		require.NoError(t, manager.RequestDelete(ctx, name))
	}
	time.Sleep(100 * time.Millisecond)

	mgr := manager.(*asyncBackendManager)
	mgr.mu.RLock()
	assert.Equal(t, 2, backend.suspendCallCount)
	assert.Equal(t, 1, backend.deleteCallCount)
	assert.Len(t, mgr.suspendedWorkers, 2)
	mgr.mu.RUnlock()
}

func TestListWorkersExcludesSuspended(t *testing.T) {
	backend := newMockSyncBackend()
	backend.workers["worker-1"] = WorkerInfo{State: WorkerStateActive, Data: "id-1"}
	backend.suspendedWks["worker-2"] = WorkerInfo{State: WorkerStateSuspended, Data: "id-2"}
	manager := MakeAsyncResumable(backend, 5)
	ctx := context.Background()

	workers, err := manager.ListWorkers(ctx)
	require.NoError(t, err)
	require.Len(t, workers, 1)
	assert.Equal(t, "worker-1", workers[0].Name)
}

func TestSilentRemoveSuspendedWorkerStartsNewAfterSync(t *testing.T) {
	backend := newMockSyncBackend()
	mgr := MakeAsyncResumable(backend, 3).(*asyncBackendManager)
	ctx := context.Background()

	backend.suspendedWks["worker-silent"] = WorkerInfo{State: WorkerStateSuspended, Data: "id-silent"}
	mgr.suspendedWorkers["worker-silent"] = WorkerInfo{State: WorkerStateSuspended, Data: "id-silent"}

	delete(backend.suspendedWks, "worker-silent")

	_, err := mgr.ListWorkers(ctx)
	require.NoError(t, err)

	mgr.mu.RLock()
	_, stillCached := mgr.suspendedWorkers["worker-silent"]
	mgr.mu.RUnlock()
	assert.False(t, stillCached, "expected suspended worker to be removed from manager cache after sync")

	name, err := mgr.RequestScaleUp(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, "worker-silent", name, "expected a new worker name, got resumed name %s", name)

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 1, backend.createCallCount)
	assert.Equal(t, 0, backend.resumeCallCount)
}
