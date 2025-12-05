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
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// LookupUser looks up a user by username and returns user information with credentials.
// Provides fallback for root user if the OS doesn't support user lookup.
func LookupUser(username string) (*User, error) {
	// Provide fallback for root user, e.g. unsupported or uninitialized OS.
	user, err := lookupUserPosix(username)
	if err != nil && username == "root" {
		return &User{
			UserName: "root",
			Name:     "root",
			HomeDir:  "/",
			Shell:    "/bin/bash",
			Credential: &syscall.Credential{
				Uid:    0,
				Gid:    0,
				Groups: []uint32{0},
			},
		}, nil
	}
	return user, err
}

// lookupUserPosix looks up a user by username using POSIX-compatible methods.
// It retrieves user information and resolves all group memberships.
func lookupUserPosix(username string) (*User, error) {
	user, err := lookupUserInfo(username)
	if err != nil {
		return nil, err
	}

	groupCmd := exec.Command("id", "-G", username)
	output, err := groupCmd.Output()
	var groups []uint32
	if err == nil {
		groupstr := strings.Split(strings.Trim(string(output), "\n"), " ")
		for _, g := range groupstr {
			gid, err := strconv.Atoi(g)
			if err != nil {
				return nil, fmt.Errorf("Failed to parse group id: %w", err)
			}
			groups = append(groups, uint32(gid))
		}
	} else {
		// Try to parse /etc/group
		groupfile, err := os.ReadFile("/etc/group")
		if err != nil {
			return nil, fmt.Errorf("Failed to read /etc/group: %w", err)
		}
		groupstr := strings.Split(string(groupfile), "\n")
		for _, g := range groupstr {
			componenets := strings.Split(g, ":")
			if len(componenets) < 4 {
				log.Printf("Invalid group entry: %s", g)
				continue
			}
			members := strings.Split(componenets[3], ",")
			for _, m := range members {
				if m == username {
					groupId, err := strconv.ParseUint(componenets[2], 10, 32)
					if err != nil {
						return nil, fmt.Errorf("Failed to parse group id: %w", err)
					}
					groups = append(groups, uint32(groupId))
					break
				}
			}
		}
	}
	uid, err := strconv.ParseUint(user.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse user id: %w", err)
	}
	gid, err := strconv.ParseUint(user.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse group id: %w", err)
	}
	if len(groups) == 0 {
		groups = append(groups, uint32(gid))
	}
	user.Credential = &syscall.Credential{
		Uid:    uint32(uid),
		Gid:    uint32(gid),
		Groups: groups,
	}
	return user, nil
}

// lookupUserInfo retrieves basic user information from system databases.
// First tries getent, then falls back to /etc/passwd.
func lookupUserInfo(username string) (*User, error) {
	// First, try to use "getent" command to lookup the user.
	user := &User{}
	cmd := exec.Command("getent", "passwd", username)
	output, err := cmd.Output()
	if err == nil {
		if err := user.FromString(string(output)); err != nil {
			log.Printf("Failed to parse user info from getent: %v", err)
		} else {
			return user, nil
		}
	}

	// If getent failed, try to parse from "/etc/passwd".
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return nil, fmt.Errorf("Failed to open /etc/passwd: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Failed to read /etc/passwd: %w", err)
		}
		if strings.HasPrefix(line, username+":") {
			if err := user.FromString(line); err != nil {
				return nil, fmt.Errorf("Failed to parse user info: %w", err)
			}
			return user, nil
		}
	}

	return nil, fmt.Errorf("User not found: %s", username)
}

// LoadDefaultEnv reads default environment variables from /etc/environment.
// Returns a slice of environment variables in KEY=VALUE format.
func LoadDefaultEnv() []string {
	envfile, err := os.ReadFile("/etc/environment")
	result := []string{}
	if err != nil {
		return result
	}

	lines := strings.Split(string(envfile), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.Trim(parts[1], "\"")
		result = append(result, fmt.Sprintf("%s=%s", parts[0], value))
	}
	return result
}
