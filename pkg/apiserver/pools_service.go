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
package apiserver

import (
	"context"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/proto"
)

type PoolManagerServiceServer struct {
	proto.UnimplementedPoolManagerServiceServer
	s   *broker.SchedulerSet
	ctx context.Context
}

func NewPoolManagerServiceServer(ctx context.Context, s *broker.SchedulerSet) *PoolManagerServiceServer {
	return &PoolManagerServiceServer{s: s, ctx: ctx}
}

func (s *PoolManagerServiceServer) ListPools(ctx context.Context, in *proto.ListPoolsRequest) (*proto.ListPoolsResponse, error) {
	poolNames := s.s.GetPools()
	poolStatuses := s.s.GetPoolAllocationStatuses()
	pools := make([]*proto.Pool, 0, len(poolNames))
	for name, pool := range poolNames {
		pools = append(pools, &proto.Pool{
			Name:             name,
			Description:      pool.GetDescription(),
			AutoscalerStatus: toProtoPoolAutoscalerStatus(poolStatuses[name]),
		})
	}
	sort.Slice(pools, func(i, j int) bool {
		return pools[i].Name < pools[j].Name
	})
	return &proto.ListPoolsResponse{Pools: pools}, nil
}

func (s *PoolManagerServiceServer) GetPool(ctx context.Context, in *proto.GetPoolRequest) (*proto.GetPoolResponse, error) {
	if in.GetPool() == "" {
		return nil, status.Error(codes.InvalidArgument, "pool name is required")
	}
	pool, err := s.s.GetPool(in.GetPool())
	if err != nil {
		return nil, err
	}
	var description string
	var status broker.PoolAllocationStatus
	if pool.PoolManager != nil {
		metadata := pool.PoolManager.Metadata.Load()
		if metadata != nil {
			description = metadata.GetDescription()
		}
		status = pool.PoolManager.GetAllocationStatus()
	}
	poolProto := &proto.Pool{Name: in.GetPool(), Description: description, AutoscalerStatus: toProtoPoolAutoscalerStatus(status)}
	return &proto.GetPoolResponse{Pool: poolProto}, nil
}

func (s *PoolManagerServiceServer) WatchPoolStatus(in *proto.WatchPoolStatusRequest, stream proto.PoolManagerService_WatchPoolStatusServer) error {
	if in.GetPool() == "" {
		return status.Error(codes.InvalidArgument, "pool name is required")
	}
	pool := in.GetPool()

	statusChan := make(chan broker.PoolAllocationStatus, 1)
	sub := s.s.SubscribePoolAllocationStatus(pool, statusChan)
	defer s.s.UnsubscribePoolAllocationStatus(sub)

	status, err := s.s.GetPoolAllocationStatus(pool)
	if err != nil {
		return err
	}
	if !status.LastAllocationErrorAt.IsZero() {
		if err := stream.Send(toProtoPoolStatusNotification(pool, status)); err != nil {
			return err
		}
	}

	var last string
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-stream.Context().Done():
			return stream.Context().Err()
		case status := <-statusChan:
			if status.LastAllocationErrorAt.IsZero() {
				last = ""
				continue
			}
			if status.LastAllocationError.Error() == last {
				continue
			}
			if err := stream.Send(toProtoPoolStatusNotification(pool, status)); err != nil {
				return err
			}
			last = status.LastAllocationError.Error()
		}
	}
}

func toProtoPoolAutoscalerStatus(status broker.PoolAllocationStatus) *proto.PoolAutoscalerStatus {
	if status.LastAllocationErrorAt.IsZero() {
		return &proto.PoolAutoscalerStatus{}
	}
	return &proto.PoolAutoscalerStatus{
		LastAllocationError:     broker.PoolAllocationErrorToClientString(status.LastAllocationError),
		LastAllocationErrorTime: timestamppb.New(status.LastAllocationErrorAt),
	}
}

func toProtoPoolStatusNotification(pool string, status broker.PoolAllocationStatus) *proto.PoolStatusNotification {
	return &proto.PoolStatusNotification{
		Pool:             pool,
		AutoscalerStatus: toProtoPoolAutoscalerStatus(status),
	}
}
