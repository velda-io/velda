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
	"path"
	"strings"
	"sync"
	"time"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

var (
	// How to buffer the cancel job list to avoid race
	cleanupAfterCancel = 10 * time.Minute

	// The minimal internal cleanup interval
	cleanupMinimumInterval = 1 * time.Minute

	// The maximum number of cancelled jobs to keep in the buffer
	cleanupMaxCancelledJobs = 128
)

type TaskQueueDb interface {
	PollTasks(
		ctx context.Context,
		leaserIdentity string,
		callback func(leaserIdentity string, task *db.TaskWithUser) error,
		regionId int) error
	CreateTask(ctx context.Context, session *proto.SessionRequest) (string, int, error)
	RenewLeaser(ctx context.Context, leaserIdentity string, now time.Time) error
	ReconnectTask(ctx context.Context, taskId string, leaserIdentity string) error
	UpdateTaskFinalResult(ctx context.Context, taskId string, result *db.BatchTaskResult) error
}

type TaskTracker struct {
	scheduler *SchedulerSet
	sessions  *SessionDatabase
	watcher   *Watcher
	regionId  int

	db       TaskQueueDb
	identity string
	gangMu   sync.Mutex
	gangs    map[string]*GangCoordinator

	sessionMu      sync.Mutex
	leasedSessions map[string]map[string]*Session // jobId -> taskId -> session
	cancelled      map[string]time.Time
	lastCleanup    time.Time
}

func NewTaskTracker(schedulerSet *SchedulerSet, sessions *SessionDatabase, db TaskQueueDb, identity string, watcher *Watcher, regionId int) *TaskTracker {
	result := &TaskTracker{
		scheduler:      schedulerSet,
		sessions:       sessions,
		db:             db,
		identity:       identity,
		watcher:        watcher,
		regionId:       regionId,
		gangs:          make(map[string]*GangCoordinator),
		leasedSessions: make(map[string]map[string]*Session),
		cancelled:      make(map[string]time.Time),
	}
	go result.renewLeaser(schedulerSet.ctx)
	return result
}

func (t *TaskTracker) getOrCreateGang(id string, desired int) *GangCoordinator {
	t.gangMu.Lock()
	defer t.gangMu.Unlock()
	gang, ok := t.gangs[id]
	if !ok {
		// Add an extra member to cleanup the coordinator once scheduled.
		gang = NewGangCoordinator(desired + 1)
		t.gangs[id] = gang
		gang.Notify(-1, func() {
			t.gangMu.Lock()
			defer t.gangMu.Unlock()
			delete(t.gangs, id)
		})
	}
	return gang
}
func (t *TaskTracker) PollTasks(ctx context.Context) error {
	err := t.db.PollTasks(ctx, t.identity,
		func(_ string, task *db.TaskWithUser) error {
			pool := task.Task.Pool
			req := &proto.SessionRequest{
				Workload:   task.Workload,
				TaskId:     task.Id,
				Pool:       pool,
				InstanceId: task.InstanceId,
				Priority:   task.Priority,
			}
			if task.Task.Workload.TotalShards > 0 && task.Task.Workload.ShardIndex < 0 {
				startTime := time.Now()
				if task.Task.Workload.ShardScheduling == proto.Workload_SHARD_SCHEDULING_GANG {
					// If supported, use batch-scheduling.
					// This may modify the req object
					if err := t.tryBatchScheduling(ctx, req); err != nil && !errors.Is(err, ErrNotSupportingBatchScheduler) {
						log.Printf("Cannot use batch-mode scheduling for task %s: %v", task.Id, err)
					}
				}
				for i := 0; i < int(task.Task.Workload.TotalShards); i++ {
					taskShard := pb.Clone(req).(*proto.SessionRequest)
					taskShard.Workload.ShardIndex = int32(i)
					taskShard.Workload.Environs = append(taskShard.Workload.Environs,
						fmt.Sprintf("VELDA_SHARD_ID=%d", i),
						fmt.Sprintf("VELDA_TOTAL_SHARDS=%d", task.Task.Workload.TotalShards))
					taskShard.TaskId = fmt.Sprintf("%s/%d", taskShard.TaskId, i)
					if _, _, err := t.db.CreateTask(ctx, taskShard); err != nil {
						return err
					}
				}
				// The task should be marked as running_subtasks.
				code := int32(0)
				if err := t.db.UpdateTaskFinalResult(ctx, task.Id, &db.BatchTaskResult{StartTime: startTime, Payload: &proto.BatchTaskResult{ExitCode: &code}}); err != nil {
					log.Printf("Failed to update task %s to running_subtasks: %v", task.Id, err)
				}
				return nil
			}
			// Always assume normal user for batch tasks.

			scheduler, err := t.scheduler.GetPool(pool)
			if err != nil {
				log.Printf("Failed to get pool %s for task %s: %v", pool, task.Id, err)
				return nil
			}
			session, err := t.sessions.AddSession(req, scheduler)
			if err != nil {
				log.Printf("Failed to add session for task %v: %v", task.Id, err)
				return nil
			}
			schedCtx := rbac.ContextWithUser(ctx, task.User)
			if task.Task.Workload.TotalShards > 0 && task.Task.Workload.ShardScheduling == proto.Workload_SHARD_SCHEDULING_GANG {
				parentTaskId := path.Dir(task.Id)
				gang := t.getOrCreateGang(parentTaskId, int(task.Task.Workload.TotalShards))
				session.SetGang(gang)
			}
			if !t.registerSession(session) {
				log.Printf("Task %s is already cancelled. Immediately cancelling.", task.Id)
				cancelCtx, cancel := context.WithCancel(schedCtx)
				cancel()
				go session.Schedule(cancelCtx)
			} else {
				go session.Schedule(schedCtx)
			}
			return nil
		}, t.regionId)
	return err
}

