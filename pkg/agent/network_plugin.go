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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"velda.io/velda/pkg/clientlib"
	"velda.io/velda/pkg/proto"
	"velda.io/velda/pkg/utils"
)

const domainNameSuffix = ".local.velda"

type NetworkPlugin struct {
	PluginBase

	WorkspaceDir  string
	requestPlugin interface{}
}

func NewNetworkPlugin(requestPlugin interface{}, workspaceDir string) *NetworkPlugin {
	return &NetworkPlugin{
		requestPlugin: requestPlugin,
		WorkspaceDir:  workspaceDir,
	}
}

func (p *NetworkPlugin) Run(ctx context.Context) error {
	req := ctx.Value(p.requestPlugin).(*proto.SessionRequest)

	hostname := req.SessionId
	if err := syscall.Sethostname([]byte(hostname)); err != nil {
		return fmt.Errorf("set host name: %w", err)
	}

	domainName := domainNameSuffix[1:]
	if req.ServiceName != "" {
		domainName = req.ServiceName + domainNameSuffix
	}
	if err := syscall.Setdomainname([]byte(domainName)); err != nil {
		return fmt.Errorf("set domain name: %w", err)
	}

	fqdn := hostname + domainNameSuffix
	if req.ServiceName != "" {
		fqdn = fmt.Sprintf("%s.%s%s", hostname, req.ServiceName, domainNameSuffix)
	}

	ip := "127.0.0.1"
	cfg := clientlib.GetAgentConfig()
	if cfg != nil {
		deviceName := cfg.GetDaemonConfig().GetPrimaryNetworkDeviceName()
		externalIP, err := utils.DetectReportIPv4(deviceName)
		if err != nil {
			log.Printf("failed to detect external IP (device=%q): %v", deviceName, err)
		} else if externalIP != nil {
			ip = externalIP.String()
		}
	}

	if err := p.updateHostsFile(ip, hostname, fqdn); err != nil {
		log.Printf("failed to update hosts file in workspace: %v", err)
	}

	return p.RunNext(ctx)
}

func (p *NetworkPlugin) updateHostsFile(ip, hostname, fqdn string) error {
	bytes, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return err
	}

	entry := fmt.Sprintf("\n%s %s %s\n", ip, fqdn, hostname)
	if strings.Contains(string(bytes), " "+fqdn) || strings.Contains(string(bytes), " "+hostname) {
		entry = ""
	}
	// remove existing entries for the IP
	lines := strings.Split(string(bytes), "\n")
	newLines := []string{}
	for _, line := range lines {
		if !strings.HasPrefix(line, ip+" ") && !strings.HasPrefix(line, ip+"\t") {
			newLines = append(newLines, line)
		}
	}
	newLines = append(newLines, entry)
	bytes = []byte(strings.Join(newLines, "\n"))

	return os.WriteFile(filepath.Join(p.WorkspaceDir, "hosts"), bytes, 0644)
}
