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
	"io"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
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
	allMetrics = prometheus.NewRegistry()
)

type Service interface {
	InitFromFlags(flags *pflag.FlagSet) error
	InitConfig() error
	InitDatabase() error
	InitServices() error
	InitGrpcServer() error
	RegisterServices() error

	Run() error
	ExportedMetrics() *prometheus.Registry
}

func initService(s Service) error {
	if err := s.InitConfig(); err != nil {
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
	flags.String("config", "", "Path to the configuration file")
	flags.Bool("restart-on-config-change", true, "Restart service on configuration change")
	flags.BoolP("foreground", "f", false, "Run service in foreground")
	flags.String("pidfile", "", "Path to the store the actual PID if not running in foreground.")
	flags.String("logfile", "", "Path to the log file, only if running in the background.")
	flags.Int("ready-fd", 0, "File descriptor for readiness signal")
	flags.MarkHidden("ready-fd")
}

func StartMetricServer(endpoint string) error {
	http.Handle("/metrics", promhttp.HandlerFor(allMetrics, promhttp.HandlerOpts{}))
	return http.ListenAndServe(endpoint, nil)
}

func Main(s Service, flags *pflag.FlagSet) {
	foreground, _ := flags.GetBool("foreground")
	if !foreground {
		logfile, _ := flags.GetString("logfile")
		pidfile, _ := flags.GetString("pidfile")
		if err := RunAsDaemon(os.Args[1:], logfile, pidfile); err != nil {
			log.Fatalf("Failed to start service as daemon: %v", err)
		}
		return
	}
	restartOnConfigChange, _ := flags.GetBool("restart-on-config-change")
	go StartMetricServer("localhost:6060")
	for {
		s.InitFromFlags(flags)
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
	if err := initService(svc); err != nil {
		return fmt.Errorf("Failed to initialize service: %v", err)
	}
	metrics := svc.ExportedMetrics()
	allMetrics.MustRegister(metrics)
	defer allMetrics.Unregister(metrics)
	return svc.Run()
}

func RunAsDaemon(args []string, logfile, pidfile string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Failed to get executable path: %v", err)
	}
	args = append(args, "--foreground", "--ready-fd", "3")
	subprocess := exec.Command(executable, args...)
	subprocess.Stdout = os.Stdout
	if logfile != "" {
		f, err := os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("Failed to open log file: %v", err)
		}
		defer f.Close()
		subprocess.Stderr = f
	} else {
		subprocess.Stderr = os.Stderr
	}
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("Failed to create pipe: %v", err)
	}
	defer r.Close()
	subprocess.ExtraFiles = []*os.File{w}
	if err := subprocess.Start(); err != nil {
		return fmt.Errorf("Failed to start subprocess: %v", err)
	}
	w.Close()

	if pidfile != "" {
		if err := os.WriteFile(pidfile, []byte(fmt.Sprintf("%d", subprocess.Process.Pid)), 0644); err != nil {
			return fmt.Errorf("Failed to write PID file: %v", err)
		}
	}
	// wait until r EOF
	if _, err := r.Read(make([]byte, 1)); err != nil && err != io.EOF {
		return fmt.Errorf("Failed to read from pipe: %v", err)
	}
	log.Printf("Service started with PID %d", subprocess.Process.Pid)
	return nil
}
