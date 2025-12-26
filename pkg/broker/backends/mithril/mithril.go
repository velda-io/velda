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
	defaultAPIEndpoint = "https://api.mithril.ai"
	apiVersion         = "v2"
)

type mithrilPoolBackend struct {
	cfg        *proto.AutoscalerBackendMithrilSpotBid
	httpClient *http.Client
	apiBaseURL string
	bidPrefix  string // prefix for bid names based on config hash

	bidMu           sync.RWMutex
	activeBids      map[string]*spotBid      // fid -> bid info
	creatingBids    map[string]chan struct{} // bid names being created
	terminatingBids map[string]struct{}      // bid names being terminated

	// Cache for suspended bids
	suspendedBidPoolMu sync.RWMutex
	suspendedBidPool   map[string]*spotBid // fid -> paused bid info
	lastScannedBids    map[string]*spotBid // track bids from last scan

	lastOp chan struct{}
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
	SSHKeys           []string `json:"ssh_keys,omitempty"`
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

	return &mithrilPoolBackend{
		cfg:              cfg,
		httpClient:       &http.Client{Timeout: 30 * time.Second},
		apiBaseURL:       fmt.Sprintf("%s/%s", apiEndpoint, apiVersion),
		bidPrefix:        prefix,
		activeBids:       make(map[string]*spotBid),
		creatingBids:     make(map[string]chan struct{}),
		terminatingBids:  make(map[string]struct{}),
		suspendedBidPool: make(map[string]*spotBid),
		lastScannedBids:  make(map[string]*spotBid),
	}
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

// getAvailableSuspendedBid retrieves and removes a suspended bid from the pool
func (m *mithrilPoolBackend) getAvailableSuspendedBid() *spotBid {
	m.suspendedBidPoolMu.Lock()
	defer m.suspendedBidPoolMu.Unlock()

	if len(m.suspendedBidPool) == 0 {
		return nil
	}

	// Get the first available suspended bid
	for _, bid := range m.suspendedBidPool {
		delete(m.suspendedBidPool, bid.FID)
		return bid
	}
	return nil
}

// getSuspendedBidPoolCount returns the current count of suspended bids in the pool
func (m *mithrilPoolBackend) getSuspendedBidPoolCount() int {
	m.suspendedBidPoolMu.RLock()
	defer m.suspendedBidPoolMu.RUnlock()
	return len(m.suspendedBidPool)
}

func (m *mithrilPoolBackend) RequestScaleUp(ctx context.Context) (string, error) {
	// Try to resume a suspended bid first
	suspendedBid := m.getAvailableSuspendedBid()
	if suspendedBid != nil {
		log.Printf("Resuming suspended Mithril bid: %s (fid: %s)", suspendedBid.Name, suspendedBid.FID)
		go func() {
			if err := m.resumeBid(ctx, suspendedBid); err != nil {
				log.Printf("Failed to resume bid %s: %v", suspendedBid.FID, err)
			}
		}()
		return suspendedBid.Name + "-1", nil
	}

	// Generate a unique bid name with prefix
	bidName := fmt.Sprintf("%s-%s", m.bidPrefix, utils.RandString(5))

	m.bidMu.Lock()
	op := make(chan struct{})
	m.lastOp = op
	m.creatingBids[bidName] = op
	m.bidMu.Unlock()

	go func() {
		defer func() {
			m.bidMu.Lock()
			delete(m.creatingBids, bidName)
			m.bidMu.Unlock()
			close(op)
		}()
		_, err := m.createSpotBid(ctx, bidName)
		if err != nil {
			log.Printf("Failed to create spot bid %s: %v", bidName, err)
		}
	}()

	// The name is always suffixed with a sequence number. Since we create one at a time, use "-1".
	return bidName + "-1", nil
}

func (m *mithrilPoolBackend) createSpotBid(ctx context.Context, bidName string) (string, error) {
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
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("Creating Mithril spot bid: %s (instance_type: %s, region: %s, limit_price: %s)",
		bidName, reqBody.InstanceType, reqBody.Region, reqBody.LimitPrice)

	resp, err := m.makeRequest(ctx, "POST", "/spot/bids", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create spot bid: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createResp createBidResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	log.Printf("Created Mithril spot bid %s (fid: %s, status: %s)", bidName, createResp.FID, createResp.Status)
	m.bidMu.Lock()
	defer m.bidMu.Unlock()
	m.activeBids[createResp.FID] = &spotBid{
		FID:    createResp.FID,
		Name:   bidName,
		Status: createResp.Status,
	}

	return createResp.FID, nil
}

func (m *mithrilPoolBackend) RequestDelete(ctx context.Context, workerName string) error {
	var creatingOp chan struct{}
	m.bidMu.Lock()
	creatingOp = m.creatingBids[workerName[:len(workerName)-2]]
	m.terminatingBids[workerName] = struct{}{}
	op := make(chan struct{})
	m.lastOp = op
	m.bidMu.Unlock()

	go func() {
		defer func() {
			m.bidMu.Lock()
			delete(m.terminatingBids, workerName)
			m.bidMu.Unlock()
			close(op)
		}()
		if creatingOp != nil {
			<-creatingOp
		}
		err := m.terminateBid(ctx, workerName)
		if err != nil {
			log.Printf("Failed to terminate bid %s: %v", workerName, err)
		}
	}()

	return nil
}

func (m *mithrilPoolBackend) terminateBid(ctx context.Context, bidName string) error {
	// Find the bid FID from the bid name
	m.bidMu.RLock()
	var bidFID string
	var bid *spotBid
	for fid, b := range m.activeBids {
		if b.Name+"-1" == bidName {
			bidFID = fid
			bid = b
			break
		}
	}
	m.bidMu.RUnlock()

	if bidFID == "" {
		return fmt.Errorf("bid not found: %s", bidName)
	}

	// Try to pause the bid if we have room in the pool
	if m.cfg.MaxSuspendedBids > 0 && int32(m.getSuspendedBidPoolCount()) < m.cfg.MaxSuspendedBids {
		log.Printf("Pausing Mithril spot bid %s (fid: %s) for reuse", bidName, bidFID)
		if err := m.pauseBid(ctx, bidFID); err != nil {
			log.Printf("Failed to pause bid %s, will terminate instead: %v", bidFID, err)
		} else {
			// Add to suspended pool
			m.suspendedBidPoolMu.Lock()
			m.suspendedBidPool[bidFID] = bid
			m.suspendedBidPoolMu.Unlock()
			return nil
		}
	}

	// Either pooling is disabled or pool is full, terminate the bid
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

// pauseBid pauses a spot bid using PATCH API
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

	m.bidMu.Lock()
	defer m.bidMu.Unlock()
	m.activeBids[bidFID] = bid

	log.Printf("Successfully resumed bid %s", bidFID)
	return nil
}

func (m *mithrilPoolBackend) ListWorkers(ctx context.Context) ([]broker.WorkerStatus, error) {
	seen := make(map[string]struct{})
	var res []broker.WorkerStatus

	// Add creating bids
	m.bidMu.RLock()
	for creatingBid := range m.creatingBids {
		res = append(res, broker.WorkerStatus{Name: creatingBid + "-1"})
		seen[creatingBid] = struct{}{}
	}
	for terminatingBid := range m.terminatingBids {
		seen[terminatingBid] = struct{}{}
	}
	m.bidMu.RUnlock()

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

	// Update active bids cache and collect workers
	m.bidMu.Lock()
	defer m.bidMu.Unlock()

	m.activeBids = make(map[string]*spotBid)
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

		// Handle paused bids separately
		if bid.Status == "Paused" {
			// Track paused bids for pool management
			continue
		}

		m.activeBids[bid.FID] = bid

		// Skip if already seen
		if _, exists := seen[bid.Name]; exists {
			continue
		}

		// Add allocated/open bids as workers
		if bid.Status == "Allocated" || bid.Status == "Open" {
			res = append(res, broker.WorkerStatus{Name: bid.Name + "-1"})
			seen[bid.Name] = struct{}{}
		}
	}

	// Scan for paused bids to populate the suspended bid pool
	if m.cfg.MaxSuspendedBids > 0 {
		go m.scanAndUpdateSuspendedBidPool(ctx)
	}

	return res, nil
}

// scanAndUpdateSuspendedBidPool scans for paused bids and updates the suspended bid pool
func (m *mithrilPoolBackend) scanAndUpdateSuspendedBidPool(ctx context.Context) {
	bidsFound := make(map[string]*spotBid)
	bidsToTerminate := make([]string, 0)

	// List all spot bids
	path := fmt.Sprintf("/spot/bids?project=%s&status=Paused", m.cfg.GetProjectId())
	resp, err := m.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		log.Printf("Failed to list paused bids: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to list paused bids: status %d", resp.StatusCode)
		return
	}

	var listResp listBidsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		log.Printf("Failed to decode paused bids response: %v", err)
		return
	}

	// Find paused bids matching our prefix
	for i := range listResp.Data {
		bid := &listResp.Data[i]

		// Filter by prefix - only consider bids from this configuration
		if !strings.HasPrefix(bid.Name, m.bidPrefix) {
			continue
		}

		// Only consider paused bids
		if bid.Status != "Paused" {
			continue
		}

		bidsFound[bid.FID] = bid
	}

	// Update suspended bid pool
	m.suspendedBidPoolMu.Lock()
	defer m.suspendedBidPoolMu.Unlock()

	// Add new found paused bids to the pool
	for fid, bid := range bidsFound {
		_, inPool := m.suspendedBidPool[fid]
		if inPool {
			continue
		}
		_, existInLastScan := m.lastScannedBids[fid]
		if existInLastScan {
			// Was previously scanned, may already be used
			continue
		}
		if len(m.suspendedBidPool) >= int(m.cfg.MaxSuspendedBids) {
			log.Printf("Max suspended bid pool size reached (%d), terminating bid %s", m.cfg.MaxSuspendedBids, fid)
			bidsToTerminate = append(bidsToTerminate, fid)
			continue
		}
		log.Printf("Adding paused bid %s (%s) to suspended pool", bid.Name, fid)
		m.suspendedBidPool[fid] = bid
	}

	// Remove bids from pool that are no longer paused
	for fid := range m.suspendedBidPool {
		_, found := bidsFound[fid]
		if !found {
			log.Printf("Removing bid %s from suspended pool (no longer paused)", fid)
			delete(m.suspendedBidPool, fid)
		}
	}

	m.lastScannedBids = bidsFound

	// Terminate excess bids outside the lock
	if len(bidsToTerminate) > 0 {
		go func() {
			for _, bidFID := range bidsToTerminate {
				resp, err := m.makeRequest(ctx, "DELETE", fmt.Sprintf("/spot/bids/%s", bidFID), nil)
				if err != nil {
					log.Printf("Failed to terminate excess bid %s: %v", bidFID, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
					log.Printf("Failed to terminate excess bid %s: status %d", bidFID, resp.StatusCode)
				}
			}
		}()
	}
}

func (m *mithrilPoolBackend) WaitForLastOperation(ctx context.Context) error {
	if m.lastOp != nil {
		<-m.lastOp
	}
	return nil
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
