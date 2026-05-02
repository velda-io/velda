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
package primeintellect

import (
	"bytes"
	"context"
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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "google.golang.org/protobuf/proto"

	"velda.io/velda/pkg/broker"
	"velda.io/velda/pkg/broker/backends"
	"velda.io/velda/pkg/broker/backends/sshconnector"
	agentpb "velda.io/velda/pkg/proto/agent"
	proto "velda.io/velda/pkg/proto/config"
	"velda.io/velda/pkg/utils"
)

const (
	defaultAPIEndpoint = "https://api.primeintellect.ai/api/v1"
	defaultImage       = "ubuntu_22_cuda_12"
)

// --- API request/response types ---

type resourceSpec struct {
	MinCount               *int     `json:"minCount"`
	DefaultCount           *int     `json:"defaultCount"`
	MaxCount               *int     `json:"maxCount"`
	PricePerUnit           *float64 `json:"pricePerUnit"`
	Step                   *int     `json:"step"`
	DefaultIncludedInPrice *bool    `json:"defaultIncludedInPrice"`
	AdditionalInfo         *string  `json:"additionalInfo"`
}

type priceInfo struct {
	OnDemand   float64 `json:"onDemand"`
	IsVariable *bool   `json:"isVariable"`
	Currency   string  `json:"currency"`
}

type gpuAvailabilityItem struct {
	CloudID     string        `json:"cloudId"`
	GpuType     string        `json:"gpuType"`
	Socket      string        `json:"socket"`
	Provider    string        `json:"provider"`
	Region      string        `json:"region"`
	DataCenter  string        `json:"dataCenter"`
	Country     string        `json:"country"`
	GpuCount    int           `json:"gpuCount"`
	GpuMemory   int           `json:"gpuMemory"`
	Disk        *resourceSpec `json:"disk"`
	Vcpu        *resourceSpec `json:"vcpu"`
	Memory      *resourceSpec `json:"memory"`
	StockStatus string        `json:"stockStatus"`
	Security    string        `json:"security"`
	Prices      priceInfo     `json:"prices"`
	Images      []string      `json:"images"`
	IsSpot      *bool         `json:"isSpot"`
	PrepaidTime *int          `json:"prepaidTime"`
}

type gpuAvailabilityResponse struct {
	Items      []gpuAvailabilityItem `json:"items"`
	TotalCount int                   `json:"totalCount"`
}

type podSpec struct {
	Name         string `json:"name"`
	CloudID      string `json:"cloudId"`
	GpuType      string `json:"gpuType"`
	Socket       string `json:"socket"`
	GpuCount     int    `json:"gpuCount"`
	Image        string `json:"image"`
	DataCenterID string `json:"dataCenterId,omitempty"`
	Country      string `json:"country,omitempty"`
	Security     string `json:"security,omitempty"`
	// Optional resource overrides; zero means use provider default.
	DiskSize int    `json:"diskSize,omitempty"`
	Vcpus    int    `json:"vcpus,omitempty"`
	Memory   int    `json:"memory,omitempty"`
	SshKeyId string `json:"sshKeyId,omitempty"`
}

type providerSpec struct {
	Type string `json:"type"`
}

type teamSpec struct {
	TeamID string `json:"teamId"`
}

type createPodRequest struct {
	Pod      podSpec      `json:"pod"`
	Provider providerSpec `json:"provider"`
	Team     *teamSpec    `json:"team,omitempty"`
}

type podObject struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Status             string  `json:"status"`
	InstallationStatus string  `json:"installationStatus"`
	SSHConnection      *string `json:"sshConnection"`
	IP                 *string `json:"ip"`
}

type listPodsResponse struct {
	TotalCount int         `json:"total_count"`
	Offset     int         `json:"offset"`
	Limit      int         `json:"limit"`
	Data       []podObject `json:"data"`
}

// --- Backend ---

type primeIntellectPoolBackend struct {
	cfg        *proto.AutoscalerBackendPrimeIntellectInstance
	httpClient *http.Client
	apiBaseURL string
	namePrefix string

	sshConnector *sshconnector.Connector
	sshUser      string

	initialListMu sync.Once
}

