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
	"bufio"
	"fmt"
	"os"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func isAutofsRuntimeFstabEntry(fstype string, options []string) bool {
	if fstype == "autofs" {
		return true
	}
	for _, option := range options {
		if option == "x-lazy" {
			return true
		}
	}
	return false
}

func ParseRuntimeFstabMounts(fstabPath string) ([]specs.Mount, error) {
	file, err := os.Open(fstabPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	mounts := make([]specs.Mount, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			return nil, fmt.Errorf("invalid fstab line: %s", line)
		}
		options := strings.Split(fields[3], ",")
		if len(options) == 1 && options[0] == "" {
			options = nil
		}
		if isAutofsRuntimeFstabEntry(fields[2], options) {
			continue
		}
		mounts = append(mounts, specs.Mount{
			Destination: fields[1],
			Type:        fields[2],
			Source:      fields[0],
			Options:     options,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return mounts, nil
}
