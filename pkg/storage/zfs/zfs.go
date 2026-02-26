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
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Zfs struct {
	pool          string
	maxDiskSizeGb int64
	uid           int
}

func NewZfs(pool string, maxDiskSizeGb int64) (*Zfs, error) {
	z := &Zfs{
		pool:          pool,
		maxDiskSizeGb: maxDiskSizeGb,
		uid:           os.Geteuid(),
	}
	err := z.init()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ZFS storage: %w", err)
	}
	return z, nil
}

func (z *Zfs) init() error {
	// Check if the ZFS pool exists
	err := z.runCommand(context.Background(), "zfs", "list", z.pool)
	if err != nil {
		return fmt.Errorf("failed to list ZFS pools: %w", err)
	}
	// Check if the required ZFS volumes exist, and create them if they don't
	requiredVolumes := []string{"images", "image_archive"}
	for _, volume := range requiredVolumes {
		volumePath := fmt.Sprintf("%s/%s", z.pool, volume)
		if err := z.runCommand(context.Background(), "zfs", "list", volumePath); err != nil {
			if createErr := z.runCommand(context.Background(), "zfs", "create", volumePath); createErr != nil {
				return fmt.Errorf("failed to create ZFS volume %s: %w", volumePath, createErr)
			}
		}
	}
	return nil
}

func (z *Zfs) Pool() string {
	return z.pool
}

func (z *Zfs) CreateInstance(ctx context.Context, instanceId int64) error {
	err := z.runCommand(
		ctx,
		"zfs",
		"create",
		fmt.Sprintf("%s/%d", z.pool, instanceId))
	if err != nil {
		return fmt.Errorf("failed to create ZFS instance %d: %w", instanceId, err)
	}
	// If a maximum disk size is configured, set refquota for this dataset (GB).
	if z.maxDiskSizeGb > 0 {
		if err := z.runCommand(
			ctx,
			"zfs",
			"set",
			fmt.Sprintf("refquota=%dG", z.maxDiskSizeGb),
			fmt.Sprintf("%s/%d", z.pool, instanceId)); err != nil {
			return fmt.Errorf("failed to set refquota for instance %d: %w", instanceId, err)
		}
	}
	// Make a minimal filesystem structure
	for _, dir := range []string{"proc", "sys", "dev", "run", "etc"} {
		err = z.runCommand(
			ctx,
			"mkdir",
			fmt.Sprintf("/%s/%d/%s", z.pool, instanceId, dir))
		if err != nil {
			return fmt.Errorf("failed to create directory %s in instance %d: %w", dir, instanceId, err)
		}
	}
	return nil
}

func (z *Zfs) CreateInstanceFromSnapshot(ctx context.Context, instanceId int64, snapshotInstanceId int64, snapshotName string) error {
	target := fmt.Sprintf("%s/%d", z.pool, instanceId)
	if err := z.runCommand(
		ctx,
		"zfs",
		"clone",
		fmt.Sprintf("%s/%d@%s", z.pool, snapshotInstanceId, snapshotName),
		target); err != nil {
		return err
	}
	if z.maxDiskSizeGb > 0 {
		if err := z.runCommand(
			ctx,
			"zfs",
			"set",
			fmt.Sprintf("refquota=%dG", z.maxDiskSizeGb),
			target); err != nil {
			return fmt.Errorf("failed to set refquota for instance %d: %w", instanceId, err)
		}
	}
	return nil
}

func (z *Zfs) CreateInstanceFromImage(ctx context.Context, instanceId int64, imageName string) error {
	target := fmt.Sprintf("%s/%d", z.pool, instanceId)
	if err := z.runCommand(
		ctx,
		"zfs",
		"clone",
		fmt.Sprintf("%s/images/%s@image", z.pool, imageName),
		target); err != nil {
		return err
	}
	if z.maxDiskSizeGb > 0 {
		if err := z.runCommand(
			ctx,
			"zfs",
			"set",
			fmt.Sprintf("refquota=%dG", z.maxDiskSizeGb),
			target); err != nil {
			return fmt.Errorf("failed to set refquota for instance %d: %w", instanceId, err)
		}
	}
	return nil
}

func (z *Zfs) DeleteInstance(ctx context.Context, instanceId int64) error {
	var errDestroy error
	target := fmt.Sprintf("%s/%d", z.pool, instanceId)
	for retry := 0; retry < 3; retry++ {
		errDestroy = z.runCommand(
			ctx,
			"zfs",
			"destroy",
			"-rf",
			target)
		if errDestroy == nil {
			return nil
		}
		if strings.Contains(errDestroy.Error(), "filesystem has dependent clones") {
			// If the instance has dependent clones, we need to promote them to be the primary
			// Search for clones by check "origin" property
			instanceList, err := z.runCommandGetOutput(
				ctx,
				"zfs",
				"list",
				"-d", "1",
				"-H",
				"-o",
				"name,origin",
				fmt.Sprintf("%s", z.pool))
			if err != nil {
				return fmt.Errorf("failed to list ZFS instances: %w", err)
			}
			prefix := fmt.Sprintf("%s/%d@", z.pool, instanceId)
			for _, line := range strings.Split(instanceList, "\n") {
				// Each line is in the format "name origin"
				parts := strings.Fields(line)
				if len(parts) != 2 {
					continue
				}
				name := parts[0]
				origin := parts[1]
				if strings.HasPrefix(origin, prefix) {
					// Promote the clone
					log.Printf("Promoting clone %s because %d is deleting", name, instanceId)
					if err := z.runCommand(
						ctx,
						"zfs",
						"promote",
						name); err != nil {
						return fmt.Errorf("failed to promote clone %s: %w", name, err)
					}
					break
				}
			}
		} else if strings.Contains(errDestroy.Error(), "pool or dataset is busy") {
			// If the dataset is busy, we need to wait for it to become idle
			log.Printf("Dataset %d is busy, unmount & waiting...", instanceId)
			time.Sleep(10 * time.Second)
		}
	}
	return errDestroy
}