type providerOffer struct {
	CloudID      string
	GpuType      string
	Socket       string
	GpuCount     int
	Provider     string
	DataCenterID string
	Country      string
	Security     string
	Images       []string
	// TotalPrice is the estimated hourly cost including resolved resource costs.
	TotalPrice float64
	// Resolved resource values to pass in the create request (0 = use provider default).
	Vcpus    int
	MemoryGB int
	DiskGB   int
}

type instanceInfo struct {
	ID      string
	Name    string
	Status  string
	SSHUser string
	SSHHost string
}

func NewPrimeIntellectPoolBackend(cfg *proto.AutoscalerBackendPrimeIntellectInstance) broker.ResourcePoolBackend {
	endpoint := cfg.GetApiEndpoint()
	if endpoint == "" {
		endpoint = defaultAPIEndpoint
	}
	prefix := cfg.GetInstanceNamePrefix()
	if prefix == "" {
		prefix = "velda"
	}

	backend := &primeIntellectPoolBackend{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBaseURL: strings.TrimRight(endpoint, "/"),
		namePrefix: prefix,
	}

	connector, defaults, err := sshconnector.NewDefault(
		cfg.GetAgentConfig(),
		cfg.GetAgentVersionOverride(),
	)
	if err != nil {
		panic(err)
	} else if connector != nil {
		backend.sshConnector = connector
		backend.sshUser = defaults.SSHUser
		if backend.sshUser == "" {
			backend.sshUser = "root"
		}
	}

	return backends.MakeAsync(backend)
}

func (p *primeIntellectPoolBackend) GenerateWorkerName() string {
	return fmt.Sprintf("%s-%s", p.namePrefix, utils.RandString(5))
}

func (p *primeIntellectPoolBackend) CreateWorker(ctx context.Context, name string) (backends.WorkerInfo, error) {
	offer, err := p.searchCheapestOffer(ctx)
	if err != nil {
		return backends.WorkerInfo{}, err
	}
	instance, err := p.createInstance(ctx, name, offer)
	if err != nil {
		return backends.WorkerInfo{}, err
	}

	if err := p.bootstrapInstance(ctx, instance); err != nil {
		_ = p.deleteInstance(ctx, instance.ID)
		return backends.WorkerInfo{}, err
	}

	return backends.WorkerInfo{State: backends.WorkerStateActive, Data: instance.ID}, nil
}

func (p *primeIntellectPoolBackend) DeleteWorker(ctx context.Context, workerName string, workerInfo backends.WorkerInfo) error {
	instanceID, err := p.instanceIDFromWorker(ctx, workerName, workerInfo)
	if err != nil {
		return err
	}
	return p.deleteInstance(ctx, instanceID)
}

func (p *primeIntellectPoolBackend) ListRemoteWorkers(ctx context.Context) (map[string]backends.WorkerInfo, error) {
	resp, err := p.makeRequest(ctx, http.MethodGet, "/pods/", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list PrimeIntellect pods: status %d body: %s", resp.StatusCode, string(body))
	}

	var listResp listPodsResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode PrimeIntellect pods response: %w", err)
	}

	workers := make(map[string]backends.WorkerInfo)
	activeInstances := make([]*instanceInfo, 0, len(listResp.Data))
	for _, pod := range listResp.Data {
		if pod.ID == "" || pod.Name == "" {
			continue
		}
		if !strings.HasPrefix(pod.Name, p.namePrefix+"-") {
			continue
		}
		status := strings.ToUpper(pod.Status)
		if status == "TERMINATED" || status == "DELETED" {
			continue
		}
		workers[pod.Name] = backends.WorkerInfo{State: backends.WorkerStateActive, Data: pod.ID}
		activeInstances = append(activeInstances, &instanceInfo{ID: pod.ID, Name: pod.Name, Status: pod.Status})
	}
	p.bootstrapActiveWorkersOnFirstList(ctx, activeInstances)
	return workers, nil
}

func (p *primeIntellectPoolBackend) bootstrapActiveWorkersOnFirstList(ctx context.Context, activeInstances []*instanceInfo) {
	p.initialListMu.Do(func() {
		if p.sshConnector == nil {
			return
		}

		bootstrapCtx := context.WithoutCancel(ctx)
		for _, instance := range activeInstances {
			if instance == nil || instance.ID == "" {
				continue
			}
			log.Printf("Re-running bootstrap for active PrimeIntellect worker on first list: %s (%s)", instance.Name, instance.ID)
			go func(instance *instanceInfo) {
				if err := p.bootstrapInstance(bootstrapCtx, instance); err != nil {
					log.Printf("Failed bootstrap rerun on first list for PrimeIntellect instance %s: %v", instance.ID, err)
				}
			}(instance)
		}
	})
}

