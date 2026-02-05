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
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	proto "velda.io/velda/pkg/proto/config"
)

var ErrPoolMaxSizeReached = errors.New("pool is full")
var ErrNotSupportingBatchScheduler = errors.New("pool does not support batch scheduling")

type WorkerStatusCode int

const (
	// This is not used.
	WorkerStatusNone WorkerStatusCode = iota
	// Worker that is requested but not connected to the server.
	WorkerStatusPending
	// Running at least one session.
	WorkerStatusRunning
	// Not running any session.
	WorkerStatusIdle
	// Worker was connected but lose connection, and the deletion is not requested by the scheduler.
	WorkerStatusLost
	// Worker has been requested to delete by the scheduler, and waiting for the worker to confirm deletion.
	WorkerStatusNotifyDeleting
	// Worker has confirmed to stop receiving tasks and request deleting, but the connection is still alive.
	WorkerStatusDeleting
	// Worker has been requested to delete by the scheduler, and connection is terminated.
	// Waiting to confirm that backend has marked it deleted.
	// This state can be skipped if the backend doesn't report terminating instances.
	WorkerStatusDisconnected
	// Worker was in shutting down state, but future action is bringing it back to running.
	// It's considered as PENDING + DELETING
	WorkerStatusNeedsRestart
	// Worker in the backend list, but was not connected with the scheduler and no previous interactions.
	// It may reconnect later. We treat it as a running worker until reconnected or timeout.
	WorkerStatusUnknown
	// This is not used.
	WorkerStatusMax
	// Workers being deleted do not have a record in AutoScalePool.
)

func (s WorkerStatusCode) String() string {
	switch s {
	case WorkerStatusNone:
		return "None"
	case WorkerStatusPending:
		return "Pending"
	case WorkerStatusRunning:
		return "Running"
	case WorkerStatusIdle:
		return "Idle"
	case WorkerStatusLost:
		return "Lost"
	case WorkerStatusNotifyDeleting:
		return "NotifyDeleting"
	case WorkerStatusDeleting:
		return "Deleting"
	case WorkerStatusUnknown:
		return "Unknown"
	case WorkerStatusDisconnected:
		return "Disconnected"
	default:
		return "Invalid"
	}
}

var nonActiveStatus = []WorkerStatusCode{
	WorkerStatusPending,
	WorkerStatusUnknown,
	WorkerStatusLost,
	WorkerStatusDisconnected}

type WorkerStatus struct {
	Name      string
	CreatedAt time.Time
	Status    WorkerStatusCode
}

type ResourcePoolBackend interface {
	RequestScaleUp(ctx context.Context) (string, error)
	RequestDelete(ctx context.Context, workerName string) error

	// used for checking inconsistency and identify zombie workers
	ListWorkers(ctx context.Context) ([]WorkerStatus, error)
}

type BatchedPoolBackend interface {
	RequestBatch(ctx context.Context, count int, label string) ([]string, error)
}

/*
Handler that manages the lifecycle of workers in a pool.

State machine of worker:

	None -> Unknown: Reconnect without agent update request
	None -> Pending: RequestScaleUp(By autoscaler)
	Pending -> Idle: NotifyAgentAvailable
	Running -> Idle: MarkIdle
	Idle -> Running: MarkBusy
	Idle -> Deleting: MarkDeleting(Agent requests deletion)
	Deleting -> DisConnected: MarkLost
	Running/Idle -> Lost: MarkLost
	DisConnected -> None: Removed during scanning
	Lost -> None: Removed during scanning
*/

const defaultSyncInterval = 1 * time.Minute

type workerDetail struct {
	statusChan    chan AgentStatusRequest
	availableSlot int
}

