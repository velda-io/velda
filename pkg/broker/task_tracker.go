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
	"path"
	"sync"
	"time"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
)

type TaskQueueDb interface {
	PollTasks(
		ctx context.Context,
		leaserIdentity string,
		callback func(leaserIdentity string, task *db.TaskWithUser) error) error
	CreateTask(ctx context.Context, session *proto.SessionRequest) (string, int, error)
	RenewLeaser(ctx context.Context, leaserIdentity string, now time.Time) error
	ReconnectTask(ctx context.Context, taskId string, leaserIdentity string) error
}

type TaskTracker struct {
	scheduler *SchedulerSet
	sessions  *SessionDatabase

	db       TaskQueueDb
	identity string
	gangMu   sync.Mutex
	gangs    map[string]*GangCoordinator
}

func NewTaskTracker(schedulerSet *SchedulerSet, sessions *SessionDatabase, db TaskQueueDb, identity string) *TaskTracker {
	result := &TaskTracker{
		scheduler: schedulerSet,
		sessions:  sessions,
		db:        db,
		identity:  identity,
		gangs:     make(map[string]*GangCoordinator),
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
				// TODO: Handle gang scheduling.
				return nil
			}
			// Always assume normal user for batch tasks.

			scheduler, err := t.scheduler.GetPool(pool)
			if err != nil {
				return err
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
