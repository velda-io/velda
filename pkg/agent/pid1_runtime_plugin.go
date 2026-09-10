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
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"strconv"
	"syscall"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	pb "google.golang.org/protobuf/proto"
	"velda.io/velda/pkg/proto"
	agentpb "velda.io/velda/pkg/proto/agent"
)

type RunPid1WithRuntimePlugin struct {
	PluginBase
	WorkspaceDir         string
	requestPlugin        *SessionRequestPlugin
	linuxNamespacePlugin *LinuxNamespacePlugin
	devicesPlugin        *DevicesPlugin
	sandboxConfig        *agentpb.SandboxConfig
	runtimePath          string
	runtimeID            string
	runtimeCleanup       func()
}

func NewRunPid1WithRuntimePlugin(workspaceDir string, sandboxCfg *agentpb.SandboxConfig, agentDaemonPlugin *AgentDaemonPlugin, requestPlugin *SessionRequestPlugin, linuxNamespacePlugin *LinuxNamespacePlugin, devicesPlugin *DevicesPlugin) *RunPid1WithRuntimePlugin {
	return &RunPid1WithRuntimePlugin{
		WorkspaceDir:         workspaceDir,
		requestPlugin:        requestPlugin,
		linuxNamespacePlugin: linuxNamespacePlugin,
		devicesPlugin:        devicesPlugin,
		sandboxConfig:        sandboxCfg,
	}
}

func (p *RunPid1WithRuntimePlugin) Run(ctx context.Context) error {
	request := ctx.Value(p.requestPlugin).(*proto.SessionRequest)
	if !p.runtimeEnabled() {
		return p.RunNext(ctx)
	}

	var cmd *os.Process
	var err error
	p.runtimePath = ""
	p.runtimeID = ""
	defer func() {
		if p.runtimeCleanup != nil {
			p.runtimeCleanup()
			p.runtimeCleanup = nil
		}
	}()
	if !request.Checkpointed {
		sandboxConfig := p.sandboxConfig
		log.Printf("Launching sandbox pid1 via runtime %v", sandboxConfig.GetSandboxRuntime())
		cmd, err = p.runPid1WithRuntime(request, sandboxConfig)
	} else {
		return fmt.Errorf("restore from checkpoint is not supported with runtime")
	}
	if err != nil {
		return fmt.Errorf("Failed to run pid1: %w", err)
	}

	completion := make(chan *os.ProcessState, 1)
	sigfinish := make(chan struct{})
	defer close(sigfinish)
	sigtermChan := make(chan os.Signal, 1)
	signal.Notify(sigtermChan, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		for {
			select {
			case sig := <-sigtermChan:
				if p.forwardSignalWithRuntime(sig) {
					continue
				}
				if sig == syscall.SIGQUIT {
					err := killWithCgroup()
					log.Printf("Received SIGQUIT, killing with cgroup: %v", err)
				} else if cmd != nil {
					cmd.Signal(sig)
				}
			case <-sigfinish:
				return
			}
		}
	}()
	go func() {
		ps, err := cmd.Wait()
		if err != nil {
			close(completion)
			log.Printf("pid1 exited with error: %v", err)
		} else {
			completion <- ps
		}
	}()
	for {
		select {
		case state, ok := <-completion:
			if !ok {
				log.Printf("pid1 process state channel closed")
				return fmt.Errorf("pid1 process state channel closed unexpectedly")
			}
			returnCode := state.ExitCode()
			if returnCode != 0 {
				return fmt.Errorf("pid1 exited with code %d", returnCode)
			}
			return nil
		}
	}
}

