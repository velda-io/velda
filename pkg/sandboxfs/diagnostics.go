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
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type DebugHandleSnapshot struct {
	ID       uint64    `json:"id"`
	Kind     string    `json:"kind"`
	Path     string    `json:"path"`
	Access   string    `json:"access"`
	Backing  string    `json:"backing"`
	CacheOps bool      `json:"cacheOps"`
	OpenedAt time.Time `json:"openedAt"`
}

type DebugOperationSnapshot struct {
	ID        uint64    `json:"id"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Detail    string    `json:"detail"`
	StartedAt time.Time `json:"startedAt"`
}

type DebugSnapshot struct {
	OpenHandles   []DebugHandleSnapshot    `json:"openHandles"`
	InProgressOps []DebugOperationSnapshot `json:"inProgressOps"`
}

type DebugTracker struct {
	nextID uint64

	mu         sync.RWMutex
	handles    map[uint64]DebugHandleSnapshot
	operations map[uint64]DebugOperationSnapshot
}

func NewDebugTracker() *DebugTracker {
	return &DebugTracker{
		handles:    make(map[uint64]DebugHandleSnapshot),
		operations: make(map[uint64]DebugOperationSnapshot),
	}
}

func (d *DebugTracker) RegisterHandle(handle DebugHandleSnapshot) uint64 {
	if d == nil {
		return 0
	}
	handle.ID = atomic.AddUint64(&d.nextID, 1)
	handle.OpenedAt = time.Now()

	d.mu.Lock()
	d.handles[handle.ID] = handle
	d.mu.Unlock()

	return handle.ID
}

func (d *DebugTracker) UpdateHandle(id uint64, update func(*DebugHandleSnapshot)) {
	if d == nil || id == 0 || update == nil {
		return
	}

	d.mu.Lock()
	handle, ok := d.handles[id]
	if ok {
		update(&handle)
		d.handles[id] = handle
	}
	d.mu.Unlock()
}

func (d *DebugTracker) UnregisterHandle(id uint64) {
	if d == nil || id == 0 {
		return
	}

	d.mu.Lock()
	delete(d.handles, id)
	d.mu.Unlock()
}

func (d *DebugTracker) StartOperation(kind, path, detail string) uint64 {
	if d == nil {
		return 0
	}
	op := DebugOperationSnapshot{
		ID:        atomic.AddUint64(&d.nextID, 1),
		Kind:      kind,
		Path:      path,
		Detail:    detail,
		StartedAt: time.Now(),
	}

	d.mu.Lock()
	d.operations[op.ID] = op
	d.mu.Unlock()

	return op.ID
}

func (d *DebugTracker) FinishOperation(id uint64) {
	if d == nil || id == 0 {
		return
	}

	d.mu.Lock()
	delete(d.operations, id)
	d.mu.Unlock()
}

func (d *DebugTracker) Snapshot() DebugSnapshot {
	if d == nil {
		return DebugSnapshot{}
	}

	d.mu.RLock()
	handles := make([]DebugHandleSnapshot, 0, len(d.handles))
	for _, handle := range d.handles {
		handles = append(handles, handle)
	}
	operations := make([]DebugOperationSnapshot, 0, len(d.operations))
	for _, op := range d.operations {
		operations = append(operations, op)
	}
	d.mu.RUnlock()

	sort.Slice(handles, func(i, j int) bool {
		return handles[i].OpenedAt.Before(handles[j].OpenedAt)
	})
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].StartedAt.Before(operations[j].StartedAt)
	})

	return DebugSnapshot{
		OpenHandles:   handles,
		InProgressOps: operations,
	}
}
