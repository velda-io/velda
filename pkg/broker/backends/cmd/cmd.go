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
package cmd

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	proto "velda.io/velda/pkg/proto/config"
)

type cmdPoolBackend struct {
	startCmd string
	stopCmd  string
	listCmd  string
}

func NewCmdPoolBackend(startCmd, stopCmd, listCmd string) broker.ResourcePoolBackend {
	return &cmdPoolBackend{
		startCmd: startCmd,
		stopCmd:  stopCmd,
		listCmd:  listCmd,
	}
}

func (c *cmdPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	command := exec.Command("bash")
	command.Stdin = strings.NewReader(c.startCmd)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (c *cmdPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	command := exec.Command("bash", "-s", workerName)
	command.Stdin = strings.NewReader(c.stopCmd)
	command.Stderr = os.Stderr
	err := command.Run()
	if err != nil {
		return err
	}
	return nil
}

func (c *cmdPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	command := exec.Command("bash")
	command.Stdin = strings.NewReader(c.listCmd)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(output), "\n")
	workers := make([]broker.WorkerStatus, 0)
	for _, l := range lines {
		if l == "" {
			continue
		}
		workers = append(workers, broker.WorkerStatus{
			Name: l,
		})
	}
	return workers, nil
}

type cmdPoolFactory struct{}

func (f *cmdPoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_Command:
		return true
	}
	return false
}

func (f *cmdPoolFactory) NewBackend(pb *proto.AutoscalerBackend) (broker.ResourcePoolBackend, error) {
	cmd := pb.GetCommand()
	return NewCmdPoolBackend(cmd.Start, cmd.Stop, cmd.List), nil
}

func init() {
	backends.Register(&cmdPoolFactory{})
}
