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
	"path/filepath"

	"velda.io/velda/pkg/storage"
)

type MiniStorage struct {
	sandboxPath string
}

var NotSupportedError = fmt.Errorf("mini storage does not support this operation")

func NewMiniStorage(path string) (*MiniStorage, error) {
	z := &MiniStorage{
		sandboxPath: path,
	}
	return z, nil
}

func (z *MiniStorage) CreateInstance(ctx context.Context, instanceId int64) error {
	return NotSupportedError
}

func (z *MiniStorage) CreateInstanceFromSnapshot(ctx context.Context, instanceId int64, snapshotInstanceId int64, snapshotName string) error {
	return NotSupportedError
}

func (z *MiniStorage) CreateInstanceFromImage(ctx context.Context, instanceId int64, imageName string) error {
	return NotSupportedError
}

func (z *MiniStorage) DeleteInstance(ctx context.Context, instanceId int64) error {
	return NotSupportedError
}

func (z *MiniStorage) CreateSnapshot(ctx context.Context, instanceId int64, snapshotName string) error {
	return NotSupportedError
}

func (z *MiniStorage) DeleteSnapshot(ctx context.Context, instanceId int64, snapshot_name string) error {
	return NotSupportedError
}

func (z *MiniStorage) CreateImageFromSnapshot(ctx context.Context, imageName string, snapshotInstanceId int64, snapshotName string) error {
	return NotSupportedError
}

func (z *MiniStorage) DeleteImage(ctx context.Context, imageName string) error {
	return NotSupportedError
}

func (z *MiniStorage) ListImages(ctx context.Context) ([]string, error) {
	return nil, NotSupportedError
}

func (z *MiniStorage) ReadFile(ctx context.Context, instanceId int64, path string, options *storage.ReadFileOptions) (storage.ByteStream, error) {
	return storage.FileToByteStream(ctx, filepath.Join(z.GetRoot(instanceId), path), options)
}

func (z *MiniStorage) GetRoot(instanceId int64) string {
	return filepath.Join(z.sandboxPath, "root/0/1")
}
