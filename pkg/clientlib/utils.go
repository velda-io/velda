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
package clientlib

import (
	"fmt"
	"os"
	"strings"
)

func RemoveLocalEnvs() []string {
	libraryPaths := strings.Split(os.Getenv("LD_LIBRARY_PATH"), ":")
	binPath := strings.Split(os.Getenv("PATH"), ":")
	output := []string{}
	for i, path := range libraryPaths {
		if path == "/var/nvidia/lib" {
			libraryPaths = append(libraryPaths[:i], libraryPaths[i+1:]...)
			output = append(output, fmt.Sprint("LD_LIBRARY_PATH=", strings.Join(libraryPaths, ":")))
			break
		}
	}
	for i, path := range binPath {
		if path == "/var/nvidia/bin" {
			binPath = append(binPath[:i], binPath[i+1:]...)
			output = append(output, fmt.Sprint("PATH=", strings.Join(binPath, ":")))
			break
		}
	}
	return output
}
