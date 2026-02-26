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
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Btrfs struct {
	rootPath      string
	maxDiskSizeGb int64
	uid           int
}

func NewBtrfs(rootPath string, maxDiskSizeGb int64) (*Btrfs, error) {
	b := &Btrfs{
		rootPath:      rootPath,
		maxDiskSizeGb: maxDiskSizeGb,
		uid:           os.Geteuid(),
	}
	err := b.init()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize BTRFS storage: %w", err)
	}
	return b, nil
}

func (b *Btrfs) init() error {
	// Check if the BTRFS root path exists
	if _, err := os.Stat(b.rootPath); os.IsNotExist(err) {
		return fmt.Errorf("BTRFS root path %s does not exist", b.rootPath)
	}

	// Verify that this is a BTRFS filesystem
	if err := b.runCommand(context.Background(), "btrfs", "filesystem", "show", b.rootPath); err != nil {
		return fmt.Errorf("failed to verify BTRFS filesystem at %s: %w", b.rootPath, err)
	}

	// Check if the required BTRFS subvolumes exist, and create them if they don't
	requiredSubvolumes := []string{"images", "image_archive"}
	for _, subvolume := range requiredSubvolumes {
		subvolumePath := fmt.Sprintf("%s/%s", b.rootPath, subvolume)
		if _, err := os.Stat(subvolumePath); os.IsNotExist(err) {
			if createErr := b.runCommand(context.Background(), "btrfs", "subvolume", "create", subvolumePath); createErr != nil {
				return fmt.Errorf("failed to create BTRFS subvolume %s: %w", subvolumePath, createErr)
			}
		}
	}
	return nil
}

func (b *Btrfs) Pool() string {
	return b.rootPath
}

func (b *Btrfs) CreateInstance(ctx context.Context, instanceId int64) error {
	instanceDir := fmt.Sprintf("%s/%d", b.rootPath, instanceId)
	instancePath := fmt.Sprintf("%s/current", instanceDir)
	snapshotsDir := fmt.Sprintf("%s/snapshots", instanceDir)

	// Create instance directory
	if err := b.runCommand(ctx, "mkdir", "-p", instanceDir); err != nil {
		return fmt.Errorf("failed to create instance directory %d: %w", instanceId, err)
	}

	// Create the current subvolume
	err := b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"create",
		instancePath)
	if err != nil {
		return fmt.Errorf("failed to create BTRFS instance %d: %w", instanceId, err)
	}

	// Create snapshots directory
	if err := b.runCommand(ctx, "mkdir", "-p", snapshotsDir); err != nil {
		return fmt.Errorf("failed to create snapshots directory for instance %d: %w", instanceId, err)
	}

	// If a maximum disk size is configured, set quota for this subvolume (GB).
	if b.maxDiskSizeGb > 0 {
		// Enable quota on the filesystem first (if not already enabled)
		_ = b.runCommand(ctx, "btrfs", "quota", "enable", b.rootPath)

		// Get the subvolume ID
		subvolId, err := b.getQuotaId(ctx, instancePath)
		if err != nil {
			return fmt.Errorf("failed to get subvolume ID for instance %d: %w", instanceId, err)
		}

		// Set quota limit
		if err := b.runCommand(
			ctx,
			"btrfs",
			"qgroup",
			"limit",
			fmt.Sprintf("%dG", b.maxDiskSizeGb),
			subvolId,
			b.rootPath); err != nil {
			return fmt.Errorf("failed to set quota for instance %d: %w", instanceId, err)
		}
	}

	// Make a minimal filesystem structure
	for _, dir := range []string{"proc", "sys", "dev", "run", "etc"} {
		err = b.runCommand(
			ctx,
			"mkdir",
			"-p",
			fmt.Sprintf("%s/%s", instancePath, dir))
		if err != nil {
			return fmt.Errorf("failed to create directory %s in instance %d: %w", dir, instanceId, err)
		}
	}
	return nil
}

