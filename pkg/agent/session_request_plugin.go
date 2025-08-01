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
	"fmt"
	"io"
	"os"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/proto"
)

type SessionRequestPlugin struct {
	PluginBase
}

func NewSessionRequestPlugin() *SessionRequestPlugin {
	return &SessionRequestPlugin{}
}

func (p *SessionRequestPlugin) Run(ctx context.Context) error {
	req := &proto.SessionRequest{}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("Read session request %w", err)
	}
	if err := pb.Unmarshal(input, req); err != nil {
		return fmt.Errorf("Unmarshal session request %w", err)
	}
	return p.RunNext(context.WithValue(ctx, p, req))
}