func (p *primeIntellectPoolBackend) searchCheapestOffer(ctx context.Context) (*providerOffer, error) {
	q := url.Values{}
	for k, v := range p.cfg.GetSearchCriteria() {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		q.Add(k, v)
	}

	path := "/availability/gpus"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := p.makeRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to search PrimeIntellect offers: status %d body: %s", resp.StatusCode, string(body))
	}

	var availResp gpuAvailabilityResponse
	if err := json.NewDecoder(resp.Body).Decode(&availResp); err != nil {
		return nil, fmt.Errorf("failed to decode availability response: %w", err)
	}
	if len(availResp.Items) == 0 {
		return nil, status.Error(codes.ResourceExhausted, "No available offers")
	}

	minVcpus := int(p.cfg.GetMinVcpus())
	minMem := int(p.cfg.GetMinMemoryGb())
	minDisk := int(p.cfg.GetMinDiskGb())
	spotAllowed := p.cfg.GetSpotAllowed()

	var best *providerOffer
	bestPrice := math.MaxFloat64
	for _, item := range availResp.Items {
		if item.CloudID == "" || item.Provider == "" {
			continue
		}
		if item.Provider == "runpod" {
			// RunPod only offers container based solution, which is not compatible.
			continue
		}
		// Exclude spot instances unless explicitly allowed.
		if !spotAllowed && item.IsSpot != nil && *item.IsSpot {
			continue
		}
		// Exclude offers whose max resource capacity cannot meet our minimums.
		if !resourceCanSatisfy(item.Vcpu, minVcpus) {
			continue
		}
		if !resourceCanSatisfy(item.Memory, minMem) {
			continue
		}
		if !resourceCanSatisfy(item.Disk, minDisk) {
			continue
		}

		// Resolve effective resource values and compute total hourly price.
		vcpus, vcpuCost := resolveResource(item.Vcpu, minVcpus)
		memGB, memCost := resolveResource(item.Memory, minMem)
		diskGB, diskCost := resolveResource(item.Disk, minDisk)
		totalPrice := item.Prices.OnDemand + vcpuCost + memCost + diskCost

		if totalPrice < bestPrice {
			bestPrice = totalPrice
			best = &providerOffer{
				CloudID:      item.CloudID,
				GpuType:      item.GpuType,
				Socket:       item.Socket,
				GpuCount:     item.GpuCount,
				Provider:     item.Provider,
				DataCenterID: item.DataCenter,
				Country:      item.Country,
				Security:     item.Security,
				TotalPrice:   totalPrice,
				Images:       item.Images,
				Vcpus:        vcpus,
				MemoryGB:     memGB,
				DiskGB:       diskGB,
			}
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no PrimeIntellect offer satisfying resource requirements found")
	}
	log.Printf("PrimeIntellect selected cheapest offer cloudId=%s provider=%s totalPrice=%.4f/hr (vcpus=%d mem=%dGB disk=%dGB)",
		best.CloudID, best.Provider, best.TotalPrice, best.Vcpus, best.MemoryGB, best.DiskGB)
	return best, nil
}

// resourceCanSatisfy returns true if the offer spec's max can provide at least minCount units.
// If spec is nil or maxCount is nil we allow it (assume the provider can satisfy).
func resourceCanSatisfy(spec *resourceSpec, minCount int) bool {
	if minCount <= 0 || spec == nil || spec.MaxCount == nil {
		return true
	}
	return *spec.MaxCount >= minCount
}