func (b *Btrfs) CreateInstanceFromSnapshot(ctx context.Context, instanceId int64, snapshotInstanceId int64, snapshotName string) error {
	source := fmt.Sprintf("%s/%d/snapshots/%s", b.rootPath, snapshotInstanceId, snapshotName)
	instanceDir := fmt.Sprintf("%s/%d", b.rootPath, instanceId)
	target := fmt.Sprintf("%s/current", instanceDir)
	snapshotsDir := fmt.Sprintf("%s/snapshots", instanceDir)

	// Create instance directory
	if err := b.runCommand(ctx, "mkdir", "-p", instanceDir); err != nil {
		return fmt.Errorf("failed to create instance directory %d: %w", instanceId, err)
	}

	if err := b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"snapshot",
		source,
		target); err != nil {
		return err
	}

	// Create snapshots directory
	if err := b.runCommand(ctx, "mkdir", "-p", snapshotsDir); err != nil {
		return fmt.Errorf("failed to create snapshots directory for instance %d: %w", instanceId, err)
	}

	if b.maxDiskSizeGb > 0 {
		// Enable quota on the filesystem first (if not already enabled)
		_ = b.runCommand(ctx, "btrfs", "quota", "enable", b.rootPath)

		subvolId, err := b.getQuotaId(ctx, target)
		if err != nil {
			return fmt.Errorf("failed to get subvolume ID for instance %d: %w", instanceId, err)
		}

		if err := b.runCommand(
			ctx,
			"btrfs",
			"qgroup",
			"limit",
			fmt.Sprintf("%dG", b.maxDiskSizeGb),
			subvolId,
			b.rootPath); err != nil {
			return fmt.Errorf("failed to set quota for instance %d: %w", instanceId, err)
		}
	}
	return nil
}

func (b *Btrfs) CreateInstanceFromImage(ctx context.Context, instanceId int64, imageName string) error {
	source := fmt.Sprintf("%s/images/%s", b.rootPath, imageName)
	instanceDir := fmt.Sprintf("%s/%d", b.rootPath, instanceId)
	target := fmt.Sprintf("%s/current", instanceDir)
	snapshotsDir := fmt.Sprintf("%s/snapshots", instanceDir)

	// Create instance directory
	if err := b.runCommand(ctx, "mkdir", "-p", instanceDir); err != nil {
		return fmt.Errorf("failed to create instance directory %d: %w", instanceId, err)
	}

	if err := b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"snapshot",
		source,
		target); err != nil {
		return err
	}

	// Create snapshots directory
	if err := b.runCommand(ctx, "mkdir", "-p", snapshotsDir); err != nil {
		return fmt.Errorf("failed to create snapshots directory for instance %d: %w", instanceId, err)
	}

	if b.maxDiskSizeGb > 0 {
		// Enable quota on the filesystem first (if not already enabled)
		_ = b.runCommand(ctx, "btrfs", "quota", "enable", b.rootPath)

		subvolId, err := b.getQuotaId(ctx, target)
		if err != nil {
			return fmt.Errorf("failed to get subvolume ID for instance %d: %w", instanceId, err)
		}

		if err := b.runCommand(
			ctx,
			"btrfs",
			"qgroup",
			"limit",
			fmt.Sprintf("%dG", b.maxDiskSizeGb),
			subvolId,
			b.rootPath); err != nil {
			return fmt.Errorf("failed to set quota for instance %d: %w", instanceId, err)
		}
	}
	return nil
}

func (b *Btrfs) DeleteInstance(ctx context.Context, instanceId int64) error {
	var errDestroy error
	instanceDir := fmt.Sprintf("%s/%d", b.rootPath, instanceId)
	currentPath := fmt.Sprintf("%s/current", instanceDir)
	snapshotsDir := fmt.Sprintf("%s/snapshots", instanceDir)

	for retry := 0; retry < 3; retry++ {
		// First, delete all snapshots
		snapshots, err := b.listSnapshots(ctx, instanceId)
		if err == nil {
			for _, snapshot := range snapshots {
				snapshotPath := fmt.Sprintf("%s/%s", snapshotsDir, snapshot)
				_ = b.runCommand(ctx, "btrfs", "subvolume", "delete", snapshotPath)
			}
		}

		// Delete the current subvolume
		errDestroy = b.runCommand(
			ctx,
			"btrfs",
			"subvolume",
			"delete",
			currentPath)
		if errDestroy == nil {
			// Remove the instance directory
			_ = b.runCommand(ctx, "rm", "-rf", instanceDir)
			return nil
		}

		if strings.Contains(errDestroy.Error(), "busy") {
			// If the subvolume is busy, we need to wait for it to become idle
			log.Printf("Subvolume %d is busy, waiting...", instanceId)
			time.Sleep(10 * time.Second)
		}
	}
	return errDestroy
}