type AutoScaledPool struct {
	name                 string
	ctx                  context.Context
	backend              ResourcePoolBackend
	minIdle              int
	maxIdle              int
	minSize              int
	idleDecay            time.Duration
	syncLoopInterval     time.Duration
	killUnknownAfter     time.Duration
	maxSize              int
	defaultSlotsPerAgent int
	syncLoopTimer        *time.Ticker
	syncNow              chan struct{}

	currentSyncVersion atomic.Int32
	batch              bool

	mu              sync.Mutex
	pendingSessions int
	idleSlots       int
	// Workers that are not in the backend list.
	// Value is the creation time.
	workersByStatus map[WorkerStatusCode]map[string]int32
	runningWorkers  map[string]int32
	idleWorkers     map[string]int32
	pendingWorkers  map[string]int32
	deletingWorkers map[string]int32
	// Count of pending workers without names (when scaleUp returns empty name).
	// These will be assigned to unknown workers when they connect.
	pendingUnknownWorkers int
	// Includes all unknown, pending and lost workers (or workers without heartbeat with the server)
	// Value is the last seen time.
	// If the worker is not seen after killUnknownAfter, it will be deleted.
	lastKnownTime map[string]time.Time

	workerStatus map[string]WorkerStatusCode
	workerDetail map[string]*workerDetail

	killIdleTimer           *time.Timer
	killingIdle             bool
	readyForIdleMaintenance bool

	Metadata atomic.Pointer[proto.PoolMetadata]
}

type AutoScaledPoolConfig struct {
	Context              context.Context
	Backend              ResourcePoolBackend
	MinIdle              int
	MaxIdle              int
	IdleDecay            time.Duration
	MinSize              int
	MaxSize              int
	SyncLoopInterval     time.Duration
	KillUnknownAfter     time.Duration
	DefaultSlotsPerAgent int
	Batch                bool
	Metadata             *proto.PoolMetadata
}

func NewAutoScaledPool(name string, config AutoScaledPoolConfig) *AutoScaledPool {
	pool := &AutoScaledPool{
		name:            name,
		ctx:             config.Context,
		workersByStatus: make(map[WorkerStatusCode]map[string]int32),
		workerStatus:    make(map[string]WorkerStatusCode),
		workerDetail:    make(map[string]*workerDetail),
		syncNow:         make(chan struct{}, 1),
	}
	for i := 0; i < int(WorkerStatusMax); i++ {
		pool.workersByStatus[WorkerStatusCode(i)] = make(map[string]int32)
	}
	// Aliases
	pool.runningWorkers = pool.workersByStatus[WorkerStatusRunning]
	pool.idleWorkers = pool.workersByStatus[WorkerStatusIdle]
	pool.pendingWorkers = pool.workersByStatus[WorkerStatusPending]
	pool.deletingWorkers = pool.workersByStatus[WorkerStatusDeleting]
	pool.batch = config.Batch

	pool.lastKnownTime = make(map[string]time.Time)
	pool.killIdleTimer = time.AfterFunc(1000*time.Hour, pool.deleteOneIdleWorker)
	pool.killIdleTimer.Stop()
	pool.syncLoopInterval = config.SyncLoopInterval

	pool.syncLoopTimer = time.NewTicker(1000 * time.Hour)
	pool.syncLoopTimer.Stop()
	go pool.syncLoop(config.Context)
	context.AfterFunc(config.Context, pool.shutdown)

	pool.UpdateConfig(&config)
	return pool
}

func (p *AutoScaledPool) syncLoop(ctx context.Context) {
	defer p.syncLoopTimer.Stop()
	sync := func() {
		for {
			err := p.Reconnect(ctx)
			if err == backendChagnedError {
				// Retry with new backend immediately.
				continue
			}
			if err != nil {
				p.logPrintf("Failed to reconnect: %v", err)
			}
			break
		}
	}
	sync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.syncLoopTimer.C:
			sync()
		case <-p.syncNow:
			p.syncLoopTimer.Reset(p.syncLoopInterval)
			sync()
		}
	}
}

var backendChagnedError = errors.New("backend changed")

