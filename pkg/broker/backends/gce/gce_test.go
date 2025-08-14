//go:build gce

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
package gce

// To run test:
// GCE_BACKEND=skyworkstation/us-west1-a/instance-group-1 go test -v ./pkg/broker/backends/gce

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/api/compute/v1"

	"velda.io/velda/pkg/broker/backends/backend_testing"
)

func TestGCEBackend(t *testing.T) {
	if os.Getenv("GCE_BACKEND") == "" {
		t.Skip("GCE_BACKEND not set")
	}
	gce_backend := strings.Split(os.Getenv("GCE_BACKEND"), "/")

	project := gce_backend[0]
	zone := gce_backend[1]
	instanceGroup := gce_backend[2]

	svc, err := compute.NewService(context.Background())
	if err != nil {
		t.Fatalf("Failed to create GCE service: %v", err)
	}

	backend := NewGCEPoolBackend(project, zone, instanceGroup, "test-instances-", svc)

	backend_testing.TestSimpleScaleUpDown(t, backend)
}
