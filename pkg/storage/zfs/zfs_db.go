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
package zfs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
)

type ZfsInstanceDb struct {
	fs             *Zfs
	mu             sync.Mutex
	instances      map[string]int64
	nextInstanceId int64
}

func NewZfsInstanceDb(fs *Zfs) *ZfsInstanceDb {
	return &ZfsInstanceDb{
		fs:             fs,
		instances:      make(map[string]int64),
		nextInstanceId: 1,
	}
}

func (d *ZfsInstanceDb) getName(ctx context.Context, instanceId int64) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	name, err := d.fs.runCommandGetOutput(
		ctx,
		"zfs",
		"get",
		"-H",
		"-o", "value",
		":name",
		fmt.Sprintf("%s/%d", d.fs.pool, instanceId),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get name for instance %d: %w", instanceId, err)
	}
	return name, nil
}

func (d *ZfsInstanceDb) setName(ctx context.Context, name string, instanceId int64) error {
	return d.fs.runCommand(
		ctx,
		"zfs",
		"set",
		":name="+name,
		fmt.Sprintf("%s/%d", d.fs.pool, instanceId),
	)
}

func (d *ZfsInstanceDb) Init() error {
	ctx := context.Background()
	d.mu.Lock()
	defer d.mu.Unlock()

	output, err := d.fs.runCommandGetOutput(
		ctx,
		"zfs",
		"list",
		"-H",
		"-d1",
		"-o", "name,:name",
		fmt.Sprintf("%s", d.fs.pool),
	)
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		if !strings.HasPrefix(parts[0], d.fs.pool+"/") {
			continue
		}
		idString := strings.TrimPrefix(parts[0], d.fs.pool+"/")
		id, err := strconv.ParseInt(idString, 10, 64)
		if err != nil {
			continue
		}
		if id >= d.nextInstanceId {
			d.nextInstanceId = id + 1
		}
		name := parts[1]
		if name == "-" {
			continue
		}
		d.instances[name] = id
	}
	return nil
}

type createCommitter struct {
	ctx       context.Context
	db        *ZfsInstanceDb
	name      string
	id        int64
	committed bool
}

func (c *createCommitter) Commit() error {
	err := c.db.setName(c.ctx, c.name, c.id)
	if err != nil {
		return err
	}
	c.committed = true
	return nil
}

func (c *createCommitter) Rollback() error {
	if c.committed {
		return nil
	}
	c.db.mu.Lock()
	defer c.db.mu.Unlock()

	// Remove the instance from the map if it was created.
	delete(c.db.instances, c.name)
	return nil
}

func (d *ZfsInstanceDb) CreateInstance(ctx context.Context, in *proto.Instance) (*proto.Instance, db.Committer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.instances[in.InstanceName]; exists {
		return nil, nil, fmt.Errorf("instance %s already exists", in.InstanceName)
	}
	out := &proto.Instance{
		Id:           d.nextInstanceId,
		InstanceName: in.InstanceName,
	}
	d.instances[in.InstanceName] = out.Id
	d.nextInstanceId++
	return out, &createCommitter{ctx: ctx, db: d, name: in.InstanceName, id: out.Id}, nil
}

func (d *ZfsInstanceDb) GetInstance(ctx context.Context, in *proto.GetInstanceRequest) (*proto.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	name, err := d.getName(ctx, in.InstanceId)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance name: %w", err)
	}
	if id, exists := d.instances[name]; exists {
		return &proto.Instance{
			Id:           id,
			InstanceName: name,
		}, nil
	}
	return nil, fmt.Errorf("instance %d not found", in.InstanceId)
}

func (d *ZfsInstanceDb) GetInstanceByName(ctx context.Context, in *proto.GetInstanceByNameRequest) (*proto.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if id, exists := d.instances[in.InstanceName]; exists {
		return &proto.Instance{
			Id:           id,
			InstanceName: in.InstanceName,
		}, nil
	}
	return nil, fmt.Errorf("instance %s not found", in.InstanceName)
}

func (d *ZfsInstanceDb) ListInstances(ctx context.Context, in *proto.ListInstancesRequest) (*proto.ListInstancesResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var instances []*proto.Instance
	for name, id := range d.instances {
		instances = append(instances, &proto.Instance{
			Id:           id,
			InstanceName: name,
		})
	}
	return &proto.ListInstancesResponse{
		Instances: instances,
	}, nil
}

type deleteCommitter struct {
	db   *ZfsInstanceDb
	name string
}

func (c *deleteCommitter) Commit() error {
	c.db.mu.Lock()
	defer c.db.mu.Unlock()

	// Remove the instance from the map.
	delete(c.db.instances, c.name)
	return nil
}

func (c *deleteCommitter) Rollback() error {
	// No nothing
	return nil
}

func (d *ZfsInstanceDb) DeleteInstance(ctx context.Context, in *proto.DeleteInstanceRequest) (db.Committer, *proto.Instance, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	name, err := d.getName(ctx, in.InstanceId)
	if err != nil {
		return nil, nil, db.ErrNotFound
	}
	return &deleteCommitter{db: d, name: name}, &proto.Instance{
		Id:           in.InstanceId,
		InstanceName: name,
	}, nil
}

func (d *ZfsInstanceDb) RunMaintenances(ctx context.Context) {
}

// TODO: Cleanup the remaining tasks based methods.
func (d *ZfsInstanceDb) CreateTask(ctx context.Context, session *proto.SessionRequest) (string, int, error) {
	return "", 0, nil
}

func (d *ZfsInstanceDb) UpdateTaskFinalResult(ctx context.Context, taskId string, result *db.BatchTaskResult) error {
	return nil
}

func (d *ZfsInstanceDb) GetTask(ctx context.Context, taskId string) (*proto.Task, error) {
	return nil, fmt.Errorf("not implemented")
}
func (d *ZfsInstanceDb) ListTasks(ctx context.Context, request *proto.ListTasksRequest) ([]*proto.Task, string, error) {
	return nil, "", fmt.Errorf("not implemented")
}
func (d *ZfsInstanceDb) SearchTasks(ctx context.Context, request *proto.SearchTasksRequest) ([]*proto.Task, string, error) {
	return nil, "", fmt.Errorf("not implemented")
}
func (d *ZfsInstanceDb) PollTasks(
	ctx context.Context,
	pool string,
	leaserIdentity string,
	callback func(leaserIdentity string, task *db.TaskWithUser) error) error {
	return nil
}
func (d *ZfsInstanceDb) RenewLeaser(ctx context.Context, leaserIdentity string, now time.Time) error {
	return nil
}
func (d *ZfsInstanceDb) ReconnectTask(ctx context.Context, taskId string, leaserIdentity string) error {
	return nil
}
