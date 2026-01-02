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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"velda.io/velda/pkg/storage"
)

type MiniStorage struct {
	sandboxPath     string
	snapshotEnabled bool
}

var NotSupportedError = fmt.Errorf("mini storage does not support this operation")

func NewMiniStorage(path string) (*MiniStorage, error) {
	return NewMiniStorageWithOptions(path, true)
}

func NewMiniStorageWithOptions(path string, snapshotEnabled bool) (*MiniStorage, error) {
	z := &MiniStorage{
		sandboxPath:     path,
		snapshotEnabled: snapshotEnabled,
	}

	// Create base directories if they don't exist
	if err := os.MkdirAll(filepath.Join(path, "instances"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create instances directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(path, "images"), 0755); err != nil {
		return nil, fmt.Errorf("failed to create images directory: %w", err)
	}

	return z, nil
}

// Helper functions
func (z *MiniStorage) getInstancePath(instanceId int64) string {
	return filepath.Join(z.sandboxPath, "instances", strconv.FormatInt(instanceId, 10))
}

func (z *MiniStorage) getInstanceRootPath(instanceId int64) string {
	return filepath.Join(z.getInstancePath(instanceId), "root")
}

func (z *MiniStorage) getSnapshotPath(instanceId int64, snapshotName string) string {
	if !z.snapshotEnabled {
		// If snapshots are not enabled, always reference the current root
		return z.getInstanceRootPath(instanceId)
	}
	return filepath.Join(z.getInstancePath(instanceId), "snapshots", snapshotName)
}

func (z *MiniStorage) getImagePath(imageName string) string {
	return filepath.Join(z.sandboxPath, "images", imageName)
}

// copyDir recursively copies a directory tree
func copyDir(src string, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			err := os.MkdirAll(dstPath, info.Mode())
			if err != nil {
				return err
			}
			if sysstat, ok := info.Sys().(*syscall.Stat_t); ok {
				err = os.Chown(dstPath, int(sysstat.Uid), int(sysstat.Gid))
				if err != nil {
					return err
				}
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// Handle symlink
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, dstPath)
		}

		// Copy file
		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		if err != nil {
			return err
		}
		if sysstat, ok := info.Sys().(*syscall.Stat_t); ok {
			err = os.Chown(dstPath, int(sysstat.Uid), int(sysstat.Gid))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (z *MiniStorage) CreateInstance(ctx context.Context, instanceId int64) error {
	instancePath := z.getInstancePath(instanceId)
	rootPath := z.getInstanceRootPath(instanceId)

	// Create instance directory structure
	if err := os.MkdirAll(rootPath, 0755); err != nil {
		return fmt.Errorf("failed to create instance root: %w", err)
	}

	// Make a minimal filesystem structure
	for _, dir := range []string{"proc", "sys", "dev", "run", "etc"} {
		if err := os.MkdirAll(filepath.Join(rootPath, dir), 0755); err != nil {
			return fmt.Errorf("failed to create directory %s in instance %d: %w", dir, instanceId, err)
		}
	}
	if z.snapshotEnabled {
		snapshotsPath := filepath.Join(instancePath, "snapshots")
		if err := os.MkdirAll(snapshotsPath, 0755); err != nil {
			return fmt.Errorf("failed to create snapshots directory: %w", err)
		}
	}

	return nil
}

func (z *MiniStorage) CreateInstanceFromSnapshot(ctx context.Context, instanceId int64, snapshotInstanceId int64, snapshotName string) error {
	snapshotPath := z.getSnapshotPath(snapshotInstanceId, snapshotName)

	// Check if snapshot exists (only if snapshots are enabled)
	if z.snapshotEnabled {
		if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
			return fmt.Errorf("snapshot %s does not exist for instance %d", snapshotName, snapshotInstanceId)
		}
	}

	// Create the new instance directory
	instancePath := z.getInstancePath(instanceId)
	rootPath := z.getInstanceRootPath(instanceId)

	if err := os.MkdirAll(instancePath, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	if z.snapshotEnabled {
		snapshotsPath := filepath.Join(instancePath, "snapshots")
		if err := os.MkdirAll(snapshotsPath, 0755); err != nil {
			return fmt.Errorf("failed to create snapshots directory: %w", err)
		}
	}

	// Copy the snapshot to the new instance root
	if err := copyDir(snapshotPath, rootPath); err != nil {
		return fmt.Errorf("failed to copy snapshot: %w", err)
	}

	return nil
}

func (z *MiniStorage) CreateInstanceFromImage(ctx context.Context, instanceId int64, imageName string) error {
	imagePath := z.getImagePath(imageName)

	// Check if image exists
	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		return fmt.Errorf("image %s does not exist", imageName)
	}

	// Create the new instance directory
	instancePath := z.getInstancePath(instanceId)
	rootPath := z.getInstanceRootPath(instanceId)

	if err := os.MkdirAll(instancePath, 0755); err != nil {
		return fmt.Errorf("failed to create instance directory: %w", err)
	}

	if z.snapshotEnabled {
		snapshotsPath := filepath.Join(instancePath, "snapshots")
		if err := os.MkdirAll(snapshotsPath, 0755); err != nil {
			return fmt.Errorf("failed to create snapshots directory: %w", err)
		}
	}

	// Copy the image to the new instance root
	if err := copyDir(imagePath, rootPath); err != nil {
		return fmt.Errorf("failed to copy image: %w", err)
	}

	return nil
}

func (z *MiniStorage) DeleteInstance(ctx context.Context, instanceId int64) error {
	instancePath := z.getInstancePath(instanceId)

	// Remove the entire instance directory
	if err := os.RemoveAll(instancePath); err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	return nil
}

func (z *MiniStorage) CreateSnapshot(ctx context.Context, instanceId int64, snapshotName string) error {
	if !z.snapshotEnabled {
		// If snapshots are disabled, this is a no-op since snapshots always refer to the latest version
		return nil
	}

	rootPath := z.getInstanceRootPath(instanceId)
	snapshotPath := z.getSnapshotPath(instanceId, snapshotName)

	// Check if root exists
	if _, err := os.Stat(rootPath); os.IsNotExist(err) {
		return fmt.Errorf("instance %d does not exist", instanceId)
	}

	// Check if snapshot already exists
	if _, err := os.Stat(snapshotPath); err == nil {
		return fmt.Errorf("snapshot %s already exists for instance %d", snapshotName, instanceId)
	}

	// Create snapshot directory
	if err := os.MkdirAll(filepath.Dir(snapshotPath), 0755); err != nil {
		return fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Copy the root to the snapshot location
	if err := copyDir(rootPath, snapshotPath); err != nil {
		return fmt.Errorf("failed to copy root to snapshot: %w", err)
	}

	return nil
}

func (z *MiniStorage) DeleteSnapshot(ctx context.Context, instanceId int64, snapshot_name string) error {
	if !z.snapshotEnabled {
		// If snapshots are disabled, this is a no-op
		return nil
	}

	snapshotPath := z.getSnapshotPath(instanceId, snapshot_name)

	// Remove the snapshot directory
	if err := os.RemoveAll(snapshotPath); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	return nil
}

func (z *MiniStorage) CreateImageFromSnapshot(ctx context.Context, imageName string, snapshotInstanceId int64, snapshotName string) error {
	snapshotPath := z.getSnapshotPath(snapshotInstanceId, snapshotName)
	imagePath := z.getImagePath(imageName)

	// Check if snapshot exists (only if snapshots are enabled)
	if z.snapshotEnabled {
		if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
			return fmt.Errorf("snapshot %s does not exist for instance %d", snapshotName, snapshotInstanceId)
		}
	}

	// Check if image already exists
	if _, err := os.Stat(imagePath); err == nil {
		return fmt.Errorf("image %s already exists", imageName)
	}

	// Create image directory
	if err := os.MkdirAll(filepath.Dir(imagePath), 0755); err != nil {
		return fmt.Errorf("failed to create image directory: %w", err)
	}

	// Copy the snapshot to the image location
	if err := copyDir(snapshotPath, imagePath); err != nil {
		return fmt.Errorf("failed to copy snapshot to image: %w", err)
	}

	return nil
}

func (z *MiniStorage) DeleteImage(ctx context.Context, imageName string) error {
	imagePath := z.getImagePath(imageName)

	// Remove the image directory
	if err := os.RemoveAll(imagePath); err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	return nil
}

func (z *MiniStorage) ListImages(ctx context.Context) ([]string, error) {
	imagesDir := filepath.Join(z.sandboxPath, "images")

	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read images directory: %w", err)
	}

	var images []string
	for _, entry := range entries {
		if entry.IsDir() {
			images = append(images, entry.Name())
		}
	}

	return images, nil
}

func (z *MiniStorage) ReadFile(ctx context.Context, instanceId int64, path string, options *storage.ReadFileOptions) (storage.ByteStream, error) {
	return storage.FileToByteStream(ctx, filepath.Join(z.GetRoot(instanceId), path), options)
}

func (z *MiniStorage) GetRoot(instanceId int64) string {
	return z.getInstanceRootPath(instanceId)
}