func (p *AutoScaledPool) Reconnect(ctx context.Context) error {
	p.mu.Lock()
	backend := p.backend
	p.mu.Unlock()
	if backend == nil {
		return nil
	}
	// Don't hold the lock while listing workers.
	curVersion := p.currentSyncVersion.Add(1)
	workerStatuses, err := backend.ListWorkers(ctx)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.backend != backend {
		return backendChagnedError
	}
	for _, status := range workerStatuses {
		name := status.Name
		currentStatus, ok := p.workerStatus[name]
		if !ok {
			p.setWorkerStatusLocked(name, WorkerStatusUnknown, 0)
			p.lastKnownTime[name] = time.Now()
			p.logPrintf("Found unknown worker %s", name)
		} else {
			p.workersByStatus[currentStatus][name] = curVersion
		}
	}
	for _, status := range nonActiveStatus {
		workers := p.workersByStatus[status]
		for name, value := range workers {
			if value != curVersion {
				p.logPrintf("Worker %s is removed from backend. Was %v", name, status)
				p.removeWorkerLocked(name)
			}
		}
	}
	// Check for unknown workers, if expired, request deletion.
	if p.killUnknownAfter > 0 {
		for name, lastConnect := range p.lastKnownTime {
			if time.Since(lastConnect) > p.killUnknownAfter {
				if err := p.backend.RequestDelete(ctx, name); err != nil {
					p.logPrintf("Failed to request deletion for unknown worker %s: %v", name, err)
				} else {
					p.removeWorkerLocked(name)
					p.logPrintf("Unknown worker %s is deleted. Last active: %v", name, lastConnect)
				}
			}
		}
	}
	p.maintainIdleWorkers()
	return nil
}

// RequestWorker requests a worker from the pool.
// It should always be called when a new session is createdd,
// and followed by RequestCancelled, MarkBusy, or NotifyAgentAvailable
// with assignedWork=true.
func (p *AutoScaledPool) RequestWorker() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.pendingSessions++
	p.maintainIdleWorkers()
	return nil
}

// Cancel one of the previous scale up action.
// To be invoked when:
// - One pending session is cancelled
func (p *AutoScaledPool) RequestCancelled() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pendingSessions--
	p.maintainIdleWorkers()
	return nil
}

// MarkBusy marks a worker from idle to busy.
func (p *AutoScaledPool) MarkBusy(workerName string, remainingSlot int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pendingSessions--
	p.setWorkerStatusLocked(workerName, WorkerStatusRunning, remainingSlot)
	p.maintainIdleWorkers()
	return nil
}

func (p *AutoScaledPool) MarkIdle(workerName string, remainingSlot int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	oldStatus := p.setWorkerStatusLocked(workerName, WorkerStatusIdle, remainingSlot)
	log.Printf("Worker %s marked idle, was %v", workerName, oldStatus)
	if oldStatus == WorkerStatusRunning {
		p.maintainIdleWorkers()
	}
	return nil
}

func (p *AutoScaledPool) MarkLost(workerName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.lastKnownTime[workerName] = time.Now()
	oldStatus := p.workerStatus[workerName]
	switch oldStatus {
	case WorkerStatusNone:
		p.logPrintf("Worker %s lost, was not in pool", workerName)
	case WorkerStatusNeedsRestart:
		p.setWorkerStatusLocked(workerName, WorkerStatusPending, -1)
	case WorkerStatusDeleting:
		p.setWorkerStatusLocked(workerName, WorkerStatusDisconnected, 0)
	default:
		p.setWorkerStatusLocked(workerName, WorkerStatusLost, 0)
		p.logPrintf("Worker %s lost, was %v", workerName, oldStatus)
	}

	p.maintainIdleWorkers()
	return nil
}

func (p *AutoScaledPool) MarkDeleting(workerName string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldStatus := p.workerStatus[workerName]
	p.logPrintf("Worker %s deleting, was %v", workerName, oldStatus)
	p.setWorkerStatusLocked(workerName, WorkerStatusDeleting, 0)
	if err := p.backend.RequestDelete(p.ctx, workerName); err != nil {
		p.logPrintf("Failed to request deletion for worker %s: %v", workerName, err)
	}
	p.maintainIdleWorkers()
	return nil
}

