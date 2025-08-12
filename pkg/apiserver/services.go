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
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	prometheus_grpc "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"google.golang.org/grpc"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	autoscaler "velda.io/velda/pkg/broker/backends"
	"velda.io/velda/pkg/instances"
	"velda.io/velda/pkg/proto"
	configpb "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/rbac"
	"velda.io/velda/pkg/storage"
	"velda.io/velda/pkg/storage/zfs"
	"velda.io/velda/pkg/tasks"
	"velda.io/velda/pkg/utils"
)

var (
	ConfigChanged = errors.New("config changed")
)

type database interface {
	Init() error
	RunMaintenances(ctx context.Context)
}

// Open-source service implementation.
type OssService struct {
	GrpcServer  *grpc.Server
	HttpHandler *http.ServeMux
	Config      *configpb.Config

	Db              database
	Permissions     rbac.Permissions
	Storage         storage.Storage
	InstanceService proto.InstanceServiceServer
	Schedulers      *broker.SchedulerSet
	Sessions        *broker.SessionDatabase
	TaskTracker     *broker.TaskTracker
	BrokerService   proto.BrokerServiceServer
	TaskService     proto.TaskServiceServer
	httpServer      *http.Server
	Metrics         *prometheus.Registry
	Mux             *runtime.ServeMux
	GrpcMetrics     *prometheus_grpc.ServerMetrics
	ExecError       chan error
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewService creates a new Service instance with the provided configuration
func (s *OssService) InitConfig(configPath string) error {
	s.ExecError = make(chan error, 1)
	s.ctx, s.cancel = context.WithCancel(context.Background())

	var err error
	s.Config, err = utils.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("Failed to load configuration: %v", err)
	}
	return nil
}

// InitDatabase initializes the database connection and schema
func (s *OssService) InitDatabase() error {
	var err error
	s.Storage, err = BuildStorage(s.Config.GetStorage())
	if err != nil {
		return fmt.Errorf("Failed to build storage: %v", err)
	}

	s.Db = zfs.NewZfsInstanceDb(s.Storage.(*zfs.Zfs))
	if err := s.Db.Init(); err != nil {
		return fmt.Errorf("Failed to Initialize database: %v", err)
	}
	s.Permissions = &rbac.AlwaysAllowPermissions{}
	return nil
}

func (s *OssService) InitServices() error {
	s.InstanceService = instances.NewService(s.Db.(instances.InstanceDb), s.Storage, s.Permissions, 1)

	s.Schedulers = BuildAutoscalers(s.ctx, s.Config.GetAgentPools())
	s.Schedulers.AllowCreateNewPool = s.Config.AllowNewPool

	// Initialize session database
	s.Sessions = broker.NewSessionDatabase(nil)

	// Initialize task tracker
	taskTrackerId := uuid.New().String()
	s.TaskTracker = broker.NewTaskTracker(s.Schedulers, s.Sessions, s.Db.(broker.TaskQueueDb), taskTrackerId)
	nfsAuth := broker.NewNfsExportAuth("/" + s.Storage.(*zfs.Zfs).Pool())
	s.BrokerService = broker.NewBrokerServer(s.Schedulers, s.Sessions, s.Permissions, s.TaskTracker, nfsAuth, s.Db.(broker.TaskDb))

	// Initialize other services
	s.TaskService = tasks.NewTaskServiceServer(s.Db.(tasks.TaskDb), s.Permissions)
	return nil
}

// InitGrpcServer initializes the gRPC server
func (s *OssService) InitGrpcServer() error {

	s.GrpcMetrics = prometheus_grpc.NewServerMetrics(
		prometheus_grpc.WithServerHandlingTimeHistogram(),
	)
	s.GrpcServer = grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			s.GrpcMetrics.UnaryServerInterceptor(),
			//s.authDecoder.AuthInterceptor(utils.AuthRequired),
		),
		grpc.StreamInterceptor(s.GrpcMetrics.StreamServerInterceptor()),
	)
	return nil
}

