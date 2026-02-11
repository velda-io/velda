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
package instances

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"velda.io/velda/pkg/db"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/rbac"
	"velda.io/velda/pkg/storage"
	"velda.io/velda/pkg/utils"
)

const ShardOffset = utils.ShardOffset

const (
	ActionGetInsance    = "instance.get"
	ActionDeleteInsance = "instance.delete"

	ActionCreateSnapshot    = "instance.create_snapshot"
	ActionCloneFromSnapshot = "instance.clone_from_snapshot"
	ActionDeleteSnapshot    = "instance.delete_snapshot"
)

var validNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type InstanceDb interface {
	CreateInstance(ctx context.Context, in *proto.Instance) (*proto.Instance, db.Committer, error)
	GetInstance(ctx context.Context, in *proto.GetInstanceRequest) (*proto.Instance, error)
	GetInstanceByName(ctx context.Context, in *proto.GetInstanceByNameRequest) (*proto.Instance, error)
	ListInstances(ctx context.Context, in *proto.ListInstancesRequest) (*proto.ListInstancesResponse, error)
	DeleteInstance(ctx context.Context, in *proto.DeleteInstanceRequest) (db.Committer, *proto.Instance, error)
}

type service struct {
	proto.UnimplementedInstanceServiceServer
	db          InstanceDb
	storage     storage.Storage
	permissions rbac.Permissions
	shardCount  int
}

func NewService(db InstanceDb, storagebackend storage.Storage, permissions rbac.Permissions, shardCount int) proto.InstanceServiceServer {
	return &service{
		db:          db,
		storage:     storagebackend,
		permissions: permissions,
		shardCount:  shardCount,
	}
}

// TODO: Add permissions check.

func (s *service) CreateInstance(ctx context.Context, in *proto.CreateInstanceRequest) (*proto.Instance, error) {
	if err := checkInstanceName(in.Instance.InstanceName); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid instance name: %v", err)
	}
	shardId := 0
	if in.GetSnapshot() != nil {
		// Must follow source's shard.
		shardId = int(in.GetSnapshot().InstanceId >> ShardOffset)
	} else {
		shardId = rand.Intn(s.shardCount)
	}
	in.Instance.Id |= int64(shardId) << ShardOffset

	if in.Instance.InstanceName == "" {
		return nil, status.Errorf(codes.InvalidArgument, "instance name is required")
	}

	instance, committer, err := s.db.CreateInstance(ctx, in.Instance)
	if err != nil {
		return nil, err
	}
	log.Printf("Creating instance %s on shard %d, instance Id: %x",
		in.Instance.InstanceName, shardId, instance.Id)
	defer committer.Rollback()
	// TODO: DB should track status of snapshot creation.
	switch in.Source.(type) {
	case *proto.CreateInstanceRequest_Snapshot:
		snapshot := in.GetSnapshot()
		snapshotName := snapshot.SnapshotName
		if snapshotName == "" {
			// Generate a name from timestamp
			now := time.Now()
			snapshotName = fmt.Sprintf("snapshot-%d", now.Unix())
			err := s.storage.CreateSnapshot(ctx, snapshot.InstanceId, snapshotName)
			if err != nil {
				return nil, fmt.Errorf("error creating snapshot: %v", err)
			}
		}
		if err := s.permissions.Check(ctx, ActionCloneFromSnapshot, fmt.Sprintf("instances/%d", snapshot.InstanceId)); err != nil {
			return nil, err
		}

		if err := s.storage.CreateInstanceFromSnapshot(ctx, instance.Id, snapshot.InstanceId, snapshotName); err != nil {
			return nil, err
		}
	case *proto.CreateInstanceRequest_ImageName:
		image := in.GetImageName()
		if err := s.storage.CreateInstanceFromImage(ctx, instance.Id, image); err != nil {
			return nil, err
		}
	case nil:
		if err := s.storage.CreateInstance(ctx, instance.Id); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown disk source")
	}
	if err := committer.Commit(); err != nil {
		return nil, err
	}
	return instance, nil
}

func (s *service) GetInstance(ctx context.Context, in *proto.GetInstanceRequest) (*proto.Instance, error) {
	if err := s.permissions.Check(ctx, ActionGetInsance, fmt.Sprintf("instances/%d", in.InstanceId)); err != nil {
		return nil, err
	}
	return s.db.GetInstance(ctx, in)
}

