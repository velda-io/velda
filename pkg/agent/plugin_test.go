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
package agent

import (
	"context"
	"testing"
)

type MockPlugin struct {
	PluginBase
	called bool
}

func (m *MockPlugin) Run(ctx context.Context) error {
	m.called = true
	return m.RunNext(ctx)
}

func TestNewPluginRunner(t *testing.T) {
	plugin1 := &MockPlugin{}
	plugin2 := &MockPlugin{}
	plugin3 := &MockPlugin{}

	runner := NewPluginRunner(plugin1, plugin2, plugin3)

	ctx := context.Background()
	err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !plugin1.called {
		t.Errorf("plugin1 was not called")
	}
	if !plugin2.called {
		t.Errorf("plugin2 was not called")
	}
	if !plugin3.called {
		t.Errorf("plugin3 was not called")
	}
}