func (s *OssService) RegisterServices() error {
	proto.RegisterInstanceServiceServer(s.GrpcServer, s.InstanceService)
	proto.RegisterBrokerServiceServer(s.GrpcServer, s.BrokerService)
	proto.RegisterTaskServiceServer(s.GrpcServer, s.TaskService)

	// Initialize HTTP server with gRPC gateway
	s.Mux = runtime.NewServeMux()
	proto.RegisterInstanceServiceHandlerServer(context.Background(), s.Mux, s.InstanceService)
	proto.RegisterBrokerServiceHandlerServer(context.Background(), s.Mux, s.BrokerService)
	proto.RegisterTaskServiceHandlerServer(context.Background(), s.Mux, s.TaskService)

	// Setup metrics
	s.Metrics = prometheus.NewRegistry()
	s.Metrics.MustRegister(s.GrpcMetrics)
	s.Metrics.MustRegister(collectors.NewGoCollector())
	s.Metrics.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Setup HTTP handler
	s.HttpHandler = http.NewServeMux()
	s.HttpHandler.Handle("/", s.Mux)

	return nil
}

// Run starts all the service components and blocks until shutdown
func (s *OssService) Run() error {
	s.GrpcMetrics.InitializeMetrics(s.GrpcServer)
	defer s.cancel()

	// Initialize provisioners
	for _, provisionerCfg := range s.Config.Provisioners {
		provisioner, err := backends.NewProvisioner(provisionerCfg, s.Schedulers)
		if err != nil {
			return fmt.Errorf("Failed to create provisioner: %v", err)
		}
		provisioner.Run(s.ctx)
	}

	go s.Db.RunMaintenances(s.ctx)

	// Start task trackers for each pool
	for _, pool := range s.Config.GetAgentPools() {
		go func(poolName string) {
			err := s.TaskTracker.PollTasks(s.ctx, poolName)
			if !errors.Is(err, context.Canceled) {
				log.Printf("Task tracker for pool %s exited: %v", poolName, err)
			}
		}(pool.Name)
	}

	// Start HTTP server
	s.httpServer = &http.Server{
		Addr:    s.Config.GetServer().HttpAddress,
		Handler: s.HttpHandler,
	}
	go func() {
		log.Printf("Serving HTTP on %s", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.ExecError <- fmt.Errorf("Failed to serve: %v", err)
		}
	}()

	// Start gRPC server
	go func() {
		// Initialize gRPC server
		lis, err := net.Listen("tcp", s.Config.GetServer().GrpcAddress)
		if err != nil {
			s.ExecError <- fmt.Errorf("Failed to listen: %v", err)
		}
		log.Printf("Serving GRPC on %s", lis.Addr().String())
		if err := s.GrpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			s.ExecError <- fmt.Errorf("Failed to serve: %v", err)
		}
	}()

	// Wait for SIGINT (Ctrl+C) or SIGTERM.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	var completionErr error
	select {
	case sig := <-ch:
		log.Println("Received termination signal:", sig)
	case completionErr = <-s.ExecError:
	}

	// Cancel the context to stop all long-running operations
	s.cancel()

	// Perform graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()

	stopped := make(chan struct{})
	// Stop the grpc server.
	go func() {
		s.GrpcServer.GracefulStop()
		close(stopped)
	}()
	// Stop the http server.
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Failed to shutdown HTTP server: %v", err)
	}
	// Wait for both servers to stop.
	select {
	case <-stopped:
		log.Println("Server stopped")
		break
	case <-shutdownCtx.Done():
		log.Println("GRPC server shutdown timed out")
		s.GrpcServer.Stop()
	}
	return completionErr
}

func (s *OssService) ExportedMetrics() *prometheus.Registry {
	return s.Metrics
}

func BuildStorage(config *configpb.Storage) (storage.Storage, error) {
	switch config.Storage.(type) {
	case *configpb.Storage_Zfs_:
		return zfs.NewZfs(config.GetZfs().Pool)
	default:
		return nil, fmt.Errorf("Unknown storage type: %v", config.String())
	}
}

func BuildAutoscalers(ctx context.Context, pools []*configpb.AgentPool) *broker.SchedulerSet {
	scheduler := broker.NewSchedulerSet(ctx)
	for _, poolConfig := range pools {
		pool, _ := scheduler.GetPool(poolConfig.Name)
		if poolConfig.AutoScaler != nil {
			config, err := autoscaler.AutoScaledConfigFromConfig(ctx, poolConfig)
			if err != nil {
				log.Fatalf("Failed to create autoscaler backend: %v", err)
			}
			pool.PoolManager.UpdateConfig(config)
			if poolConfig.AutoScaler.InitialDelay != nil {
				time.AfterFunc(poolConfig.AutoScaler.InitialDelay.AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
			}
			log.Printf("Created autoscaler for pool %s", poolConfig.Name)
		}
	}
	return scheduler
}