func (t *TaskTracker) tryBatchScheduling(ctx context.Context, req *proto.SessionRequest) error {
	scheduler, err := t.scheduler.GetPool(req.Pool)
	if err != nil {
		log.Printf("Failed to get pool %s for task %s: %v", req.Pool, req.TaskId, err)
		return nil
	}
	if err := scheduler.PoolManager.RequestBatch(ctx, int(req.Workload.TotalShards), req.TaskId); err != nil {
		return fmt.Errorf("failed to request batch: %w", err)
	}
	req.Pool = fmt.Sprintf("%s:%s", req.Pool, req.TaskId)
	newScheduler, err := t.scheduler.GetOrCreatePool(req.Pool)
	if err != nil {
		log.Printf("Failed to create or get pool %s: %v", req.Pool, err)
		return nil
	}
	log.Printf("New pool created: %s", req.Pool)
	newScheduler.PoolManager = scheduler.PoolManager
	return nil
}

func (t *TaskTracker) registerSession(session *Session) bool {
	t.sessionMu.Lock()
	defer t.sessionMu.Unlock()
	taskId := session.Request.TaskId
	segments := strings.SplitN(taskId, "/", 2)
	jobId := segments[0]
	if _, ok := t.cancelled[jobId]; ok {
		// Job is already cancelled.
		return false
	}
	sessionInJob := t.leasedSessions[jobId]
	if sessionInJob == nil {
		sessionInJob = make(map[string]*Session)
		t.leasedSessions[jobId] = sessionInJob
	}
	sessionInJob[taskId] = session
	session.AddCompletion(func() {
		t.sessionMu.Lock()
		defer t.sessionMu.Unlock()
		delete(sessionInJob, taskId)
		if len(sessionInJob) == 0 {
			delete(t.leasedSessions, jobId)
		}
		if _, ok := t.cancelled[jobId]; ok {
			if err := t.db.UpdateTaskFinalResult(context.Background(), taskId, &db.BatchTaskResult{Cancelled: true}); err != nil {
				log.Printf("Failed to mark cancelled task %s as cancelled: %v", taskId, err)
			}
		}
	})
	return true
}

func (t *TaskTracker) renewLeaser(ctx context.Context) error {
	ticker := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			func() {
				timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				if err := t.db.RenewLeaser(timeoutCtx, t.identity, time.Now()); err != nil {
					log.Printf("Failed to renew leaser: %v %v", t.identity, err)
				}
			}()
		}
	}
}

func (t *TaskTracker) ReconnectTask(ctx context.Context, session *Session) error {
	taskId := session.Request.TaskId
	err := t.db.ReconnectTask(ctx, taskId, t.identity)
	if err != nil {
		return err
	}
	if !t.registerSession(session) {
		log.Printf("Task %s is already cancelled, killing", taskId)
		session.Kill(ctx, false)
	}
	return nil
}

func (t *TaskTracker) GetTaskStatus(ctx context.Context, taskId string) (*proto.ExecutionStatus, error) {
	segments := strings.SplitN(taskId, "/", 2)
	jobId := segments[0]
	t.sessionMu.Lock()
	sessions := t.leasedSessions[jobId]
	t.sessionMu.Unlock()
	if sessions == nil {
		return nil, fmt.Errorf("task %s not found in tracker", taskId)
	}
	session, ok := sessions[taskId]
	if !ok {
		return nil, fmt.Errorf("task %s not found in tracker", taskId)
	}
	return session.Status(), nil
}

func (t *TaskTracker) WatchTask(ctx context.Context, taskId string, callback func(*proto.ExecutionStatus) bool) {
	t.watcher.SubscribeTask(ctx, taskId, callback, func() *proto.ExecutionStatus {
		s, err := t.GetTaskStatus(ctx, taskId)
		if err == nil {
			return s
		}
		return nil
	})
}

func (t *TaskTracker) CancelJob(ctx context.Context, jobId string) error {
	var sessions map[string]*Session
	func() {
		t.sessionMu.Lock()
		defer t.sessionMu.Unlock()
		t.cancelled[jobId] = time.Now()
		sessions = t.leasedSessions[jobId]

		if len(t.cancelled) > cleanupMaxCancelledJobs || time.Since(t.lastCleanup) > cleanupMinimumInterval {
			newCancelled := make(map[string]time.Time)
			for k, v := range t.cancelled {
				if time.Since(v) < cleanupAfterCancel {
					newCancelled[k] = v
				}
			}
			t.cancelled = newCancelled
			t.lastCleanup = time.Now()
		}
	}()
	log.Printf("Cancelling job %s with %d running sessions", jobId, len(sessions))
	if len(sessions) == 0 {
		return nil
	}
	for _, session := range sessions {
		err := session.Kill(ctx, false)
		if err != nil {
			// TODO: Handle this?
			log.Printf("Failed to kill session %s: %v", session.Request.SessionId, err)
		} else {
			log.Printf("Killed canceled task %s", session.Request.TaskId)
		}
	}
	return nil
}