func (s *service) GetInstanceByName(ctx context.Context, in *proto.GetInstanceByNameRequest) (*proto.Instance, error) {
	// Always filter by owner.
	return s.db.GetInstanceByName(ctx, in)
}

func (s *service) ListInstances(ctx context.Context, in *proto.ListInstancesRequest) (*proto.ListInstancesResponse, error) {
	// Always filter by owner.
	return s.db.ListInstances(ctx, in)
}

func (s *service) DeleteInstance(ctx context.Context, in *proto.DeleteInstanceRequest) (*emptypb.Empty, error) {
	if err := s.permissions.Check(ctx, ActionDeleteInsance, fmt.Sprintf("instances/%d", in.InstanceId)); err != nil {
		return nil, err
	}
	committer, inst, err := s.db.DeleteInstance(ctx, in)
	if err != nil {
		return nil, err
	}
	defer committer.Rollback()
	log.Printf("Deleting instance %d %s", in.InstanceId, inst.InstanceName)
	if err := s.storage.DeleteInstance(ctx, in.InstanceId); err != nil {
		return nil, fmt.Errorf("error deleting instance: %v", err)
	}
	if err := committer.Commit(); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *service) CreateSnapshot(ctx context.Context, in *proto.CreateSnapshotRequest) (*proto.SnapshotReference, error) {
	if err := s.permissions.Check(ctx, ActionCreateSnapshot, fmt.Sprintf("instances/%d", in.InstanceId)); err != nil {
		return nil, err
	}
	if err := s.storage.CreateSnapshot(ctx, in.InstanceId, in.SnapshotName); err != nil {
		return nil, fmt.Errorf("error creating snapshot: %v", err)
	}

	return &proto.SnapshotReference{
		InstanceId:   in.InstanceId,
		SnapshotName: in.SnapshotName,
	}, nil
}

func (s *service) DeleteSnapshot(ctx context.Context, in *proto.DeleteSnapshotRequest) (*emptypb.Empty, error) {
	if err := s.permissions.Check(ctx, ActionDeleteSnapshot, fmt.Sprintf("instances/%d", in.InstanceId)); err != nil {
		return nil, err
	}
	if err := s.storage.DeleteSnapshot(ctx, in.InstanceId, in.SnapshotName); err != nil {
		return nil, fmt.Errorf("error deleting snapshot: %v", err)
	}

	return &emptypb.Empty{}, nil
}

func (s *service) ListImages(ctx context.Context, in *proto.ListImagesRequest) (*proto.ListImagesResponse, error) {
	images, err := s.storage.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing images: %v", err)
	}
	return &proto.ListImagesResponse{Images: images}, nil
}

func (s *service) CreateImage(ctx context.Context, in *proto.CreateImageRequest) (*emptypb.Empty, error) {
	if err := s.permissions.Check(ctx, ActionCreateSnapshot, fmt.Sprintf("instances/%d", in.InstanceId)); err != nil {
		return nil, err
	}
	if in.SnapshotName == "" {
		// Create a snapshot first
		snapshotName := fmt.Sprintf("image-snapshot-%d", time.Now().Unix())
		if err := s.storage.CreateSnapshot(ctx, in.InstanceId, snapshotName); err != nil {
			return nil, fmt.Errorf("error creating snapshot for image: %v", err)
		}
		in.SnapshotName = snapshotName
	}
	if err := s.storage.CreateImageFromSnapshot(ctx, in.ImageName, in.InstanceId, in.SnapshotName); err != nil {
		return nil, fmt.Errorf("error creating image: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func (s *service) DeleteImage(ctx context.Context, in *proto.DeleteImageRequest) (*emptypb.Empty, error) {
	if err := s.storage.DeleteImage(ctx, in.ImageName); err != nil {
		return nil, fmt.Errorf("error deleting image: %v", err)
	}
	return &emptypb.Empty{}, nil
}

func checkInstanceName(name string) error {
	if len(name) < 3 || len(name) > 50 {
		return fmt.Errorf("instance name must be between 3 and 50 characters")
	}

	if !validNameRegex.MatchString(name) {
		return fmt.Errorf("instance name must start with a lowercase letter or digit and can only contain lowercase letters, numbers, and '-'")
	}
	return nil
}
