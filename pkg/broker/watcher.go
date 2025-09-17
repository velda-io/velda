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
package broker

import (
	"context"
	"fmt"

	"github.com/simonfxr/pubsub"
	"velda.io/velda/pkg/proto"
)

type Watcher struct {
	bus *pubsub.Bus
}

type WatchCallback func(status *proto.ExecutionStatus) bool

func NewWatcher() *Watcher {
	return &Watcher{
		bus: pubsub.NewBus(),
	}
}

func (w *Watcher) SubscribeSession(ctx context.Context, instanceId int64, sessionId string, callback WatchCallback, initialStatus func() *proto.ExecutionStatus) {
	topic := fmt.Sprintf("session:%d:%s", instanceId, sessionId)
	w.safeSubscribe(ctx, topic, callback, initialStatus)
}

func (w *Watcher) SubscribeTask(ctx context.Context, taskId string, callback WatchCallback, initialStatus func() *proto.ExecutionStatus) {
	topic := fmt.Sprintf("task:%s", taskId)
	w.safeSubscribe(ctx, topic, callback, initialStatus)
}

func (w *Watcher) safeSubscribe(ctx context.Context, topic string, callback WatchCallback, initialStatusFunc func() *proto.ExecutionStatus) {
	ch := make(chan struct{})
	var s *pubsub.Subscription
	s = w.bus.Subscribe(topic, func(status *proto.ExecutionStatus) {
		<-ch
		if !callback(status) {
			w.bus.Unsubscribe(s)
		}
	})
	// Block all updates until we have sent the initial status
	initialStatus := initialStatusFunc()
	callback(initialStatus)
	close(ch)
	context.AfterFunc(ctx, func() {
		w.bus.Unsubscribe(s)
	})
}

func (w *Watcher) NotifyStateChange(instanceId int64, sessionId string, status *proto.ExecutionStatus) {
	topic := fmt.Sprintf("session:%d:%s", instanceId, sessionId)
	w.bus.Publish(topic, status)
}

func (w *Watcher) NotifyTaskChange(taskId string, status *proto.ExecutionStatus) {
	topic := fmt.Sprintf("task:%s", taskId)
	w.bus.Publish(topic, status)
}

func (w *Watcher) Unsubscribe(sub *pubsub.Subscription) {
	w.bus.Unsubscribe(sub)
}