func (p *RunPid1WithRuntimePlugin) runPid1WithRuntime(request *proto.SessionRequest, runtimeConfig *agentpb.SandboxConfig) (*os.Process, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	runtimePath, err := resolveSandboxRuntimePath(runtimeConfig)
	if err != nil {
		return nil, err
	}
	p.runtimePath = runtimePath
	p.runtimeID = request.SessionId
	processArgs := append([]string{"/run/velda/velda"}, os.Args[1:]...)
	processArgs = append(processArgs, "--pid1")
	spec := specs.Spec{
		Version: specs.Version,
		Process: &specs.Process{
			Terminal: false,
			Args:     processArgs,
			Env:      os.Environ(),
			Cwd:      "/",
		},
		Root: &specs.Root{Path: "workspace"},
		Mounts: []specs.Mount{
			{
				Destination: "/proc",
				Type:        "proc",
				Source:      "proc",
				Options:     []string{"nosuid", "noexec", "nodev"},
			},
		},
		Linux: &specs.Linux{
			Resources: &specs.LinuxResources{
				Devices: []specs.LinuxDeviceCgroup{
					{
						Allow:  true,
						Type:   "c",
						Major:  int64Ptr(10),
						Minor:  int64Ptr(235),
						Access: "rwm",
					},
				},
			},
			RootfsPropagation: "shared",
		},
	}
	if err := p.appendRuntimeSpec(&spec); err != nil {
		return nil, err
	}
	configBytes, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal runtime config: %w", err)
	}
	if err := os.WriteFile(path.Join(p.WorkspaceDir, "config.json"), configBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write runtime config: %w", err)
	}
	log.Printf("Starting runtime %s with bundle %s", runtimePath, p.WorkspaceDir)
	args := []string{"run", "--bundle", p.WorkspaceDir, request.SessionId}
	if request.Workload != nil {
		args = append(args, "--preserve-fds", "1")
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe for stdin: %w", err)
	}
	requestBytes, err := pb.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session request: %w", err)
	}
	cmd := exec.Command(runtimePath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdinR
	if request.Workload != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, os.NewFile(3, "/proc/self/fd/3")) // Batch output
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start sandbox runtime: %w", err)
	}
	stdinR.Close()
	go func() {
		defer stdinW.Close()
		_, err := stdinW.Write(requestBytes)
		if err != nil {
			log.Printf("Failed to write session request to runtime stdin: %v", err)
		}
	}()
	return cmd.Process, nil
}
func (p *RunPid1WithRuntimePlugin) appendRuntimeNamespaceSpec(spec *specs.Spec) {
	if spec.Linux == nil {
		spec.Linux = &specs.Linux{}
	}
	spec.Linux.Namespaces = []specs.LinuxNamespace{
		{Type: specs.PIDNamespace},
		{Type: specs.IPCNamespace},
		{Type: specs.UTSNamespace},
		{Type: specs.MountNamespace},
		// For now, use host network.
		//{Type: specs.NetworkNamespace},
		{Type: specs.CgroupNamespace},
	}
}

func (p *RunPid1WithRuntimePlugin) appendRuntimeSpec(spec *specs.Spec) error {
	p.appendRuntimeNamespaceSpec(spec)
	if err := p.linuxNamespacePlugin.appendRuntimeSpec(spec); err != nil {
		return err
	}
	if err := p.devicesPlugin.appendRuntimeSpec(spec); err != nil {
		return err
	}
	return nil
}

func (p *RunPid1WithRuntimePlugin) runtimeEnabled() bool {
	return sandboxRuntimeEnabled(p.sandboxConfig)
}

func (p *RunPid1WithRuntimePlugin) forwardSignalWithRuntime(sig os.Signal) bool {
	if p.runtimePath == "" || p.runtimeID == "" {
		return false
	}
	sigValue, ok := sig.(syscall.Signal)
	if !ok {
		return false
	}
	sigArg := strconv.Itoa(int(sigValue))
	killCmd := exec.Command(p.runtimePath, "kill", p.runtimeID, sigArg)
	output, err := killCmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to forward signal %v with runtime %s kill %s: %v, output: %s", sig, p.runtimePath, p.runtimeID, err, string(output))
		return false
	}
	log.Printf("Forwarded signal %v via runtime %s kill %s", sig, p.runtimePath, p.runtimeID)
	return true
}
