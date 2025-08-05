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
package storage

import (
	"context"
)

type Storage interface {
	CreateInstance(ctx context.Context, instanceId int64) error

	CreateInstanceFromSnapshot(ctx context.Context, instanceId int64, snapshotInstanceId int64, snapshotName string) error

	CreateInstanceFromImage(ctx context.Context, instanceId int64, imageName string) error

	CreateSnapshot(ctx context.Context, instanceId int64, snapshotName string) error

	DeleteSnapshot(ctx context.Context, instanceId int64, snapshot_name string) error

	DeleteInstance(ctx context.Context, instanceId int64) error

	CreateImageFromSnapshot(ctx context.Context, imageName string, snapshotInstanceId int64, snapshotName string) error

	ListImages(ctx context.Context) ([]string, error)

	DeleteImage(ctx context.Context, imageName string) error
}