func (z *Zfs) CreateSnapshot(ctx context.Context, instanceId int64, snapshotName string) error {
	return z.runCommand(
		ctx,
		"zfs",
		"snapshot",
		fmt.Sprintf("%s/%d@%s", z.pool, instanceId, snapshotName))
}

func (z *Zfs) DeleteSnapshot(ctx context.Context, instanceId int64, snapshot_name string) error {
	return z.runCommand(
		ctx,
		"zfs",
		"destroy",
		fmt.Sprintf("%s/%d@%s", z.pool, instanceId, snapshot_name))
}

func (z *Zfs) CreateImageFromSnapshot(ctx context.Context, imageName string, snapshotInstanceId int64, snapshotName string) error {
	imageTarget := fmt.Sprintf("%s/images/%s", z.pool, imageName)
	if err := z.runCommand(
		ctx,
		"zfs",
		"clone",
		fmt.Sprintf("%s/%d@%s", z.pool, snapshotInstanceId, snapshotName),
		imageTarget); err != nil {
		return err
	}
	if z.maxDiskSizeGb > 0 {
		if err := z.runCommand(
			ctx,
			"zfs",
			"set",
			fmt.Sprintf("refquota=%dG", z.maxDiskSizeGb),
			imageTarget); err != nil {
			return fmt.Errorf("failed to set refquota for image %s: %w", imageName, err)
		}
	}
	if err := z.runCommand(
		ctx,
		"zfs",
		"promote",
		fmt.Sprintf("%s/images/%s", z.pool, imageName)); err != nil {
		return fmt.Errorf("failed to promote image %s: %w", imageName, err)
	}
	if err := z.runCommand(
		ctx,
		"zfs",
		"rename",
		fmt.Sprintf("%s/images/%s@%s", z.pool, imageName, snapshotName),
		fmt.Sprintf("%s/images/%s@image", z.pool, imageName)); err != nil {
		return fmt.Errorf("failed to set mountpoint for image %s: %w", imageName, err)
	}
	return nil
}

func (z *Zfs) DeleteImage(ctx context.Context, imageName string) error {
	err := z.runCommand(
		ctx,
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/images/%s", z.pool, imageName))
	if err != nil {
		// Try to rename the image to archive.
		archiveName := fmt.Sprintf("%s-archived-%d", imageName, time.Now().Unix())
		err = z.runCommand(
			ctx,
			"zfs",
			"rename",
			fmt.Sprintf("%s/images/%s", z.pool, imageName),
			fmt.Sprintf("%s/image_archive/%s", z.pool, archiveName))
		if err != nil {
			return fmt.Errorf("failed to archive image %s: %w", imageName, err)
		}
		log.Printf("Image %s archived to %s", imageName, archiveName)
		return nil
	}
	log.Printf("Image %s deleted successfully", imageName)
	return nil
}

func (z *Zfs) ListImages(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(fmt.Sprintf("/%s/images", z.pool))
	if err != nil {
		return nil, err
	}
	var images []string
	for _, entry := range entries {
		images = append(images, entry.Name())
	}
	return images, nil
}

func (z *Zfs) runCommandGetOutput(ctx context.Context, command ...string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("no command provided")
	}
	var cmd *exec.Cmd
	if z.uid != 0 {
		cmd = exec.CommandContext(ctx, "sudo", command...)
	} else {
		cmd = exec.CommandContext(ctx, command[0], command[1:]...)
	}
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error running command %v: %v (stderr: %s)", command, err, stderr.String())
	}
	return stdout.String(), nil
}

func (z *Zfs) runCommand(ctx context.Context, command ...string) error {
	if len(command) == 0 {
		return fmt.Errorf("no command provided")
	}
	var cmd *exec.Cmd
	if z.uid != 0 {
		cmd = exec.CommandContext(ctx, "sudo", command...)
	} else {
		cmd = exec.CommandContext(ctx, command[0], command[1:]...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error running command %v: %v (stderr: %s)", command, err, stderr.String())
	}
	return nil
}

func (z *Zfs) GetRoot(instanceId int64) string {
	return fmt.Sprintf("/%s/%d", z.pool, instanceId)
}

func (z *Zfs) GetSnapshotRoot(instanceId int64, snapshotName string) string {
	return fmt.Sprintf("/%s/%d/.zfs/snapshot/%s", z.pool, instanceId, snapshotName)
}
