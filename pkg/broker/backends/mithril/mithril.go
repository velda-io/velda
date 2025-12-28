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
package mithril

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
	defaultAPIEndpoint = "https://api.mithril.ai"
	apiVersion         = "v2"
)

type mithrilPoolBackend struct {
	cfg        *proto.AutoscalerBackendMithrilSpotBid
	httpClient *http.Client
	apiBaseURL string
	bidPrefix  string // prefix for bid names based on config hash
}

type spotBid struct {
	FID              string               `json:"fid"`
	Name             string               `json:"name"`
	Project          string               `json:"project"`
	Status           string               `json:"status"`
	Instances        []string             `json:"instances"`
	InstanceType     string               `json:"instance_type"`
	Region           string               `json:"region"`
	LimitPrice       string               `json:"limit_price"`
	InstanceQuantity int                  `json:"instance_quantity"`
	LaunchSpec       *launchSpecification `json:"launch_specification"`
	CreatedAt        time.Time            `json:"created_at"`
}

type launchSpecification struct {
	Volumes           []string `json:"volumes"`
	SSHKeys           []string `json:"ssh_keys"`
	StartupScript     string   `json:"startup_script,omitempty"`
	KubernetesCluster string   `json:"kubernetes_cluster,omitempty"`
	ImageVersion      string   `json:"image_version,omitempty"`
	MemoryGB          int      `json:"memory_gb,omitempty"`
}

type createBidRequest struct {
	Project             string               `json:"project"`
	Region              string               `json:"region"`
	InstanceType        string               `json:"instance_type"`
	LimitPrice          string               `json:"limit_price"`
	InstanceQuantity    int                  `json:"instance_quantity"`
	Name                string               `json:"name"`
	LaunchSpecification *launchSpecification `json:"launch_specification"`
}

type createBidResponse struct {
	FID    string `json:"fid"`
	Status string `json:"status"`
	Name   string `json:"name"`
}

type listBidsResponse struct {
	Data       []spotBid `json:"data"`
	NextCursor *string   `json:"next_cursor"`
}

func NewMithrilPoolBackend(cfg *proto.AutoscalerBackendMithrilSpotBid) broker.ResourcePoolBackend {
	apiEndpoint := cfg.GetApiEndpoint()
	if apiEndpoint == "" {
		apiEndpoint = defaultAPIEndpoint
	}

	// Calculate prefix hash from configuration parameters
	prefix := calculateBidPrefix(cfg)

	backend := &mithrilPoolBackend{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBaseURL: fmt.Sprintf("%s/%s", apiEndpoint, apiVersion),
		bidPrefix:  prefix,
	}

	// Use AsyncManager's suspend/resume if MaxSuspendedBids is configured
	if cfg.MaxSuspendedBids > 0 {
		return backends.MakeAsyncResumable(backend, int(cfg.MaxSuspendedBids))
	}
	return backends.MakeAsync(backend)
}

// GenerateWorkerName implements SyncBackend interface
func (m *mithrilPoolBackend) GenerateWorkerName() string {
	bidName := fmt.Sprintf("%s-%s", m.bidPrefix, utils.RandString(5))
	return bidName + "-1"
}

// calculateBidPrefix generates a hash-based prefix from bid configuration
func calculateBidPrefix(cfg *proto.AutoscalerBackendMithrilSpotBid) string {
	// Hash parameters that define bid compatibility
	h := sha256.New()
	h.Write([]byte(cfg.GetInstanceType()))
	h.Write([]byte(cfg.GetRegion()))
	h.Write([]byte(cfg.GetProjectId()))
	h.Write([]byte(fmt.Sprintf("%.2f", cfg.GetLimitPrice())))

	// Include SSH keys and agent version in hash
	for _, key := range cfg.GetSshKeyIds() {
		h.Write([]byte(key))
	}
	if cfg.GetAgentVersionOverride() != "" {
		h.Write([]byte(cfg.GetAgentVersionOverride()))
	}

	hash := hex.EncodeToString(h.Sum(nil))
	// Use first 8 characters of hash as prefix
	return "velda-" + hash[:8]
}

