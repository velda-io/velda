//go:build wireinject

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
	"github.com/google/wire"
	"github.com/spf13/pflag"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/tasks"
)

func ProvideRunners(
	_ proto.BrokerServiceServer,
	_ proto.TaskServiceServer,
	_ proto.PoolManagerServiceServer,
	_ proto.InstanceServiceServer,
	metrics MetricRegistryRunner,
	provisioners ProvisionRunner,
	grpcRunner GrpcRunner,
	httpRunner HttpRunner,
	ready ReadySignalRunner,
) []Runner {
	return []Runner{
		Runner(metrics),
		Runner(provisioners),
		Runner(httpRunner),
		Runner(grpcRunner),
		Runner(ready),
	}
}

func RunAllService(flag *pflag.FlagSet) (CompletionError, error) {
	wire.Build(
		FlagProviders,
		ProvideRunners,
		ProvideConfig,
		ProvideRegionId,
		ProvideStorage,
		ProvidePermission,
		ProvideInstanceService,
		ProvideSchedulers,
		ProvideSessionDb,
		ProvideTaskTracker,
		ProvideLocalDiskStorage,
		ProvideWatcher,
		ProvideSessionHelper,
		ProvideBrokerInfo,
		wire.Bind(new(tasks.TaskTracker), (**broker.TaskTracker)(nil)),
		wire.InterfaceValue(new(broker.SessionCompletionWatcher), &broker.NullCompletionWatcher{}),
		DatabaseProviders,
		wire.Value(ServerAuthUnaryInterceptor(sessionInterceptor)),
		wire.Value(ServerAuthStreamInterceptor(sessionStreamInterceptor)),
		ProvideTaskLogDb,
		ProvideQuotaChecker,
		ProvideBrokerServer,
		ProvideTaskService,
		ProvideNfsBrokerAuth,
		ProvidePoolService,
		ServiceProviders,
	)
	return CompletionError(nil), nil
}
