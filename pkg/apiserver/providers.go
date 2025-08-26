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
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/google/wire"
	prometheus_grpc "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	"velda.io/velda/pkg/instances"
	"velda.io/velda/pkg/proto"
	configpb "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/rbac"
	"velda.io/velda/pkg/storage"
	"velda.io/velda/pkg/storage/mini"
	"velda.io/velda/pkg/storage/zfs"
	"velda.io/velda/pkg/tasks"
	"velda.io/velda/pkg/utils"
)

var (
	ConfigChanged = errors.New("config changed")
)

// The provider of runner should start the workload, and return a cleanup function.
type Runner func(shutdownCtx context.Context)

type ConfigPath string
type ReadyFd int

type ServiceCtx struct {
	ctx       context.Context
	cancel    context.CancelFunc
	ExecError chan error
}

type database interface {
	Init() error
	RunMaintenances(ctx context.Context)
	instances.InstanceDb
	broker.TaskDb
	broker.TaskQueueDb
	tasks.TaskDb
}

func ProvideConfigPath(flags *pflag.FlagSet) ConfigPath {
	configPath, _ := flags.GetString("config")
	return ConfigPath(configPath)
}

func ProvideReadyFd(flags *pflag.FlagSet) ReadyFd {
	readyFd, _ := flags.GetInt("ready-fd")
	return ReadyFd(readyFd)
}

var FlagProviders = wire.NewSet(
	ProvideConfigPath,
	ProvideReadyFd,
)

func ProvideConfig(cfgPath ConfigPath) (*configpb.Config, error) {
	if cfgPath == "" {
		return nil, errors.New("Config path not provided")
	}
	return utils.LoadConfig(string(cfgPath))
}

func ProvideBaseCtx() *ServiceCtx {
	ctx, cancel := context.WithCancel(context.Background())
	return &ServiceCtx{
		ctx:       ctx,
		cancel:    cancel,
		ExecError: make(chan error, 1),
	}
}

func ProvideCtx(s *ServiceCtx) context.Context {
	return s.ctx
}

func ProvideStorage(cfg *configpb.Config) (storage.Storage, error) {
	config := cfg.GetStorage()
	switch config.Storage.(type) {
	case *configpb.Storage_Zfs_:
		return zfs.NewZfs(config.GetZfs().Pool)
	case *configpb.Storage_Mini:
		return mini.NewMiniStorage(config.GetMini().Root)
	default:
		return nil, fmt.Errorf("Unknown storage type: %v", config.String())
	}
}

func ProvidePermission() rbac.Permissions {
	return &rbac.AlwaysAllowPermissions{}
}

func ProvideDb(s storage.Storage, ctx context.Context) (database, error) {
	var db database
	if zfsdb, ok := s.(*zfs.Zfs); ok {
		db = zfs.NewZfsInstanceDb(zfsdb)
	} else if minidb, ok := s.(*mini.MiniStorage); ok {
		db = mini.NewMiniInstanceDb(minidb)
	} else {
		return nil, fmt.Errorf("Unsupported storage type: %T", s)
	}
	if err := db.Init(); err != nil {
		return nil, err
	}
	go db.RunMaintenances(ctx)
	return db, nil
}

func ProvideInstanceService(grpcServer *grpc.Server, mux *runtime.ServeMux, db instances.InstanceDb, storage storage.Storage, perm rbac.Permissions) proto.InstanceServiceServer {
	s := instances.NewService(db, storage, perm, 1)
	proto.RegisterInstanceServiceServer(grpcServer, s)
	proto.RegisterInstanceServiceHandlerServer(context.Background(), mux, s)
	return s
}

func ProvideSchedulers(ctx context.Context, cfg *configpb.Config) (*broker.SchedulerSet, error) {
	pools := cfg.AgentPools
	scheduler := broker.NewSchedulerSet(ctx)
	for _, poolConfig := range pools {
		pool, _ := scheduler.GetPool(poolConfig.Name)
		if poolConfig.AutoScaler != nil {
			config, err := backends.AutoScaledConfigFromConfig(ctx, poolConfig)
			if err != nil {
				return nil, fmt.Errorf("Failed to create autoscaler backend: %v", err)
			}
			pool.PoolManager.UpdateConfig(config)
			if poolConfig.AutoScaler.InitialDelay != nil {
				time.AfterFunc(poolConfig.AutoScaler.InitialDelay.AsDuration(), pool.PoolManager.ReadyForIdleMaintenance)
			}
			log.Printf("Created autoscaler for pool %s", poolConfig.Name)
		}
	}
	scheduler.AllowCreateNewPool = cfg.AllowNewPool
	return scheduler, nil
}

