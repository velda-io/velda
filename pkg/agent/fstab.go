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
	"log"
	"os"
	"strings"
)

// FstabEntry represents a single entry in /etc/fstab
type FstabEntry struct {
	Source     string
	MountPoint string
	FSType     string
	Options    []string
	OptionsRaw string
	Dump       int
	Pass       int
}

// ParseFstab reads /etc/fstab and returns a list of FstabEntry
func ParseFstab(path string) (map[string]*FstabEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	mountPointMap := make(map[string]*FstabEntry)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			return nil, fmt.Errorf("invalid fstab line: %s", line)
		}

		dump, pass := 0, 0
		fmt.Sscanf(fields[4], "%d", &dump)
		fmt.Sscanf(fields[5], "%d", &pass)
		options := strings.Split(fields[3], ",")
		if len(options) == 0 || (len(options) == 1 && options[0] == "") {
			options = []string{}
		}

		// Skip if options doesn't contain "lazy"
		containsLazy := false
		for _, option := range options {
			if option == "x-lazy" {
				containsLazy = true
				break
			}
		}
		if !containsLazy {
			continue
		}

		entry := FstabEntry{
			Source:     fields[0],
			MountPoint: fields[1],
			FSType:     fields[2],
			Options:    options,
			OptionsRaw: fields[3],
			Dump:       dump,
			Pass:       pass,
		}
		mountPointMap[entry.MountPoint] = &entry
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	log.Printf("mountPointMap: %v", mountPointMap)
	return mountPointMap, nil
}