func (p *AutoScaledPool) NotifyAgentAvailable(name string, busy bool, statusChannel chan AgentStatusRequest, availableSlot int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var oldStatus WorkerStatusCode
	if busy {
		oldStatus = p.setWorkerStatusLocked(name, WorkerStatusRunning, availableSlot)
	} else {
		oldStatus = p.setWorkerStatusLocked(name, WorkerStatusIdle, availableSlot)
	}
	if oldStatus == WorkerStatusNone {
		if p.backend == nil {
			p.logPrintf("Worker %s Connected", name)
		} else if p.pendingUnknownWorkers > 0 {
			// Assign this worker to one of the pending unknown workers
			p.pendingUnknownWorkers--
			p.idleSlots -= p.defaultSlotsPerAgent
			p.logPrintf("Worker %s connected, assigned to pending unknown worker. Remaining unknown: %d", name, p.pendingUnknownWorkers)
		} else {
			p.logPrintf("Worker %s starting while not in pool", name)
		}
	} else if oldStatus == WorkerStatusUnknown {
		p.logPrintf("Unknown worker %s reconnected. Busy: %t", name, busy)
	}
	p.workerDetail[name].statusChan = statusChannel
	p.workerDetail[name].availableSlot = availableSlot

	p.maintainIdleWorkers()

	return nil
}

func (p *AutoScaledPool) SupportsBatch() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.batch {
		return false
	}
	_, ok := p.backend.(BatchedPoolBackend)
	return ok
}

func (p *AutoScaledPool) RequestBatch(ctx context.Context, count int, label string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.batch {
		return ErrNotSupportingBatchScheduler
	}
	batchBackend, ok := p.backend.(BatchedPoolBackend)
	if !ok {
		return ErrNotSupportingBatchScheduler
	}
	// TODO: Check size & queue the request.
	nodes, err := batchBackend.RequestBatch(ctx, count, label)
	if err != nil {
		return err
	}
	p.pendingSessions += count
	now := time.Now()
	for _, name := range nodes {
		p.lastKnownTime[name] = now
		p.setWorkerStatusLocked(name, WorkerStatusPending, p.defaultSlotsPerAgent)
	}
	return nil
}

func (p *AutoScaledPool) UpdateConfig(config *AutoScaledPoolConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	oldBackend := p.backend
	p.backend = config.Backend
	p.minIdle = config.MinIdle
	p.maxIdle = config.MaxIdle
	p.idleDecay = config.IdleDecay
	p.maxSize = config.MaxSize
	p.minSize = config.MinSize
	p.batch = config.Batch
	p.killUnknownAfter = config.KillUnknownAfter
	if p.killingIdle {
		p.killIdleTimer.Stop()
		p.killingIdle = false
	}
	oldInterval := p.syncLoopInterval
	p.syncLoopInterval = config.SyncLoopInterval
	if p.syncLoopInterval <= 0 {
		p.syncLoopInterval = defaultSyncInterval
	}
	p.defaultSlotsPerAgent = config.DefaultSlotsPerAgent
	if p.defaultSlotsPerAgent <= 0 {
		p.defaultSlotsPerAgent = 1
	}
	if p.maxIdle < p.minIdle+p.defaultSlotsPerAgent-1 {
		log.Printf("Pool %s: maxIdle (%d) is less than minIdle (%d) + defaultSlotsPerAgent (%d) - 1. Adjusting maxIdle to %d.",
			p.name, p.maxIdle, p.minIdle, p.defaultSlotsPerAgent, p.minIdle+p.defaultSlotsPerAgent-1)
		p.maxIdle = p.minIdle + p.defaultSlotsPerAgent - 1
	}
	if oldInterval != p.syncLoopInterval {
		p.syncLoopTimer.Reset(p.syncLoopInterval)
	}
	if oldBackend != p.backend {
		p.syncNow <- struct{}{}
	} else {
		p.maintainIdleWorkers()
	}
	p.Metadata.Store(config.Metadata)
}

func (p *AutoScaledPool) ReadyForIdleMaintenance() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.readyForIdleMaintenance {
		p.logPrintf("Ready for idle maintenance per request by initialDelay. Current unknown workers: %d", len(p.workersByStatus[WorkerStatusUnknown]))
		p.readyForIdleMaintenance = true
		p.maintainIdleWorkers()
	}
}