func (b *Btrfs) CreateSnapshot(ctx context.Context, instanceId int64, snapshotName string) error {
	source := fmt.Sprintf("%s/%d/current", b.rootPath, instanceId)
	target := fmt.Sprintf("%s/%d/snapshots/%s", b.rootPath, instanceId, snapshotName)

	return b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"snapshot",
		"-r", // Read-only snapshot
		source,
		target)
}

func (b *Btrfs) DeleteSnapshot(ctx context.Context, instanceId int64, snapshotName string) error {
	snapshotPath := fmt.Sprintf("%s/%d/snapshots/%s", b.rootPath, instanceId, snapshotName)
	return b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"delete",
		snapshotPath)
}

func (b *Btrfs) CreateImageFromSnapshot(ctx context.Context, imageName string, snapshotInstanceId int64, snapshotName string) error {
	source := fmt.Sprintf("%s/%d/snapshots/%s", b.rootPath, snapshotInstanceId, snapshotName)
	imageDir := fmt.Sprintf("%s/images/%s", b.rootPath, imageName)

	// Create a writable snapshot as the image
	if err := b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"snapshot",
		source,
		imageDir); err != nil {
		return err
	}

	return nil
}

func (b *Btrfs) DeleteImage(ctx context.Context, imageName string) error {
	imageDir := fmt.Sprintf("%s/images/%s", b.rootPath, imageName)

	err := b.runCommand(
		ctx,
		"btrfs",
		"subvolume",
		"delete",
		imageDir)
	if err != nil {
		return fmt.Errorf("failed to delete image %s: %w", imageName, err)
	}

	log.Printf("Image %s deleted successfully", imageName)
	return nil
}

func (b *Btrfs) ListImages(ctx context.Context) ([]string, error) {
	imagesPath := fmt.Sprintf("%s/images", b.rootPath)

	// Use ls command with sudo to list directory
	output, err := b.runCommandGetOutput(ctx, "ls", "-1", imagesPath)
	if err != nil {
		// If images directory doesn't exist, return empty list
		return []string{}, nil
	}

	var images []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line != "" {
			images = append(images, line)
		}
	}
	return images, nil
}

func (b *Btrfs) GetRoot(instanceId int64) string {
	return fmt.Sprintf("%s/%d/current", b.rootPath, instanceId)
}

func (b *Btrfs) GetSnapshotRoot(instanceId int64, snapshotName string) string {
	return fmt.Sprintf("%s/%d/snapshots/%s", b.rootPath, instanceId, snapshotName)
}

// Helper function to get subvolume ID
func (b *Btrfs) getQuotaId(ctx context.Context, path string) (string, error) {
	output, err := b.runCommandGetOutput(ctx, "btrfs", "subvolume", "show", path)
	if err != nil {
		return "", err
	}

	// Parse the output to get the subvolume ID
	// Output format: "        Subvolume ID:           123"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Subvolume ID:") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				return "0/" + parts[2], nil
			}
		}
	}
	return "", fmt.Errorf("failed to parse subvolume ID from output: %s", output)
}

// Helper function to list snapshots in an instance
func (b *Btrfs) listSnapshots(ctx context.Context, instanceId int64) ([]string, error) {
	snapshotsPath := fmt.Sprintf("%s/%d/snapshots", b.rootPath, instanceId)

	// Use ls command with sudo to list directory
	output, err := b.runCommandGetOutput(ctx, "ls", "-1", snapshotsPath)
	if err != nil {
		// If snapshots directory doesn't exist, return empty list
		return []string{}, nil
	}

	var snapshots []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line != "" {
			snapshots = append(snapshots, line)
		}
	}
	return snapshots, nil
}

func (b *Btrfs) runCommandGetOutput(ctx context.Context, command ...string) (string, error) {
	if len(command) == 0 {
		return "", fmt.Errorf("no command provided")
	}
	var cmd *exec.Cmd
	if b.uid != 0 {
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

func (b *Btrfs) runCommand(ctx context.Context, command ...string) error {
	if len(command) == 0 {
		return fmt.Errorf("no command provided")
	}
	var cmd *exec.Cmd
	if b.uid != 0 {
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
