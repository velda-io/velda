// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package digitalocean

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	agentpb "velda.io/velda/pkg/proto/agent"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

const (
	defaultAPIEndpoint = "https://api-amd.digitalocean.com"
	apiVersion         = "v2"
)

type digitaloceanPoolBackend struct {
	cfg        *proto.AutoscalerBackendDigitalOceanDroplet
	httpClient *http.Client
	apiBaseURL string
	namePrefix string

	dropletMu           sync.RWMutex
	activeDroplets      map[int]*droplet
	creatingDroplets    map[string]chan struct{}
	terminatingDroplets map[string]struct{}

	lastOp chan struct{}
}

type droplet struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Region    region   `json:"region"`
	Size      size     `json:"size"`
	Image     image    `json:"image"`
	CreatedAt string   `json:"created_at"`
	Networks  networks `json:"networks"`
	Tags      []string `json:"tags"`
}

type region struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type size struct {
	Slug string `json:"slug"`
}

type image struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type networks struct {
	V4 []networkV4 `json:"v4"`
	V6 []networkV6 `json:"v6"`
}

type networkV4 struct {
	IPAddress string `json:"ip_address"`
	Netmask   string `json:"netmask"`
	Gateway   string `json:"gateway"`
	Type      string `json:"type"`
}

type networkV6 struct {
	IPAddress string `json:"ip_address"`
	Netmask   int    `json:"netmask"`
	Gateway   string `json:"gateway"`
	Type      string `json:"type"`
}

type createDropletRequest struct {
	Name       string   `json:"name"`
	Region     string   `json:"region"`
	Size       string   `json:"size"`
	Image      string   `json:"image"`
	SSHKeys    []int    `json:"ssh_keys,omitempty"`
	Backups    bool     `json:"backups"`
	IPv6       bool     `json:"ipv6"`
	Monitoring bool     `json:"monitoring"`
	Tags       []string `json:"tags"`
	UserData   string   `json:"user_data,omitempty"`
	VpcUUID    string   `json:"vpc_uuid,omitempty"`
}

type createDropletResponse struct {
	Droplet droplet `json:"droplet"`
}

type listDropletsResponse struct {
	Droplets []droplet `json:"droplets"`
	Links    *links    `json:"links,omitempty"`
	Meta     *meta     `json:"meta,omitempty"`
}

type links struct {
	Pages pages `json:"pages"`
}

type pages struct {
	First string `json:"first,omitempty"`
	Prev  string `json:"prev,omitempty"`
	Next  string `json:"next,omitempty"`
	Last  string `json:"last,omitempty"`
}

type meta struct {
	Total int `json:"total"`
}

func NewDigitalOceanPoolBackend(cfg *proto.AutoscalerBackendDigitalOceanDroplet) broker.ResourcePoolBackend {
	apiEndpoint := cfg.GetApiEndpoint()
	if apiEndpoint == "" {
		apiEndpoint = defaultAPIEndpoint
	}

	prefix := calculateDropletPrefix(cfg)

	return &digitaloceanPoolBackend{
		cfg:                 cfg,
		httpClient:          &http.Client{Timeout: 30 * time.Second},
		apiBaseURL:          fmt.Sprintf("%s/%s", apiEndpoint, apiVersion),
		namePrefix:          prefix,
		activeDroplets:      make(map[int]*droplet),
		creatingDroplets:    make(map[string]chan struct{}),
		terminatingDroplets: make(map[string]struct{}),
	}
}

func calculateDropletPrefix(cfg *proto.AutoscalerBackendDigitalOceanDroplet) string {
	h := sha256.New()
	h.Write([]byte(cfg.GetSize()))
	h.Write([]byte(cfg.GetRegion()))
	h.Write([]byte(cfg.GetImage()))

	for _, key := range cfg.GetSshKeyIds() {
		h.Write([]byte(fmt.Sprintf("%d", key)))
	}
	if cfg.GetAgentVersionOverride() != "" {
		h.Write([]byte(cfg.GetAgentVersionOverride()))
	}

	hash := hex.EncodeToString(h.Sum(nil))
	return "velda-" + hash[:8]
}

