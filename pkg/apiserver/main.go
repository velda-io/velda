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
	"errors"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/pflag"

	_ "velda.io/velda/pkg/broker/backends/aws"
	_ "velda.io/velda/pkg/broker/backends/cmd"
	_ "velda.io/velda/pkg/broker/backends/gce"
	_ "velda.io/velda/pkg/broker/backends/k8s"
)

var (
	configPath string
	// If set to false, the service will exit on configuration change
	// This will allow the service to be restarted by a systemd service or other process manager
	restartOnConfigChange bool
	allMetrics            = prometheus.NewRegistry()
)

type Service interface {
	InitConfig(configPath string) error
	InitDatabase() error
	InitServices() error
	InitGrpcServer() error
	RegisterServices() error

	Run() error
	ExportedMetrics() *prometheus.Registry
}

func initService(s Service, configPath string) error {
	if err := s.InitConfig(configPath); err != nil {
		return fmt.Errorf("Failed to Initialize service configuration: %v", err)
	}

	if err := s.InitDatabase(); err != nil {
		return fmt.Errorf("Failed to Initialize database: %v", err)
	}

	if err := s.InitServices(); err != nil {
		return fmt.Errorf("Failed to Initialize services: %v", err)
	}

	if err := s.InitGrpcServer(); err != nil {
		return fmt.Errorf("Failed to Initialize gRPC server: %v", err)
	}

	if err := s.RegisterServices(); err != nil {
		return fmt.Errorf("Failed to register services: %v", err)
	}

	return nil
}

func AddFlags(flags *pflag.FlagSet) {
	flags.String("config", "config.yaml", "Path to the configuration file")
	flags.Bool("restart-on-config-change", true, "Restart service on configuration change")
}

func StartMetricServer(endpoint string) error {
	http.Handle("/metrics", promhttp.HandlerFor(allMetrics, promhttp.HandlerOpts{}))
	return http.ListenAndServe(endpoint, nil)
}

func Main(s Service, flags *pflag.FlagSet) {
	configPath, _ = flags.GetString("config")
	restartOnConfigChange, _ = flags.GetBool("restart-on-config-change")
	go StartMetricServer("localhost:6060")
	for {
		err := RunService(s)
		if errors.Is(err, ConfigChanged) && restartOnConfigChange {
			log.Println("Configuration changed, restarting service")
			// Reset the instance.
			t := reflect.TypeOf(s).Elem()
			s = reflect.New(t).Interface().(Service)
			continue
		}
		if err != nil {
			log.Fatalf("Service exited with error: %v", err)
		}
		break
	}
}

func RunService(svc Service) error {
	if err := initService(svc, configPath); err != nil {
		return fmt.Errorf("Failed to initialize service: %v", err)
	}
	metrics := svc.ExportedMetrics()
	allMetrics.MustRegister(metrics)
	defer allMetrics.Unregister(metrics)
	return svc.Run()
}
