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
package agent

import "os/exec"

// CommandModifier is a function type that modifies an exec.Cmd before execution,
// typically used to set environment variables or other execution parameters.
type CommandModifier func(cmd *exec.Cmd)

func gpuModifier(libraryPath, binPath string) func(*exec.Cmd) {
	return func(cmd *exec.Cmd) {
		existingLdLibraryPath := ""
		existingPath := ""

		for _, env := range cmd.Env {
			if len(env) > 15 && env[:15] == "LD_LIBRARY_PATH=" {
				existingLdLibraryPath = env[16:]
			}
			if len(env) > 5 && env[:5] == "PATH=" {
				existingPath = env[5:]
			}
		}

		if existingLdLibraryPath != "" {
			libraryPath = libraryPath + ":" + existingLdLibraryPath
		}

		if existingPath != "" {
			binPath = binPath + ":" + existingPath
		}
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+libraryPath)
		cmd.Env = append(cmd.Env, "PATH="+binPath)
	}
}