func (d *digitaloceanPoolBackend) makeRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", d.apiBaseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	apiToken := d.cfg.GetApiToken()
	if apiToken == "" {
		apiToken = os.Getenv("DIGITALOCEAN_API_TOKEN")
	}
	if apiToken == "" {
		return nil, fmt.Errorf("API token not configured")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

func (d *digitaloceanPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	dropletName := fmt.Sprintf("%s-%s", d.namePrefix, utils.RandString(5))

	d.dropletMu.Lock()
	op := make(chan struct{})
	d.lastOp = op
	d.creatingDroplets[dropletName] = op
	d.dropletMu.Unlock()

	go func() {
		defer func() {
			d.dropletMu.Lock()
			delete(d.creatingDroplets, dropletName)
			d.dropletMu.Unlock()
			close(op)
		}()
		_, err := d.createDroplet(ctx, dropletName)
		if err != nil {
			log.Printf("Failed to create droplet %s: %v", dropletName, err)
		}
	}()

	return dropletName, nil
}

func (d *digitaloceanPoolBackend) createDroplet(ctx context.Context, dropletName string) (int, error) {
	agentConfig := d.cfg.AgentConfigContent
	version := d.cfg.AgentVersionOverride
	if version == "" {
		version = velda.Version
	}

	startupScriptParts := []string{`#!/bin/bash
set -e

apt update && apt install -y curl nfs-common

mkdir -p /etc/velda
cat << 'VELDA_CONFIG_EOF' > /etc/velda/agent.yaml
` + agentConfig + `
VELDA_CONFIG_EOF

nvidia-smi || true

curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/nvidia-collect.sh -o /tmp/nvidia-collect.sh && bash /tmp/nvidia-collect.sh || true
`}

	if tc := d.cfg.GetTailscaleConfig(); tc != nil && tc.GetPreAuthKey() != "" {
		tailscaleSetup := fmt.Sprintf(`
if ! command -v tailscale &> /dev/null; then
curl -fsSL https://tailscale.com/install.sh | sh
fi

tailscale up --login-server=%s --authkey=%s --accept-routes
`, tc.GetServer(), tc.GetPreAuthKey())
		startupScriptParts = append(startupScriptParts, tailscaleSetup)
	}

	veldaSetup := fmt.Sprintf(`
if [ "$(/bin/velda version 2>/dev/null || true)" != "%s" ]; then
    curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/velda-%s-linux-amd64 -o /tmp/velda
    chmod +x /tmp/velda
    mv /tmp/velda /bin/velda
fi

if [ ! -e /usr/lib/systemd/system/velda-agent.service ]; then
    curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/velda-agent.service -o /usr/lib/systemd/system/velda-agent.service
    systemctl daemon-reload
    systemctl enable velda-agent.service
    systemctl start velda-agent.service &
fi
`, version, version)
	startupScriptParts = append(startupScriptParts, veldaSetup)

	startupScript := strings.Join(startupScriptParts, "")

	tags := []string{"velda"}
	for k, v := range d.cfg.GetLabels() {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}

	reqBody := createDropletRequest{
		Name:       dropletName,
		Region:     d.cfg.GetRegion(),
		Size:       d.cfg.GetSize(),
		Image:      d.cfg.GetImage(),
		SSHKeys:    convertToIntSlice(d.cfg.GetSshKeyIds()),
		Backups:    d.cfg.GetBackups(),
		IPv6:       d.cfg.GetIpv6(),
		Monitoring: d.cfg.GetMonitoring(),
		Tags:       tags,
		UserData:   startupScript,
		VpcUUID:    d.cfg.GetVpcUuid(),
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("Creating DigitalOcean droplet: %s (size: %s, region: %s, image: %s)",
		dropletName, reqBody.Size, reqBody.Region, reqBody.Image)

	resp, err := d.makeRequest(ctx, "POST", "/droplets", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to create droplet: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createResp createDropletResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return 0, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Printf("Created DigitalOcean droplet %s (id: %d, status: %s)", dropletName, createResp.Droplet.ID, createResp.Droplet.Status)
	d.dropletMu.Lock()
	defer d.dropletMu.Unlock()
	d.activeDroplets[createResp.Droplet.ID] = &createResp.Droplet

	return createResp.Droplet.ID, nil
}

func (d *digitaloceanPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	var creatingOp chan struct{}
	d.dropletMu.Lock()
	creatingOp = d.creatingDroplets[workerName]
	d.terminatingDroplets[workerName] = struct{}{}
	op := make(chan struct{})
	d.lastOp = op
	d.dropletMu.Unlock()

	go func() {
		defer func() {
			d.dropletMu.Lock()
			delete(d.terminatingDroplets, workerName)
			d.dropletMu.Unlock()
			close(op)
		}()
		if creatingOp != nil {
			<-creatingOp
		}
		err := d.terminateDroplet(ctx, workerName)
		if err != nil {
			log.Printf("Failed to terminate droplet %s: %v", workerName, err)
		}
	}()

	return nil
}

func (d *digitaloceanPoolBackend) terminateDroplet(ctx context.Context, dropletName string) error {
	d.dropletMu.RLock()
	var dropletID int
	for id, drop := range d.activeDroplets {
		if drop.Name == dropletName {
			dropletID = id
			break
		}
	}
	d.dropletMu.RUnlock()

	if dropletID == 0 {
		return fmt.Errorf("droplet not found: %s", dropletName)
	}

	log.Printf("Terminating DigitalOcean droplet %s (id: %d)", dropletName, dropletID)

	resp, err := d.makeRequest(ctx, "DELETE", fmt.Sprintf("/droplets/%d", dropletID), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to terminate droplet: status %d, body: %s", resp.StatusCode, string(body))
	}

	log.Printf("Terminated DigitalOcean droplet %s", dropletName)

	d.dropletMu.Lock()
	delete(d.activeDroplets, dropletID)
	d.dropletMu.Unlock()

	return nil
}

func (d *digitaloceanPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	seen := make(map[string]struct{})
	var res []broker.WorkerStatus

	d.dropletMu.RLock()
	for creatingDroplet := range d.creatingDroplets {
		res = append(res, broker.WorkerStatus{Name: creatingDroplet})
		seen[creatingDroplet] = struct{}{}
	}
	for terminatingDroplet := range d.terminatingDroplets {
		seen[terminatingDroplet] = struct{}{}
	}
	d.dropletMu.RUnlock()

	path := "/droplets?tag_name=velda"
	resp, err := d.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list droplets: status %d, body: %s", resp.StatusCode, string(body))
	}

	var listResp listDropletsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	d.dropletMu.Lock()
	defer d.dropletMu.Unlock()

	d.activeDroplets = make(map[int]*droplet)
	for i := range listResp.Droplets {
		drop := &listResp.Droplets[i]

		if !strings.HasPrefix(drop.Name, d.namePrefix) {
			continue
		}

		d.activeDroplets[drop.ID] = drop

		if _, exists := seen[drop.Name]; exists {
			continue
		}

		if drop.Status == "active" || drop.Status == "new" {
			res = append(res, broker.WorkerStatus{Name: drop.Name})
			seen[drop.Name] = struct{}{}
		}
	}

	return res, nil
}

func (d *digitaloceanPoolBackend) WaitForLastOperation(ctx context.Context) error {
	if d.lastOp != nil {
		<-d.lastOp
	}
	return nil
}

func convertToIntSlice(input []int32) []int {
	result := make([]int, len(input))
	for i, v := range input {
		result[i] = int(v)
	}
	return result
}

type digitaloceanDropletPoolFactory struct{}

func (f *digitaloceanDropletPoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_DigitaloceanDroplet:
		return true
	}
	return false
}

func (f *digitaloceanDropletPoolFactory) NewBackend(pool *proto.AgentPool, brokerInfo *agentpb.BrokerInfo) (broker.ResourcePoolBackend, error) {
	doTemplate := pool.GetAutoScaler().GetBackend().GetDigitaloceanDroplet()

	if doTemplate.AgentConfig != nil {
		doTemplate = pb.Clone(doTemplate).(*proto.AutoscalerBackendDigitalOceanDroplet)
		doTemplate.AgentConfig.Pool = pool.GetName()
		if doTemplate.AgentConfig.Broker == nil {
			if brokerInfo == nil {
				return nil, fmt.Errorf("no default broker info provided for pool %s", pool.GetName())
			}
			doTemplate.AgentConfig.Broker = brokerInfo
		}
		var err error
		doTemplate.AgentConfigContent, err = utils.ProtoToYaml(doTemplate.AgentConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal agent config: %w", err)
		}
	}

	if doTemplate.GetSize() == "" {
		return nil, fmt.Errorf("size is required for DigitalOcean backend")
	}
	if doTemplate.GetRegion() == "" {
		return nil, fmt.Errorf("region is required for DigitalOcean backend")
	}
	if doTemplate.GetImage() == "" {
		return nil, fmt.Errorf("image is required for DigitalOcean backend")
	}

	return NewDigitalOceanPoolBackend(doTemplate), nil
}

func init() {
	backends.Register(&digitaloceanDropletPoolFactory{})
}
