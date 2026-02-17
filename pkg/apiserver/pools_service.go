// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
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

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/proto"
)

type PoolManagerServiceServer struct {
	proto.UnimplementedPoolManagerServiceServer
	s *broker.SchedulerSet
}

func NewPoolManagerServiceServer(s *broker.SchedulerSet) *PoolManagerServiceServer {
	return &PoolManagerServiceServer{s: s}
}

func (s *PoolManagerServiceServer) ListPools(ctx context.Context, in *proto.ListPoolsRequest) (*proto.ListPoolsResponse, error) {
	poolNames := s.s.GetPools()
	pools := make([]*proto.Pool, 0, len(poolNames))
	for name, pool := range poolNames {
		pools = append(pools, &proto.Pool{Name: name, Description: pool.GetDescription()})
	}
	sort.Slice(pools, func(i, j int) bool {
		return pools[i].Name < pools[j].Name
	})
	return &proto.ListPoolsResponse{Pools: pools}, nil
}
