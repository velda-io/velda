// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package broker

import (
	"context"
	"log"
	"time"

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/rbac"
	"velda.io/velda/pkg/proto"
)

type TaskQueueDb interface {
	PollTasks(
		ctx context.Context,
		pool string,
		leaserIdentity string,
		callback func(leaserIdentity string, task *db.TaskWithUser) error) error
	RenewLeaser(ctx context.Context, leaserIdentity string, now time.Time) error
	ReconnectTask(ctx context.Context, taskId string, leaserIdentity string) error
}

type TaskTracker struct {
	scheduler *SchedulerSet
	sessions  *SessionDatabase

	db       TaskQueueDb
	identity string
}

func NewTaskTracker(schedulerSet *SchedulerSet, sessions *SessionDatabase, db TaskQueueDb, identity string) *TaskTracker {
	result := &TaskTracker{
		scheduler: schedulerSet,
		sessions:  sessions,
		db:        db,
		identity:  identity,
	}
	go result.renewLeaser(schedulerSet.ctx)
	return result
}

func (t *TaskTracker) PollTasks(ctx context.Context, pool string) error {
	scheduler, err := t.scheduler.GetPool(pool)
	if err != nil {
		return err
	}
	err = t.db.PollTasks(ctx, pool, t.identity,
		func(_ string, task *db.TaskWithUser) error {
			req := &proto.SessionRequest{
				Workload:   task.Workload,
				TaskId:     task.Id,
				Pool:       pool,
				InstanceId: task.InstanceId,
				Priority:   task.Priority,
			}
			// Always assume normal user for batch tasks.

			session, err := t.sessions.AddSession(req, scheduler)
			if err != nil {
				log.Printf("Failed to add session for task %v: %v", task.Id, err)
				return nil
			}
			schedCtx := rbac.ContextWithUser(ctx, task.User)
			go session.Schedule(schedCtx)
			return nil
		})
	return err
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

func (t *TaskTracker) ReconnectTask(ctx context.Context, taskId string) error {
	return t.db.ReconnectTask(ctx, taskId, t.identity)
}
