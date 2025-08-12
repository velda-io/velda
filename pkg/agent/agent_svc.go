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
package agent

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"regexp"
	"strings"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type AgentDaemonService struct {
	proto.UnimplementedAgentDaemonServer
	workDir        string
	chanCheckPoint chan *proto.CheckPointRequest
	tempDirs       []string // Temporary directories created for empty mounts
	tempDir        string   // Temporary directory for empty mounts
}

var emptyDirRegex = regexp.MustCompile(`^<\w*>$`)

func (s *AgentDaemonService) Mount(ctx context.Context, req *proto.MountRequest) (*emptypb.Empty, error) {
	if req.Fstype != "host" {
		return nil, status.Errorf(codes.Unimplemented, "unsupported fstype")
	}
	source := req.Source
	if emptyDirRegex.MatchString(source) {
		var err error
		baseTempDir := s.tempDir
		if baseTempDir == "" {
			baseTempDir = s.workDir
		}
		source, err = os.MkdirTemp(baseTempDir, "emptydir-")
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create empty dir: %v", err)
		}
		if err := os.Chmod(source, 0o777); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to chmod empty dir: %v", err)
		}
		s.tempDirs = append(s.tempDirs, source)
		log.Printf("Created temporary empty directory: %s to mount %s", source, req.Target)
	} else if !strings.HasPrefix(source, "/tmp/shared") {
		// TODO: Properly validate source perission
		return nil, status.Errorf(codes.InvalidArgument, "source must be under /tmp")
	}
	// Use bind mount
	if err := syscall.Mount(source, path.Join(s.workDir, "workspace", req.Target), "", syscall.MS_BIND, ""); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to mount: %v", err)
	}
	log.Printf("Mounted %s to %s", source, path.Join(s.workDir, "workspace", req.Target))
	return &emptypb.Empty{}, nil
}

func (s *AgentDaemonService) CheckPoint(ctx context.Context, req *proto.CheckPointRequest) (*emptypb.Empty, error) {
	if s.chanCheckPoint == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "CheckPoint channel not initialized")
	}
	s.chanCheckPoint <- req
	return &emptypb.Empty{}, nil
}

func (s *AgentDaemonService) Cleanup() error {
	for _, dir := range s.tempDirs {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("Failed to remove temporary directory %s: %v", dir, err)
		} else {
			log.Printf("Removed temporary directory %s", dir)
		}
	}
	s.tempDirs = nil
	return nil
}

type AgentDaemonPlugin struct {
	PluginBase
	WorkspaceDir   string
	TempDir        string // Temporary directory for empty mounts
	ChanCheckPoint chan *proto.CheckPointRequest
}

func NewAgentDaemonPlugin(workspaceDir string, sandboxCfg *agentpb.SandboxConfig) *AgentDaemonPlugin {
	return &AgentDaemonPlugin{
		WorkspaceDir:   workspaceDir,
		TempDir:        sandboxCfg.GetHostLocalMountBaseDir(),
		ChanCheckPoint: make(chan *proto.CheckPointRequest, 1),
	}
}

func (p *AgentDaemonPlugin) Run(ctx context.Context) error {
	workDir := p.WorkspaceDir

	// Start AgentDaemonServer from a Unix socket
	socketPath := path.Join(workDir, "velda/agent.sock")
	if err := os.RemoveAll(socketPath); err != nil {
		return err
	}
	log.Printf("Starting agent daemon on %s", socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("Agent daemon listen: %w", err)
	}

	grpcServer := grpc.NewServer()
	daemonSvc := &AgentDaemonService{
		workDir:        workDir,
		chanCheckPoint: p.ChanCheckPoint,
		tempDirs:       []string{},
		tempDir:        p.TempDir,
	}
	proto.RegisterAgentDaemonServer(grpcServer, daemonSvc)

	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()
	defer listener.Close()
	defer grpcServer.Stop()
	defer daemonSvc.Cleanup()
	return p.RunNext(ctx)
}