func (m *mithrilPoolBackend) makeRequest(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s%s", m.apiBaseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication header
	apiToken := m.cfg.GetApiToken()
	if apiToken == "" {
		apiToken = os.Getenv("MITHRIL_API_TOKEN")
	}
	if apiToken == "" {
		return nil, fmt.Errorf("API token not configured")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// CreateWorker implements SyncBackend interface
func (m *mithrilPoolBackend) CreateWorker(ctx context.Context, name string) (backends.WorkerInfo, error) {
	// Extract the base name (remove -1 suffix) for bid name
	bidName := name[:len(name)-2]
	bid, err := m.createSpotBid(ctx, bidName)
	if err != nil {
		return backends.WorkerInfo{}, err
	}
	return backends.WorkerInfo{
		State: backends.WorkerStateActive,
		Data:  bid,
	}, nil
}

func (m *mithrilPoolBackend) createSpotBid(ctx context.Context, bidName string) (*spotBid, error) {
	// Prepare bash startup script
	agentConfig := m.cfg.AgentConfigContent
	version := m.cfg.AgentVersionOverride
	if version == "" {
		version = velda.Version
	}

	// Create bash script for instance initialization
	startupScriptParts := []string{`#!/bin/bash
set -e

apt update && apt install -y curl nfs-common

# Create velda config directory and write agent config
mkdir -p /etc/velda
cat << 'VELDA_CONFIG_EOF' > /etc/velda/agent.yaml
` + agentConfig + `
VELDA_CONFIG_EOF

# Initialize nvidia device handles
nvidia-smi || true

# Download and run nvidia collection script
curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/nvidia-collect.sh -o /tmp/nvidia-collect.sh && bash /tmp/nvidia-collect.sh || true
`}

	// Add Tailscale setup if Tailscale config is provided
	if tc := m.cfg.GetTailscaleConfig(); tc != nil && tc.GetPreAuthKey() != "" {
		tailscaleSetup := fmt.Sprintf(`
# Install and configure Tailscale
if ! command -v tailscale &> /dev/null; then
	curl -fsSL https://tailscale.com/install.sh | sh
fi

# Authenticate with Tailscale

tailscale up --login-server=%s --authkey=%s --accept-routes
`, tc.GetServer(), tc.GetPreAuthKey())
		startupScriptParts = append(startupScriptParts, tailscaleSetup)
	}

	// Add Velda agent installation
	veldaSetup := fmt.Sprintf(`
# Install velda agent if not present or version mismatch
if [ "$(/bin/velda version 2>/dev/null || true)" != "%s" ]; then
    curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/velda-%s-linux-amd64 -o /tmp/velda
    chmod +x /tmp/velda
    mv /tmp/velda /bin/velda
fi

# Setup and start velda agent service
if [ ! -e /usr/lib/systemd/system/velda-agent.service ]; then
    curl -fsSL https://velda-release.s3.us-west-1.amazonaws.com/velda-agent.service -o /usr/lib/systemd/system/velda-agent.service
    systemctl daemon-reload
    systemctl enable velda-agent.service
    systemctl start velda-agent.service &
fi
`, version, version)
	startupScriptParts = append(startupScriptParts, veldaSetup)

	startupScript := strings.Join(startupScriptParts, "")

	// Create the spot bid request
	reqBody := createBidRequest{
		Project:          m.cfg.GetProjectId(),
		Region:           m.cfg.GetRegion(),
		InstanceType:     m.cfg.GetInstanceType(),
		LimitPrice:       fmt.Sprintf("$%.2f", m.cfg.GetLimitPrice()),
		InstanceQuantity: 1, // Create one instance at a time
		Name:             bidName,
		LaunchSpecification: &launchSpecification{
			Volumes:       []string{},
			SSHKeys:       m.cfg.GetSshKeyIds(),
			StartupScript: startupScript,
			MemoryGB:      int(m.cfg.GetMemoryGb()),
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("Creating Mithril spot bid: %s (instance_type: %s, region: %s, limit_price: %s)",
		bidName, reqBody.InstanceType, reqBody.Region, reqBody.LimitPrice)

	resp, err := m.makeRequest(ctx, "POST", "/spot/bids", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create spot bid: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createResp createBidResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Printf("Created Mithril spot bid %s (fid: %s, status: %s)", bidName, createResp.FID, createResp.Status)

	// Return spotBid structure
	bid := &spotBid{
		FID:    createResp.FID,
		Name:   bidName,
		Status: createResp.Status,
	}
	return bid, nil
}

// DeleteWorker implements SyncBackend interface
func (m *mithrilPoolBackend) DeleteWorker(ctx context.Context, workerName string, workerInfo backends.WorkerInfo) error {
	return m.terminateBid(ctx, workerName, workerInfo)
}

func (m *mithrilPoolBackend) terminateBid(ctx context.Context, bidName string, workerInfo backends.WorkerInfo) error {
	var bidFID string
	if b, ok := workerInfo.Data.(*spotBid); ok && b.Name+"-1" == bidName {
		bidFID = b.FID
	} else {
		return fmt.Errorf("invalid worker info for bid %s: %v", bidName, workerInfo)
	}

	if bidFID == "" {
		return fmt.Errorf("bid not found: %s", bidName)
	}

	log.Printf("Terminating Mithril spot bid %s (fid: %s)", bidName, bidFID)

	resp, err := m.makeRequest(ctx, "DELETE", fmt.Sprintf("/spot/bids/%s", bidFID), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to terminate bid: status %d, body: %s", resp.StatusCode, string(body))
	}

	log.Printf("Terminated Mithril spot bid %s", bidName)
	return nil
}

// SuspendWorker implements Resumable interface
func (m *mithrilPoolBackend) SuspendWorker(ctx context.Context, name string, activeWorker backends.WorkerInfo) error {
	var bidFID string
	if b, ok := activeWorker.Data.(*spotBid); ok && b.Name+"-1" == name {
		bidFID = b.FID
	} else {
		return fmt.Errorf("invalid worker info for bid %s: %v", name, activeWorker)
	}

	if bidFID == "" {
		return fmt.Errorf("bid not found: %s", name)
	}

	log.Printf("Suspending Mithril spot bid %s (fid: %s) for reuse", name, bidFID)
	if err := m.pauseBid(ctx, bidFID); err != nil {
		return fmt.Errorf("failed to pause bid %s: %w", bidFID, err)
	}

	return nil
}

// ResumeWorker implements Resumable interface
func (m *mithrilPoolBackend) ResumeWorker(ctx context.Context, name string, suspendedWorker backends.WorkerInfo) error {
	var bid *spotBid
	if b, ok := suspendedWorker.Data.(*spotBid); ok && b.Name+"-1" == name {
		bid = b
	} else {
		return fmt.Errorf("invalid worker info for bid %s: %v", name, suspendedWorker)
	}

	log.Printf("Resuming suspended Mithril bid: %s (fid: %s)", bid.Name, bid.FID)
	if err := m.resumeBid(ctx, bid); err != nil {
		return fmt.Errorf("failed to resume bid %s: %w", bid.FID, err)
	}

	return nil
}

// Pauses a spot bid using PATCH API
func (m *mithrilPoolBackend) pauseBid(ctx context.Context, bidFID string) error {
	patchBody := map[string]interface{}{
		"paused": true,
	}

	bodyBytes, err := json.Marshal(patchBody)
	if err != nil {
		return fmt.Errorf("failed to marshal pause request: %w", err)
	}

	resp, err := m.makeRequest(ctx, "PATCH", fmt.Sprintf("/spot/bids/%s", bidFID), strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to pause bid: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// resumeBid resumes a paused spot bid using PATCH API
func (m *mithrilPoolBackend) resumeBid(ctx context.Context, bid *spotBid) error {
	bidFID := bid.FID
	patchBody := map[string]interface{}{
		"paused": false,
	}

	bodyBytes, err := json.Marshal(patchBody)
	if err != nil {
		return fmt.Errorf("failed to marshal resume request: %w", err)
	}

	resp, err := m.makeRequest(ctx, "PATCH", fmt.Sprintf("/spot/bids/%s", bidFID), strings.NewReader(string(bodyBytes)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to resume bid: status %d, body: %s", resp.StatusCode, string(body))
	}

	log.Printf("Successfully resumed bid %s", bidFID)
	return nil
}

// ListRemoteWorkers implements SyncBackend interface
func (m *mithrilPoolBackend) ListRemoteWorkers(ctx context.Context) (map[string]backends.WorkerInfo, error) {
	// List all spot bids
	path := fmt.Sprintf("/spot/bids?project=%s", m.cfg.GetProjectId())
	resp, err := m.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list bids: status %d, body: %s", resp.StatusCode, string(body))
	}

	var listResp listBidsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	workers := make(map[string]backends.WorkerInfo)
	for i := range listResp.Data {
		bid := &listResp.Data[i]

		// Filter by prefix - only process bids that match our configuration
		if !strings.HasPrefix(bid.Name, m.bidPrefix) {
			continue
		}

		// Skip terminated bids
		if bid.Status == "Terminated" {
			continue
		}

		// Add paused bids as suspended workers
		if bid.Status == "Paused" {
			workers[bid.Name+"-1"] = backends.WorkerInfo{
				State: backends.WorkerStateSuspended,
				Data:  bid,
			}
			continue
		}

		// Add allocated/open bids as active workers
		if bid.Status == "Allocated" || bid.Status == "Open" {
			workers[bid.Name+"-1"] = backends.WorkerInfo{
				State: backends.WorkerStateActive,
				Data:  bid,
			}
		}
	}

	return workers, nil
}

type mithrilSpotBidPoolFactory struct{}

func (f *mithrilSpotBidPoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_MithrilSpotBid:
		return true
	}
	return false
}

func (f *mithrilSpotBidPoolFactory) NewBackend(pool *proto.AgentPool, brokerInfo *agentpb.BrokerInfo) (broker.ResourcePoolBackend, error) {
	mithrilTemplate := pool.GetAutoScaler().GetBackend().GetMithrilSpotBid()

	if mithrilTemplate.AgentConfig != nil {
		mithrilTemplate = pb.Clone(mithrilTemplate).(*proto.AutoscalerBackendMithrilSpotBid)
		mithrilTemplate.AgentConfig.Pool = pool.GetName()
		if mithrilTemplate.AgentConfig.Broker == nil {
			if brokerInfo == nil {
				return nil, fmt.Errorf("no default broker info provided for pool %s", pool.GetName())
			}
			mithrilTemplate.AgentConfig.Broker = brokerInfo
		}
		var err error
		mithrilTemplate.AgentConfigContent, err = utils.ProtoToYaml(mithrilTemplate.AgentConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal agent config: %w", err)
		}
	}

	// Validate required fields
	if mithrilTemplate.GetInstanceType() == "" {
		return nil, fmt.Errorf("instance_type is required for Mithril backend")
	}
	if mithrilTemplate.GetRegion() == "" {
		return nil, fmt.Errorf("region is required for Mithril backend")
	}
	if mithrilTemplate.GetProjectId() == "" {
		return nil, fmt.Errorf("project_id is required for Mithril backend")
	}

	return NewMithrilPoolBackend(mithrilTemplate), nil
}

func init() {
	backends.Register(&mithrilSpotBidPoolFactory{})
}
