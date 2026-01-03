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
package mini

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/db/sqlite"
	"velda.io/velda/pkg/proto"
)

type MiniInstanceDb struct {
	*sqlite.SqliteDatabase
	storage        *MiniStorage
	nextInstanceId int64
	mu             sync.Mutex
}

func NewMiniInstanceDb(fs *MiniStorage) *MiniInstanceDb {
	sqlite, err := sqlite.NewSqliteDatabase(fmt.Sprintf("/%s/db.sqlite", fs.sandboxPath))
	if err != nil {
		panic(fmt.Sprintf("failed to create sqlite database: %s %v", fs.sandboxPath, err))
	}
	db := &MiniInstanceDb{
		SqliteDatabase: sqlite,
		storage:        fs,
		nextInstanceId: 1,
	}

	// Initialize nextInstanceId by scanning existing instances
	if err := db.initializeNextInstanceId(); err != nil {
		panic(fmt.Sprintf("failed to initialize instance ID counter: %v", err))
	}

	return db
}

func (d *MiniInstanceDb) initializeNextInstanceId() error {
	instancesDir := filepath.Join(d.storage.sandboxPath, "instances")

	// Read all instance directories
	entries, err := os.ReadDir(instancesDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No instances directory yet, start from 1
			d.nextInstanceId = 1
			return nil
		}
		return fmt.Errorf("failed to read instances directory: %w", err)
	}

	// Find the maximum instance ID
	maxId := int64(0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		instanceId, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil {
			continue
		}

		if instanceId > maxId {
			maxId = instanceId
		}
	}

	// Set next ID to max + 1
	d.nextInstanceId = maxId + 1
	return nil
}

func (d *MiniInstanceDb) Init() error {
	return nil
}

// Helper functions to read/write instance name
func (d *MiniInstanceDb) getInstanceNamePath(instanceId int64) string {
	return filepath.Join(d.storage.getInstancePath(instanceId), "name")
}

func (d *MiniInstanceDb) writeInstanceName(instanceId int64, name string) error {
	namePath := d.getInstanceNamePath(instanceId)
	return os.WriteFile(namePath, []byte(name), 0644)
}

func (d *MiniInstanceDb) readInstanceName(instanceId int64) (string, error) {
	namePath := d.getInstanceNamePath(instanceId)
	data, err := os.ReadFile(namePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type dummyCommitter struct{}

func (c *dummyCommitter) Commit() error {
	return nil
}

func (c *dummyCommitter) Rollback() error {
	return nil
}

func (d *MiniInstanceDb) CreateInstance(ctx context.Context, in *proto.Instance) (*proto.Instance, db.Committer, error) {
	d.mu.Lock()
	// Auto-assign instance ID
	instanceId := d.nextInstanceId
	d.nextInstanceId++
	d.mu.Unlock()

	// Create the instance in storage
	if err := d.storage.CreateInstance(ctx, instanceId); err != nil {
		return nil, nil, fmt.Errorf("failed to create instance storage: %w", err)
	}

	// Write the instance name
	if err := d.writeInstanceName(instanceId, in.InstanceName); err != nil {
		return nil, nil, fmt.Errorf("failed to write instance name: %w", err)
	}

	return in, &dummyCommitter{}, nil
}

func (d *MiniInstanceDb) GetInstance(ctx context.Context, in *proto.GetInstanceRequest) (*proto.Instance, error) {
	instancePath := d.storage.getInstancePath(in.InstanceId)

	// Check if instance exists
	if _, err := os.Stat(instancePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("instance %d not found", in.InstanceId)
	}

	// Read instance name
	name, err := d.readInstanceName(in.InstanceId)
	if err != nil {
		return nil, fmt.Errorf("failed to read instance name: %w", err)
	}

	return &proto.Instance{
		Id:           in.InstanceId,
		InstanceName: name,
	}, nil
}

func (d *MiniInstanceDb) GetInstanceByName(ctx context.Context, in *proto.GetInstanceByNameRequest) (*proto.Instance, error) {
	instancesDir := filepath.Join(d.storage.sandboxPath, "instances")

	// Read all instance directories
	entries, err := os.ReadDir(instancesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("instance %s not found", in.InstanceName)
		}
		return nil, fmt.Errorf("failed to read instances directory: %w", err)
	}

	// Search for instance by name
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		instanceId, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil {
			continue
		}

		name, err := d.readInstanceName(instanceId)
		if err != nil {
			continue
		}

		if name == in.InstanceName {
			return &proto.Instance{
				Id:           instanceId,
				InstanceName: name,
			}, nil
		}
	}

	return nil, fmt.Errorf("instance %s not found", in.InstanceName)
}

func (d *MiniInstanceDb) ListInstances(ctx context.Context, in *proto.ListInstancesRequest) (*proto.ListInstancesResponse, error) {
	instancesDir := filepath.Join(d.storage.sandboxPath, "instances")

	// Read all instance directories
	entries, err := os.ReadDir(instancesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &proto.ListInstancesResponse{
				Instances: []*proto.Instance{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read instances directory: %w", err)
	}

	var instances []*proto.Instance
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		instanceId, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil {
			continue
		}

		name, err := d.readInstanceName(instanceId)
		if err != nil {
			continue
		}

		instances = append(instances, &proto.Instance{
			Id:           instanceId,
			InstanceName: name,
		})
	}

	return &proto.ListInstancesResponse{
		Instances: instances,
	}, nil
}

func (d *MiniInstanceDb) DeleteInstance(ctx context.Context, in *proto.DeleteInstanceRequest) (db.Committer, *proto.Instance, error) {
	// Get the instance before deleting
	instance, err := d.GetInstance(ctx, &proto.GetInstanceRequest{InstanceId: in.InstanceId})
	if err != nil {
		return nil, nil, err
	}

	// Delete the instance storage
	if err := d.storage.DeleteInstance(ctx, in.InstanceId); err != nil {
		return nil, nil, fmt.Errorf("failed to delete instance storage: %w", err)
	}

	return &dummyCommitter{}, instance, nil
}

func (d *MiniInstanceDb) RunMaintenances(ctx context.Context) {
}