func (p *AutoScaledPool) maintainIdleWorkers() {
	// We will start worker management once we have all the information about each worker.
	if p.backend != nil && len(p.workersByStatus[WorkerStatusUnknown]) == 0 && !p.readyForIdleMaintenance {
		p.readyForIdleMaintenance = true
		p.logPrintf("No more unknown workers, start size maintenance")
	}
	if !p.readyForIdleMaintenance || p.backend == nil {
		return
	}
	if p.ctx.Err() != nil {
		return
	}
	for !p.batch && (p.idleSizeLocked() < p.minIdle || p.sizeLocked() < p.minSize) && p.sizeLocked() < p.maxSize {
		name, err := p.backend.RequestScaleUp(p.ctx)
		if err != nil {
			// TODO: Needs add backoff
			p.logPrintf("Failed to scale up: %v", err)
			break
		}
		if name == "" {
			// Backend doesn't return worker name. Track as pending unknown worker.
			p.pendingUnknownWorkers++
			p.idleSlots += p.defaultSlotsPerAgent
			p.logPrintf("Creating unknown worker, before idle size: %d, pending unknown: %d", p.idleSizeLocked(), p.pendingUnknownWorkers)
		} else {
			p.logPrintf("Creating worker %s, before idle size: %d", name, p.idleSizeLocked())
			p.lastKnownTime[name] = time.Now()
			p.setWorkerStatusLocked(name, WorkerStatusPending, p.defaultSlotsPerAgent)
		}
	}

	// Delete idle workers if there are too many.
	for p.sizeLocked() > p.minSize && p.idleSizeLocked() > p.maxIdle {
		if !p.deleteOneWorkerLocked() {
			break
		}
		p.killingIdle = false
		p.killIdleTimer.Stop()
	}
	if p.canKillIdleWorkerLocked() {
		if !p.killingIdle {
			p.logPrintf("Started idle decay timer")
			p.killIdleTimer.Reset(p.idleDecay)
			p.killingIdle = true
		}
	} else if p.killingIdle {
		p.killIdleTimer.Stop()
		p.killingIdle = false
	}
}

func (p *AutoScaledPool) canKillIdleWorkerLocked() bool {
	return p.sizeLocked() > p.minSize && p.idleSizeLocked()-p.defaultSlotsPerAgent >= p.minIdle && (len(p.idleWorkers) > 0 || len(p.pendingWorkers) > 0)
}

func (p *AutoScaledPool) deleteOneIdleWorker() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killingIdle = false
	if !p.canKillIdleWorkerLocked() {
		p.logPrintf("No idle workers to delete, stopping idle decay timer")
		return
	}
	if p.deleteOneWorkerLocked() {
		p.maintainIdleWorkers()
	}
}

func (p *AutoScaledPool) deleteOneWorkerLocked() bool {
	if len(p.idleWorkers) == 0 && len(p.pendingWorkers) == 0 {
		p.logPrintf("No idle workers to delete")
		return false
	}
	if p.sizeLocked() <= p.minSize {
		p.logPrintf("Not deleting worker: would violate min pool size (%d)", p.minSize)
		return false
	}
	var name string
	// Prefer deleting pending workers first, since they have not started.
	if len(p.pendingWorkers) > 0 {
		for k := range p.pendingWorkers {
			name = k
			break
		}
		p.logPrintf("Deleting pending worker %s", name)
		p.setWorkerStatusLocked(name, WorkerStatusDeleting, 0)
		if err := p.backend.RequestDelete(p.ctx, name); err != nil {
			p.logPrintf("Failed to delete pending worker %s: %v", name, err)
		}
	} else {
		for k := range p.idleWorkers {
			// Do not delete if deleting this worker would violate min idle or min total size.
			if p.idleSizeLocked()-p.workerDetail[k].availableSlot < p.minIdle {
				continue
			}
			name = k
			break
		}
		if name == "" {
			p.logPrintf("No idle workers can be deleted without violating min idle size")
			return false
		}
		// Send signal to worker to remove from scheduler, then request shutdown from backend.
		statusChan := p.workerDetail[name].statusChan
		statusChan <- AgentStatusRequest{
			shutdown: true,
			target:   name,
		}
		p.setWorkerStatusLocked(name, WorkerStatusNotifyDeleting, -1)
		p.logPrintf("Deleting idle worker %s, current idle size: %d", name, p.idleSizeLocked())
	}
	return true
}