// resolveResource returns the effective count to request and the additional hourly cost.
//
// Baseline is:
//   - defaultCount when defaultIncludedInPrice=true  (free to use default)
//   - minCount from spec otherwise                   (cheapest starting point)
//
// effective = max(requested minimum, baseline)
//
// Extra cost:
//   - 0 when defaultIncludedInPrice=true and effective == defaultCount
//   - effective * pricePerUnit otherwise (when pricePerUnit is set)
func resolveResource(spec *resourceSpec, minCount int) (effective int, cost float64) {
	if spec == nil {
		return 0, 0
	}
	var baseline int
	if spec.DefaultIncludedInPrice != nil && *spec.DefaultIncludedInPrice {
		if spec.DefaultCount != nil {
			baseline = *spec.DefaultCount
		}
	} else {
		if spec.MinCount != nil {
			baseline = *spec.MinCount
		}
	}
	step := 1
	effective = baseline
	if spec.Step != nil && *spec.Step > 0 {
		step = *spec.Step
		minCount = int(math.Ceil(float64(minCount)/float64(step))) * step // round up to step
	}
	if minCount > effective {
		effective = minCount
	}
	if spec.PricePerUnit == nil || *spec.PricePerUnit == 0 {
		return effective, 0
	}
	if spec.DefaultIncludedInPrice != nil && *spec.DefaultIncludedInPrice &&
		spec.DefaultCount != nil && effective == *spec.DefaultCount {
		return effective, 0 // using included default — no extra charge
	}
	return effective, float64(effective/step) * *spec.PricePerUnit
}

func selectImage(offer *providerOffer) string {
	for _, img := range offer.Images {
		if img == defaultImage {
			return img
		}
	}
	if len(offer.Images) > 0 {
		return offer.Images[0]
	}
	return defaultImage
}

func (p *primeIntellectPoolBackend) createInstance(ctx context.Context, name string, offer *providerOffer) (*instanceInfo, error) {
	req := createPodRequest{
		Pod: podSpec{
			Name:         name,
			CloudID:      offer.CloudID,
			GpuType:      offer.GpuType,
			Socket:       offer.Socket,
			GpuCount:     offer.GpuCount,
			Image:        selectImage(offer),
			DataCenterID: offer.DataCenterID,
			Country:      offer.Country,
			Security:     offer.Security,
			Vcpus:        offer.Vcpus,
			Memory:       offer.MemoryGB,
			DiskSize:     offer.DiskGB,
			SshKeyId:     p.cfg.GetSshKeyId(),
		},
		Provider: providerSpec{
			Type: offer.Provider,
		},
	}
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal create pod request: %w", err)
	}

	resp, err := p.makeRequest(ctx, http.MethodPost, "/pods/", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create PrimeIntellect pod: status %d body: %s", resp.StatusCode, string(body))
	}

	var pod podObject
	if err := json.NewDecoder(resp.Body).Decode(&pod); err != nil {
		return nil, fmt.Errorf("failed to decode created pod response: %w", err)
	}
	if pod.ID == "" {
		return nil, fmt.Errorf("PrimeIntellect create response did not contain pod id")
	}
	if pod.Name == "" {
		pod.Name = name
	}
	return &instanceInfo{
		ID:     pod.ID,
		Name:   pod.Name,
		Status: pod.Status,
	}, nil
}

func (p *primeIntellectPoolBackend) bootstrapInstance(ctx context.Context, instance *instanceInfo) error {
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, 120*time.Second)
		current, err := p.getInstance(reqCtx, instance.ID)
		if err != nil {
			lastErr = fmt.Errorf("failed to get instance status during bootstrap: %w", err)
		} else {
			host := strings.TrimSpace(current.SSHHost)
			if host != "" {
				sshUser := p.sshUser
				if strings.TrimSpace(current.SSHUser) != "" {
					sshUser = strings.TrimSpace(current.SSHUser)
				}
				workerName := current.Name
				if workerName == "" {
					workerName = instance.Name
				}
				if err := p.sshConnector.Bootstrap(reqCtx, host, workerName, sshUser); err == nil {
					return nil
				} else {
					lastErr = fmt.Errorf("failed to bootstrap instance via SSH: %w", err)
				}
			}
		}
		reqCancel()

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for PrimeIntellect instance bootstrap readiness: %w, lastErr: %v", waitCtx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (p *primeIntellectPoolBackend) getInstance(ctx context.Context, instanceID string) (*instanceInfo, error) {
	resp, err := p.makeRequest(ctx, http.MethodGet, "/pods/"+instanceID, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get PrimeIntellect pod %s: status %d body: %s", instanceID, resp.StatusCode, string(body))
	}

	var pod podObject
	if err := json.NewDecoder(resp.Body).Decode(&pod); err != nil {
		return nil, fmt.Errorf("failed to decode pod %s: %w", instanceID, err)
	}
	if pod.ID == "" {
		pod.ID = instanceID
	}
	inst := &instanceInfo{
		ID:     pod.ID,
		Name:   pod.Name,
		Status: pod.Status,
	}
	if pod.SSHConnection != nil && *pod.SSHConnection != "" {
		inst.SSHUser, inst.SSHHost = parseSSHConnection(*pod.SSHConnection)
	} else if pod.IP != nil && *pod.IP != "" {
		inst.SSHHost = *pod.IP
	}
	return inst, nil
}

