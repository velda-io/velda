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
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	pb "google.golang.org/protobuf/proto"

	"velda.io/velda"
	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	"velda.io/velda/pkg/broker/backends/sshconnector"
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
	basePrefix string // stable prefix configured by user
	bidPrefix  string // current version prefix under basePrefix

	sshConnector    *sshconnector.Connector
	sshUser         string
	sshReadyTimeout time.Duration
	sshPollInterval time.Duration

	initialReconnectMu sync.Once
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

type instanceModel struct {
	FID            string `json:"fid"`
	Name           string `json:"name"`
	Bid            string `json:"bid"`
	SshDestination string `json:"ssh_destination"`
	PrivateIP      string `json:"private_ip"`
	Status         string `json:"status"`
}

type listInstancesResponse struct {
	Data       []instanceModel `json:"data"`
	NextCursor *string         `json:"next_cursor"`
}

type currentPricingResponse struct {
	SpotPriceCents *int64 `json:"spot_price_cents"`
}

func NewMithrilPoolBackend(cfg *proto.AutoscalerBackendMithrilSpotBid) broker.ResourcePoolBackend {
	apiEndpoint := cfg.GetApiEndpoint()
	if apiEndpoint == "" {
		apiEndpoint = defaultAPIEndpoint
	}

	basePrefix := cfg.GetInstanceNamePrefix()
	if basePrefix == "" {
		basePrefix = "velda"
	}
	versionPrefix := calculateVersionPrefix(cfg)
	prefix := fmt.Sprintf("%s-%s", basePrefix, versionPrefix)

	backend := &mithrilPoolBackend{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBaseURL: fmt.Sprintf("%s/%s", apiEndpoint, apiVersion),
		basePrefix: basePrefix,
		bidPrefix:  prefix,
	}

	connector, defaults, err := sshconnector.NewDefault(
		cfg.GetAgentConfig(),
		cfg.GetAgentVersionOverride(),
	)
	if err != nil {
		log.Printf("Mithril SSH connector disabled due to invalid config: %v", err)
	} else if connector != nil {
		backend.sshConnector = connector
		backend.sshUser = defaults.SSHUser
		if backend.sshUser == "" {
			backend.sshUser = "ubuntu"
		}
		backend.sshReadyTimeout = defaults.ReadyTimeout
		if backend.sshReadyTimeout <= 0 {
			backend.sshReadyTimeout = 5 * time.Minute
		}
		backend.sshPollInterval = defaults.ReadyPollInterval
		if backend.sshPollInterval <= 0 {
			backend.sshPollInterval = 5 * time.Second
		}
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

// calculateVersionPrefix generates a hash-based version suffix from bid configuration.
func calculateVersionPrefix(cfg *proto.AutoscalerBackendMithrilSpotBid) string {
	cloned := pb.Clone(cfg).(*proto.AutoscalerBackendMithrilSpotBid)
	cloned.ApiToken = ""
	cloned.MaxSuspendedBids = 0
	cloned.AgentVersionOverride = ""

	options := pb.MarshalOptions{Deterministic: true}
	data, err := options.Marshal(cloned)
	if err != nil {
		panic(err)
	}

	version := cfg.GetAgentVersionOverride()
	if version == "" {
		version = velda.Version
	}

	h := sha256.New()
	h.Write(data)
	h.Write([]byte(version))
	hash := hex.EncodeToString(h.Sum(nil)[:16])
	return hash[:8]
}

func (m *mithrilPoolBackend) isManagedByBasePrefix(bidName string) bool {
	return strings.HasPrefix(bidName, m.basePrefix+"-")
}

func (m *mithrilPoolBackend) isCurrentVersionPrefix(bidName string) bool {
	return strings.HasPrefix(bidName, m.bidPrefix+"-")
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
	// Create the spot bid request
	reqBody := createBidRequest{
		Project:          m.cfg.GetProjectId(),
		Region:           m.cfg.GetRegion(),
		InstanceType:     m.cfg.GetInstanceType(),
		LimitPrice:       fmt.Sprintf("$%.2f", m.cfg.GetLimitPrice()),
		InstanceQuantity: 1, // Create one instance at a time
		Name:             bidName,
		LaunchSpecification: &launchSpecification{
			Volumes:  []string{},
			SSHKeys:  m.cfg.GetSshKeyIds(),
			MemoryGB: int(m.cfg.GetMemoryGb()),
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

	if err := m.bootstrapBid(ctx, bid); err != nil {
		log.Printf("Mithril bootstrap failed for bid %s (%s), terminating bid", bid.Name, bid.FID)
		_ = m.terminateBid(ctx, bid.Name+"-1", backends.WorkerInfo{Data: bid})
		return nil, err
	}
	return bid, nil
}

func (m *mithrilPoolBackend) bootstrapBid(ctx context.Context, bid *spotBid) error {
	if m.sshConnector == nil {
		return nil
	}
	if bid == nil || bid.FID == "" {
		return fmt.Errorf("invalid bid for bootstrap")
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, m.sshReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(m.sshPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, 120*time.Second)
		instances, err := m.getInstancesByBidFID(reqCtx, bid.FID)
		if err != nil {
			lastErr = err
		} else {
			for _, inst := range instances {
				host := strings.TrimSpace(inst.SshDestination)
				workerName := bid.Name + "-1"
				if err := m.sshConnector.Bootstrap(reqCtx, host, workerName, m.sshUser); err != nil {
					lastErr = err
					continue
				}
				log.Printf("Bootstrapped Mithril worker %s via SSH host %s", workerName, host)
				return nil
			}
			if len(instances) == 0 {
				lastErr = fmt.Errorf("waiting for instance allocation for bid %s", bid.FID)
			} else if lastErr == nil {
				lastErr = fmt.Errorf("no SSH host candidates found for bid %s", bid.FID)
			}
		}
		reqCancel()

		select {
		case <-bootstrapCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for SSH bootstrap of bid %s: %w", bid.FID, lastErr)
			}
			return fmt.Errorf("timed out waiting for SSH bootstrap of bid %s: %w", bid.FID, bootstrapCtx.Err())
		case <-ticker.C:
			log.Printf("Still waiting for SSH bootstrap of bid %s: %v", bid.FID, lastErr)
		}
	}
}

func (m *mithrilPoolBackend) getInstancesByBidFID(ctx context.Context, bidFID string) ([]instanceModel, error) {
	params := url.Values{}
	params.Set("project", m.cfg.GetProjectId())
	params.Set("bid_fid_in", bidFID)

	path := "/instances?" + params.Encode()
	resp, err := m.makeRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list instances while waiting for bootstrap: status %d, body: %s", resp.StatusCode, string(body))
	}

	var listResp listInstancesResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode instances list: %w", err)
	}

	instances := make([]instanceModel, 0, len(listResp.Data))
	for _, inst := range listResp.Data {
		if inst.Status != "STATUS_RUNNING" {
			return nil, fmt.Errorf("instance %s for bid %s is not running yet (status: %s)", inst.FID, bidFID, inst.Status)
		}
		instances = append(instances, inst)
	}

	return instances, nil
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
	if err := m.bootstrapBid(ctx, bid); err != nil {
		return fmt.Errorf("failed to rerun bootstrap after resume for bid %s: %w", bid.FID, err)
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
	activeBids := make([]*spotBid, 0)
	stalePausedBids := make([]*spotBid, 0)
	for i := range listResp.Data {
		bid := &listResp.Data[i]

		// Skip terminated bids
		if bid.Status == "Terminated" {
			continue
		}
		// Manage all bids under the same base prefix. Bids not matching the
		// current version prefix are considered stale and are only deleted once suspended.
		if !m.isManagedByBasePrefix(bid.Name) {
			continue
		}
		isStale := !m.isCurrentVersionPrefix(bid.Name)

		// Add paused bids as suspended workers
		if bid.Status == "Paused" {
			if isStale {
				stalePausedBids = append(stalePausedBids, bid)
				continue
			}
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
			activeBids = append(activeBids, bid)
		}
	}

	if len(stalePausedBids) > 0 {
		go func(stale []*spotBid) {
			cleanupCtx := context.WithoutCancel(ctx)
			for _, bid := range stale {
				if bid == nil || bid.FID == "" {
					continue
				}
				log.Printf("Deleting stale suspended Mithril bid %s (%s)", bid.Name, bid.FID)
				if err := m.terminateBid(cleanupCtx, bid.Name+"-1", backends.WorkerInfo{Data: bid}); err != nil {
					log.Printf("Failed to delete stale suspended Mithril bid %s (%s): %v", bid.Name, bid.FID, err)
				}
			}
		}(stalePausedBids)
	}

	m.bootstrapActiveWorkersOnFirstReconnect(ctx, activeBids)

	return workers, nil
}

func (m *mithrilPoolBackend) bootstrapActiveWorkersOnFirstReconnect(ctx context.Context, activeBids []*spotBid) {
	m.initialReconnectMu.Do(func() {
		if m.sshConnector == nil {
			return
		}

		for _, bid := range activeBids {
			if bid == nil || bid.FID == "" {
				continue
			}
			log.Printf("Re-running bootstrap for active Mithril worker on first reconnect: %s (%s)", bid.Name, bid.FID)
			go func(bid *spotBid) {
				if err := m.bootstrapBid(ctx, bid); err != nil {
					log.Printf("Failed bootstrap rerun on first reconnect for bid %s: %v", bid.FID, err)
				}
			}(bid)
		}
	})

}

// CheckPrice implements PricingBackend interface
// Returns the current hourly spot price in USD for this backend configuration.
func (m *mithrilPoolBackend) CheckPrice(ctx context.Context) (float64, error) {
	instanceType := strings.TrimSpace(m.cfg.GetInstanceType())
	if instanceType == "" {
		return 0, fmt.Errorf("instance_type is required for Mithril pricing")
	}

	params := url.Values{}
	params.Set("instance_type", instanceType)
	if region := strings.TrimSpace(m.cfg.GetRegion()); region != "" {
		params.Set("region", region)
	}
	params.Set("instance_quantity", "1")

	path := "/pricing/current?" + params.Encode()
	resp, err := m.makeRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch Mithril pricing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to fetch Mithril pricing: status %d, body: %s", resp.StatusCode, string(body))
	}

	var pricing currentPricingResponse
	if err := json.NewDecoder(resp.Body).Decode(&pricing); err != nil {
		return 0, fmt.Errorf("failed to decode Mithril pricing response: %w", err)
	}
	if pricing.SpotPriceCents == nil {
		return 0, fmt.Errorf("Mithril pricing response missing spot_price_cents")
	}
	if *pricing.SpotPriceCents < 0 {
		return 0, fmt.Errorf("invalid Mithril spot_price_cents: %d", *pricing.SpotPriceCents)
	}

	// If a limit price is configured and the current spot price exceeds it, return infinity to indicate
	// we should not use this backend.
	if m.cfg.GetLimitPrice() > 0 && float64(*pricing.SpotPriceCents)/100.0 >= m.cfg.GetLimitPrice() {
		return math.MaxFloat64, nil
	}
	return float64(*pricing.SpotPriceCents) / 100.0, nil
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
