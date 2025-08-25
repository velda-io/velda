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

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/db/sqlite"
	"velda.io/velda/pkg/proto"
)

const OnlyInstanceId = 1
const InstanceName = "mini-velda"

var theInstance = &proto.Instance{
	Id:           OnlyInstanceId,
	InstanceName: InstanceName,
}

type MiniInstanceDb struct {
	*sqlite.SqliteDatabase
}

func NewMiniInstanceDb(fs *MiniStorage) *MiniInstanceDb {
	sqlite, err := sqlite.NewSqliteDatabase(fmt.Sprintf("/%s/db.sqlite", fs.sandboxPath))
	if err != nil {
		panic(fmt.Sprintf("failed to create sqlite database: %s %v", fs.sandboxPath, err))
	}
	return &MiniInstanceDb{
		SqliteDatabase: sqlite,
	}
}

func (d *MiniInstanceDb) Init() error {
	return nil
}

func (d *MiniInstanceDb) CreateInstance(ctx context.Context, in *proto.Instance) (*proto.Instance, db.Committer, error) {
	return nil, nil, NotSupportedError
}

func (d *MiniInstanceDb) GetInstance(ctx context.Context, in *proto.GetInstanceRequest) (*proto.Instance, error) {
	if in.InstanceId != OnlyInstanceId {
		return nil, fmt.Errorf("instance %d not found", in.InstanceId)
	}
	return theInstance, nil
}

func (d *MiniInstanceDb) GetInstanceByName(ctx context.Context, in *proto.GetInstanceByNameRequest) (*proto.Instance, error) {
	if in.InstanceName == InstanceName {
		return theInstance, nil
	}
	return nil, fmt.Errorf("instance %s not found", in.InstanceName)
}

func (d *MiniInstanceDb) ListInstances(ctx context.Context, in *proto.ListInstancesRequest) (*proto.ListInstancesResponse, error) {

	return &proto.ListInstancesResponse{
		Instances: []*proto.Instance{theInstance},
	}, nil
}

func (d *MiniInstanceDb) DeleteInstance(ctx context.Context, in *proto.DeleteInstanceRequest) (db.Committer, *proto.Instance, error) {
	return nil, nil, NotSupportedError
}

func (d *MiniInstanceDb) RunMaintenances(ctx context.Context) {
}