func (p *primeIntellectPoolBackend) deleteInstance(ctx context.Context, instanceID string) error {
	resp, err := p.makeRequest(ctx, http.MethodDelete, "/pods/"+instanceID, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete PrimeIntellect pod %s: status %d body: %s", instanceID, resp.StatusCode, string(body))
	}
	return nil
}

func (p *primeIntellectPoolBackend) instanceIDFromWorker(_ context.Context, _ string, workerInfo backends.WorkerInfo) (string, error) {
	if id, ok := workerInfo.Data.(string); ok && id != "" {
		return id, nil
	}
	return "", fmt.Errorf("instance id missing from worker info")
}

func (p *primeIntellectPoolBackend) makeRequest(ctx context.Context, method, reqPath string, body io.Reader) (*http.Response, error) {
	target := p.apiBaseURL + reqPath
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	apiKey := p.cfg.GetApiKey()
	if apiKey == "" {
		apiKey = os.Getenv("PRIMEINTELLECT_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("PrimeIntellect API key not configured")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

// parseSSHConnection extracts username and host/IP from an SSH connection instruction.
// Supported forms include:
//   - "root@135.181.63.138 -p 22"
//   - "ssh root@135.181.63.138 -p 22"
//   - "root@135.181.63.138"
//
// Port is ignored here; caller assumes 22.
func parseSSHConnection(sshConn string) (string, string) {
	parts := strings.Fields(sshConn)
	if len(parts) == 0 {
		return "", ""
	}
	first := parts[0]
	if strings.EqualFold(first, "ssh") {
		if len(parts) < 2 {
			return "", ""
		}
		first = parts[1]
	}
	at := strings.SplitN(first, "@", 2)
	if len(at) == 2 {
		return at[0], at[1]
	}
	return "", first
}

// CheckPrice implements PricingBackend interface
// Returns the estimated hourly price in USD for the cheapest available offer
func (p *primeIntellectPoolBackend) CheckPrice(ctx context.Context) (float64, error) {
	offer, err := p.searchCheapestOffer(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get price from PrimeIntellect: %w", err)
	}
	return offer.TotalPrice, nil
}

type primeIntellectInstancePoolFactory struct{}

func (f *primeIntellectInstancePoolFactory) CanHandle(pb *proto.AutoscalerBackend) bool {
	switch pb.Backend.(type) {
	case *proto.AutoscalerBackend_PrimeintellectInstance:
		return true
	}
	return false
}

func (f *primeIntellectInstancePoolFactory) NewBackend(pool *proto.AgentPool, brokerInfo *agentpb.BrokerInfo) (broker.ResourcePoolBackend, error) {
	cfg := pool.GetAutoScaler().GetBackend().GetPrimeintellectInstance()

	if cfg.AgentConfig != nil {
		cfg = pb.Clone(cfg).(*proto.AutoscalerBackendPrimeIntellectInstance)
		cfg.AgentConfig.Pool = pool.GetName()
		if cfg.AgentConfig.Broker == nil {
			if brokerInfo == nil {
				return nil, fmt.Errorf("no default broker info provided for pool %s", pool.GetName())
			}
			cfg.AgentConfig.Broker = brokerInfo
		}
	}

	if cfg.GetInstanceNamePrefix() == "" {
		cfg.InstanceNamePrefix = fmt.Sprintf("velda-%s", pool.GetName())
	}

	return NewPrimeIntellectPoolBackend(cfg), nil
}

func init() {
	backends.Register(&primeIntellectInstancePoolFactory{})
}
