// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package backend_testing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"velda.io/velda/pkg/broker"
)

type hasWait interface {
	WaitForLastOperation(ctx context.Context) error
}

type dummyWait struct{}

func (d *dummyWait) WaitForLastOperation(ctx context.Context) error {
	return nil
}

func TestSimpleScaleUpDown(t *testing.T, backend broker.ResourcePoolBackend) {
	var instanceName string
	waiter, ok := backend.(hasWait)
	if !ok {
		t.Logf("Backend does not support waiting for last operation")
		waiter = &dummyWait{}
	}
	t.Run("RequestScaleUp", func(t *testing.T) {
		name, err := backend.RequestScaleUp(context.Background())
		assert.NoError(t, err)
		t.Logf("Scale up %s", name)
		instanceName = name
	})

	waiter.WaitForLastOperation(context.Background())

	t.Run("ListWorkers", func(t *testing.T) {
		workers, err := backend.ListWorkers(context.Background())
		assert.NoError(t, err)
		var found bool
		for _, w := range workers {
			t.Logf("Worker: %v", w)
			if w.Name == instanceName {
				found = true
			}
		}
		assert.True(t, found)
	})

	t.Run("RequestDelete", func(t *testing.T) {
		err := backend.RequestDelete(context.Background(), instanceName)
		assert.NoError(t, err)
		t.Logf("Scale down %s", instanceName)
	})
	waiter.WaitForLastOperation(context.Background())

	t.Run("ListWorkersAfterDelete", func(t *testing.T) {
		workers, err := backend.ListWorkers(context.Background())
		assert.NoError(t, err)
		var found bool
		for _, w := range workers {
			if w.Name == instanceName {
				found = true
			}
		}
		assert.False(t, found)
	})
}