var ProvideSessionDb = broker.NewSessionDatabase

func ProvideTaskTracker(config *configpb.Config, ctx context.Context, scheduler *broker.SchedulerSet, sessiondb *broker.SessionDatabase, taskdb broker.TaskQueueDb) *broker.TaskTracker {
	taskTrackerId := uuid.New().String()
	tracker := broker.NewTaskTracker(scheduler, sessiondb, taskdb, taskTrackerId)

	// Start task trackers for each pool
	for _, pool := range config.GetAgentPools() {
		go func(poolName string) {
			err := tracker.PollTasks(ctx, poolName)
			if !errors.Is(err, context.Canceled) {
				log.Printf("Task tracker for pool %s exited: %v", poolName, err)
			}
		}(pool.Name)
	}
	return tracker
}

func ProvideLocalDiskStorage(s storage.Storage) broker.LocalDiskProvider {
	return s.(broker.LocalDiskProvider)
}

func ProvideTaskLogDb(s storage.Storage) tasks.TaskLogDb {
	return storage.NewLocalStorageLogDb(s)
}

func ProvideBrokerServer(grpcServer *grpc.Server, mux *runtime.ServeMux, schedulers *broker.SchedulerSet, sessions *broker.SessionDatabase, permissions rbac.Permissions, taskTracker *broker.TaskTracker, auth broker.AuthHelper, taskdb broker.TaskDb) proto.BrokerServiceServer {
	s := broker.NewBrokerServer(schedulers, sessions, permissions, taskTracker, auth, taskdb)
	proto.RegisterBrokerServiceServer(grpcServer, s)
	proto.RegisterBrokerServiceHandlerServer(context.Background(), mux, s)
	return s
}

func ProvideTaskService(grpcServer *grpc.Server, mux *runtime.ServeMux, db tasks.TaskDb, logdb tasks.TaskLogDb, perm rbac.Permissions) proto.TaskServiceServer {
	s := tasks.NewTaskServiceServer(db, logdb, perm)
	proto.RegisterTaskServiceServer(grpcServer, s)
	proto.RegisterTaskServiceHandlerServer(context.Background(), mux, s)
	return s
}

var ProvideNfsBrokerAuth = wire.NewSet(
	broker.NewNfsExportAuth,
	wire.Bind(new(broker.AuthHelper), new(*broker.NfsExportAuth)),
)

func ProvidePoolService(grpcServer *grpc.Server, mux *runtime.ServeMux, schedulers *broker.SchedulerSet) proto.PoolManagerServiceServer {
	s := NewPoolManagerServiceServer(schedulers)
	proto.RegisterPoolManagerServiceServer(grpcServer, s)
	proto.RegisterPoolManagerServiceHandlerServer(context.Background(), mux, s)
	return s
}

type ServerAuthUnaryInterceptor grpc.UnaryServerInterceptor

func ProvideGrpcMetrics() *prometheus_grpc.ServerMetrics {
	return prometheus_grpc.NewServerMetrics(
		prometheus_grpc.WithServerHandlingTimeHistogram(),
	)
}

func ProvideGrpcServer(metrics *prometheus_grpc.ServerMetrics, authInterceptor ServerAuthUnaryInterceptor) *grpc.Server {
	return grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			metrics.UnaryServerInterceptor(),
			grpc.UnaryServerInterceptor(authInterceptor),
		),
		grpc.StreamInterceptor(
			metrics.StreamServerInterceptor(),
		),
	)
}

func ProvideGrpcMux(httpHandler *http.ServeMux) *runtime.ServeMux {
	m := runtime.NewServeMux()
	httpHandler.Handle("/", m)
	return m
}

type MetricRegistryRunner Runner

