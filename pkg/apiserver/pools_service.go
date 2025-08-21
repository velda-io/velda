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
	sort.Strings(poolNames)
	pools := make([]*proto.Pool, 0, len(poolNames))
	for _, pool := range poolNames {
		pools = append(pools, &proto.Pool{Name: pool})
	}
	return &proto.ListPoolsResponse{Pools: pools}, nil
}
