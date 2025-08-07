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
	"os"
	"os/exec"
)

type Zfs struct {
	pool string
}

func NewZfs(pool string) *Zfs {
	return &Zfs{
		pool: pool,
	}
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
	return z.runCommand(
		ctx,
		"zfs",
		"clone",
		fmt.Sprintf("%s/%d@%s", z.pool, snapshotInstanceId, snapshotName),
		fmt.Sprintf("%s/%d", z.pool, instanceId))
}

func (z *Zfs) CreateInstanceFromImage(ctx context.Context, instanceId int64, imageName string) error {
	return z.runCommand(
		ctx,
		"zfs",
		"clone",
		fmt.Sprintf("%s/images/%s@image", z.pool, imageName),
		fmt.Sprintf("%s/%d", z.pool, instanceId))
}

func (z *Zfs) DeleteInstance(ctx context.Context, instanceId int64) error {
	return z.runCommand(
		ctx,
		"zfs",
		"destroy",
		fmt.Sprintf("%s/%d", z.pool, instanceId))
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
	if err := z.runCommand(
		ctx,
		"zfs",
		"clone",
		fmt.Sprintf("%s/%d@%s", z.pool, snapshotInstanceId, snapshotName),
		fmt.Sprintf("%s/images/%s", z.pool, imageName)); err != nil {
		return err
	}
	return z.runCommand(
		ctx,
		"zfs",
		"promote",
		fmt.Sprintf("%s/images/%s", z.pool, imageName))
}

func (z *Zfs) DeleteImage(ctx context.Context, imageName string) error {
	return z.runCommand(
		ctx,
		"zfs",
		"destroy",
		"-r",
		fmt.Sprintf("%s/images/%s", z.pool, imageName))
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
	cmd := exec.CommandContext(ctx, "sudo", command...)
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
	cmd := exec.CommandContext(ctx, "sudo", command...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error running command %v: %v (stderr: %s)", command, err, stderr.String())
	}
	return nil
}