func ProvideMetrics(r *prometheus.Registry, grpcMetrics *prometheus_grpc.ServerMetrics) MetricRegistryRunner {
	r.MustRegister(grpcMetrics)
	goCollector := collectors.NewGoCollector()
	r.MustRegister(goCollector)
	processCollector := collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})
	r.MustRegister(processCollector)
	return func(_ context.Context) {
		r.Unregister(grpcMetrics)
		r.Unregister(goCollector)
		r.Unregister(processCollector)
	}
}

func ProvideHttpHandler() *http.ServeMux {
	return http.NewServeMux()
}

type ProvisionRunner Runner

func ProvideProvisioners(ctx context.Context, config *configpb.Config, schedulers *broker.SchedulerSet) (ProvisionRunner, error) {
	// Initialize provisioners
	for _, provisionerCfg := range config.Provisioners {
		provisioner, err := backends.NewProvisioner(provisionerCfg, schedulers)
		if err != nil {
			return nil, fmt.Errorf("Failed to create provisioner: %v", err)
		}
		provisioner.Run(ctx)
	}
	return nil, nil
}

type GrpcRunner Runner

func ProvideGrpcRunner(serviceCtx *ServiceCtx, config *configpb.Config, grpcServer *grpc.Server) (GrpcRunner, error) {
	// Initialize gRPC server
	lis, err := net.Listen("tcp", config.GetServer().GrpcAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed to listen: %v", err)
	}
	go func() {
		log.Printf("Serving GRPC on %s", lis.Addr().String())
		if err := grpcServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			serviceCtx.ExecError <- fmt.Errorf("Failed to serve: %v", err)
		}
	}()
	return func(shutdownCtx context.Context) {
		grpcServer.GracefulStop()
	}, nil
}

type HttpRunner Runner

func ProvideHttpRunner(serviceCtx *ServiceCtx, config *configpb.Config, httpHandler *http.ServeMux) (HttpRunner, error) {
	lis, err := net.Listen("tcp", config.GetServer().HttpAddress)
	if err != nil {
		return nil, fmt.Errorf("Failed to listen: %v", err)
	}
	httpServer := &http.Server{
		Handler: httpHandler,
	}
	go func() {
		log.Printf("Serving HTTP on %s", lis.Addr().String())
		if err := httpServer.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serviceCtx.ExecError <- fmt.Errorf("Failed to serve: %v", err)
		}
	}()
	return func(shutdownCtx context.Context) {
		httpServer.Shutdown(shutdownCtx)
	}, nil
}

type ReadySignalRunner Runner

func ProvideReadySignal(fd ReadyFd) ReadySignalRunner {
	if fd > 0 {
		readyFile := os.NewFile(uintptr(fd), "ready-fd")
		readyFile.Close()
	}
	return nil
}

type CompletionError error

func ProvideCompletionSignal(runners []Runner, serviceCtx *ServiceCtx) CompletionError {
	defer RunCleanup(runners)
	// Wait for SIGINT (Ctrl+C) or SIGTERM.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	var completionErr error
	select {
	case sig := <-ch:
		log.Println("Received termination signal:", sig)
	case completionErr = <-serviceCtx.ExecError:
	}
	return CompletionError(completionErr)
}

func RunCleanup(runners []Runner) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second*20)
	defer cancel()
	wg := sync.WaitGroup{}
	for _, runner := range runners {
		if runner == nil {
			continue
		}
		wg.Add(1)
		go func(r Runner) {
			defer wg.Done()
			r(shutdownCtx)
		}(runner)
	}
	wg.Wait()
}

var DatabaseProviders = wire.NewSet(
	ProvideDb,
	wire.Bind(new(broker.TaskQueueDb), new(database)),
	wire.Bind(new(broker.TaskDb), new(database)),
	wire.Bind(new(tasks.TaskDb), new(database)),
	wire.Bind(new(instances.InstanceDb), new(database)),
)

var ServiceProviders = wire.NewSet(
	wire.Value(AllMetrics),
	ProvideBaseCtx,
	ProvideCtx,
	ProvideHttpHandler,
	ProvideGrpcMux,
	ProvideGrpcMetrics,
	ProvideMetrics,
	ProvideProvisioners,
	ProvideGrpcServer,
	ProvideGrpcRunner,
	ProvideHttpRunner,
	ProvideReadySignal,
	ProvideCompletionSignal,
)
