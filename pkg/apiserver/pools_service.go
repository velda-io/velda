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
		pools = append(pools, &proto.Pool{Name: name, Description: pool.Description})
	}
	sort.Slice(pools, func(i, j int) bool {
		return pools[i].Name < pools[j].Name
	})
	return &proto.ListPoolsResponse{Pools: pools}, nil
}