type InspectWorkerFunc func(ctx context.Context) (*AgentStatusResponse, error)
type InspectFunc func(name string, status WorkerStatusCode, inspectWorker InspectWorkerFunc) bool

func (p *AutoScaledPool) InspectWorkers(f InspectFunc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for status, workers := range p.workersByStatus {
		for name := range workers {
			statusChan := p.workerDetail[name].statusChan
			inspectWorker := func(ctx context.Context) (*AgentStatusResponse, error) {
				if statusChan == nil {
					return nil, fmt.Errorf("worker %s is not available for inspection", name)
				}
				responseChan := make(chan *AgentStatusResponse, 1)
				statusChan <- AgentStatusRequest{
					response: responseChan,
					ctx:      ctx,
				}
				select {
				case response := <-responseChan:
					return response, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if !f(name, status, inspectWorker) {
				return
			}
		}
	}
}

func (p *AutoScaledPool) sizeLocked() int {
	return len(p.idleWorkers) + len(p.runningWorkers) + len(p.pendingWorkers) + len(p.workersByStatus[WorkerStatusUnknown]) + p.pendingUnknownWorkers
}

func (p *AutoScaledPool) idleSizeLocked() int {
	return p.idleSlots - p.pendingSessions
}

func (p *AutoScaledPool) shutdown() {
	p.killIdleTimer.Stop()
}

func (p *AutoScaledPool) setWorkerStatusLocked(name string, status WorkerStatusCode, newIdleSlots int) WorkerStatusCode {
	currentStatus, ok := p.workerStatus[name]
	if ok && currentStatus != status {
		delete(p.workersByStatus[currentStatus], name)
		if currentStatus == WorkerStatusUnknown || currentStatus == WorkerStatusPending || currentStatus == WorkerStatusLost {
			delete(p.lastKnownTime, name)
		}
		if status == WorkerStatusPending && currentStatus == WorkerStatusDeleting {
			status = WorkerStatusNeedsRestart
			p.logPrintf("Worker %s is being marked as pending while deleting, marking as NeedsRestart", name)
		}
	} else if !ok {
		p.workerDetail[name] = &workerDetail{}
	}
	old := p.workerDetail[name].availableSlot
	if newIdleSlots < 0 {
		newIdleSlots = old
	}
	p.workerDetail[name].availableSlot = newIdleSlots
	if currentStatus == WorkerStatusNotifyDeleting {
		old = 0
	}
	if status == WorkerStatusNotifyDeleting {
		newIdleSlots = 0
	}
	p.idleSlots += newIdleSlots - old

	p.workersByStatus[status][name] = p.currentSyncVersion.Load()
	p.workerStatus[name] = status
	return currentStatus
}

func (p *AutoScaledPool) removeWorkerLocked(name string) WorkerStatusCode {
	currentStatus, ok := p.workerStatus[name]
	if ok {
		delete(p.workersByStatus[currentStatus], name)
		p.idleSlots -= p.workerDetail[name].availableSlot
	} else {
		log.Printf("Pool %s: Removing worker %s that is not in pool", p.name, name)
	}
	delete(p.workerStatus, name)
	delete(p.workerDetail, name)
	delete(p.lastKnownTime, name)
	return currentStatus
}

func (p *AutoScaledPool) logPrintf(format string, args ...interface{}) {
	log.Printf("Pool %s:\t"+format, append([]interface{}{p.name}, args...)...)
}

func (p *AutoScaledPool) Backend() ResourcePoolBackend {
	return p.backend
}
