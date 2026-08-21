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
	"fmt"
	"os/exec"

	agentpb "velda.io/velda/pkg/proto/agent"
)

func sandboxRuntimeEnabled(sandboxConfig *agentpb.SandboxConfig) bool {
	if sandboxConfig == nil {
		return false
	}
	return sandboxConfig.GetSandboxRuntime() != agentpb.SandboxConfig_SANDBOX_RUNTIME_UNSPECIFIED
}

func resolveSandboxRuntimePath(sandboxConfig *agentpb.SandboxConfig) (string, error) {
	if sandboxConfig == nil {
		return "", fmt.Errorf("sandbox runtime config is not set")
	}
	runtimeBinary := sandboxConfig.GetSandboxRuntimePath()
	if runtimeBinary == "" {
		switch sandboxConfig.GetSandboxRuntime() {
		case agentpb.SandboxConfig_SANDBOX_RUNTIME_RUNC:
			runtimeBinary = "runc"
		case agentpb.SandboxConfig_SANDBOX_RUNTIME_RUNSC:
			runtimeBinary = "runsc"
		default:
			return "", fmt.Errorf("sandbox runtime is not enabled")
		}
	}
	resolvedPath, err := exec.LookPath(runtimeBinary)
	if err != nil {
		return "", fmt.Errorf("failed to find sandbox runtime %q: %w", runtimeBinary, err)
	}
	return resolvedPath, nil
}
