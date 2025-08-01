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
)

type AbstractPlugin interface {
	Run(ctx context.Context) error
}

type Plugin interface {
	AbstractPlugin
	setNext(next AbstractPlugin)
}

type PluginBase struct {
	nextPlugin AbstractPlugin
}

func (p *PluginBase) RunNext(ctx context.Context) error {
	if p.nextPlugin != nil {
		return p.nextPlugin.Run(ctx)
	}
	return nil
}

func (p *PluginBase) setNext(next AbstractPlugin) {
	p.nextPlugin = next
}

func NewPluginRunner(plugins ...Plugin) AbstractPlugin {
	for i := 0; i < len(plugins)-1; i++ {
		plugins[i].setNext(plugins[i+1])
	}
	return plugins[0]
}
