// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package broker

import "sync"

type GangCoordinator struct {
	mu        sync.Mutex
	desired   int
	notif     map[int]func()
	triggered bool
}

func NewGangCoordinator(desired int) *GangCoordinator {
	return &GangCoordinator{
		desired: desired,
		notif:   make(map[int]func()),
	}
}

func (g *GangCoordinator) Notify(id int, f func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.notif[id] = f
	if len(g.notif) == g.desired && !g.triggered {
		for _, fn := range g.notif {
			fn()
		}
		g.triggered = true
	}
}

func (g *GangCoordinator) Unnotify(id int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.triggered {
		return false
	}
	delete(g.notif, id)
	return true
}
