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
package btrfs

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/db/sqlite"
	"velda.io/velda/pkg/proto"
)

const xattrNameKey = "user.velda.name"

type BtrfsInstanceDb struct {
	*sqlite.SqliteDatabase
	fs             *Btrfs
	mu             sync.Mutex
	instances      map[string]int64
	nextInstanceId int64
}

func NewBtrfsInstanceDb(fs *Btrfs) *BtrfsInstanceDb {
	sqlite, err := sqlite.NewSqliteDatabase(fmt.Sprintf("%s/db.sqlite", fs.rootPath))
	if err != nil {
		panic(fmt.Sprintf("failed to create sqlite database: %s %v", fs.rootPath, err))
	}
	return &BtrfsInstanceDb{
		SqliteDatabase: sqlite,
		fs:             fs,
		instances:      make(map[string]int64),
		nextInstanceId: 1,
	}
}

func (d *BtrfsInstanceDb) getName(ctx context.Context, instanceId int64) (string, error) {
	instanceDir := fmt.Sprintf("%s/%d", d.fs.rootPath, instanceId)
	
	if d.fs.uid == 0 {
		// Direct xattr access when running as root
		buf := make([]byte, 256)
		sz, err := unix.Getxattr(instanceDir, xattrNameKey, buf)
		if err != nil {
			return "", fmt.Errorf("failed to get name xattr for instance %d: %w", instanceId, err)
		}
		return string(buf[:sz]), nil
	}
	
	// Use getfattr with sudo when not root
	output, err := d.fs.runCommandGetOutput(ctx, "getfattr", "-n", xattrNameKey, "--only-values", instanceDir)
	if err != nil {
		return "", fmt.Errorf("failed to get name xattr for instance %d: %w", instanceId, err)
	}
	return strings.TrimSpace(output), nil
}

func (d *BtrfsInstanceDb) setName(ctx context.Context, name string, instanceId int64) error {
	instanceDir := fmt.Sprintf("%s/%d", d.fs.rootPath, instanceId)
	
	if d.fs.uid == 0 {
		// Direct xattr access when running as root
		err := unix.Setxattr(instanceDir, xattrNameKey, []byte(name), 0)
		if err != nil {
			return fmt.Errorf("failed to set name xattr for instance %d: %w", instanceId, err)
		}
		return nil
	}
	
	// Use setfattr with sudo when not root
	err := d.fs.runCommand(ctx, "setfattr", "-n", xattrNameKey, "-v", name, instanceDir)
	if err != nil {
		return fmt.Errorf("failed to set name xattr for instance %d: %w", instanceId, err)
	}
	return nil
}

func (d *BtrfsInstanceDb) Init() error {
	ctx := context.Background()
	d.mu.Lock()
	defer d.mu.Unlock()

	// List all directories in root path to find instance directories
	output, err := d.fs.runCommandGetOutput(
		ctx,
		"find",
		d.fs.rootPath,
		"-maxdepth",
		"1",
		"-type",
		"d",
	)
	if err != nil {
		return fmt.Errorf("failed to list directories: %w", err)
	}
	
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == d.fs.rootPath {
			continue
		}
		
		// Extract the directory name from the full path
		parts := strings.Split(line, "/")
		if len(parts) == 0 {
			continue
		}
		dirName := parts[len(parts)-1]
		
		// Check if this is a numeric instance ID (not images, image_archive, etc.)
		id, err := strconv.ParseInt(dirName, 10, 64)
		if err != nil {
			continue
		}
		
		if id >= d.nextInstanceId {
			d.nextInstanceId = id + 1
		}
		
		// Try to get the name from xattr
		name, err := d.getName(ctx, id)
		if err != nil {
			// Skip instances without a name
			continue
		}
		
		d.instances[name] = id
	}
	return nil
}

type createCommitter struct {
	ctx       context.Context
	db        *BtrfsInstanceDb
	name      string
	id        int64
	committed bool
}

func (c *createCommitter) Commit() error {
	err := c.db.setName(c.ctx, c.name, c.id)
	if err != nil {
		return err
	}
	log.Printf("Created instance %d as %s", c.id, c.name)
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

func (d *BtrfsInstanceDb) CreateInstance(ctx context.Context, in *proto.Instance) (*proto.Instance, db.Committer, error) {
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

func (d *BtrfsInstanceDb) GetInstance(ctx context.Context, in *proto.GetInstanceRequest) (*proto.Instance, error) {
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

func (d *BtrfsInstanceDb) GetInstanceByName(ctx context.Context, in *proto.GetInstanceByNameRequest) (*proto.Instance, error) {
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

func (d *BtrfsInstanceDb) ListInstances(ctx context.Context, in *proto.ListInstancesRequest) (*proto.ListInstancesResponse, error) {
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
	db   *BtrfsInstanceDb
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
	// Do nothing
	return nil
}

func (d *BtrfsInstanceDb) DeleteInstance(ctx context.Context, in *proto.DeleteInstanceRequest) (db.Committer, *proto.Instance, error) {
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

func (d *BtrfsInstanceDb) RunMaintenances(ctx context.Context) {
}
