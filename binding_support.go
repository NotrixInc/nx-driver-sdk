package driversdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Binding direction constants
const (
	BindingDirectionSource = "source" // This driver sends values
	BindingDirectionTarget = "target" // This driver receives values
)

// Binding type constants
const (
	BindingTypeEndpointToEndpoint = "ENDPOINT_TO_ENDPOINT"
	BindingTypeDriverToDriver     = "DRIVER_TO_DRIVER"
)

// Binding represents a binding between endpoints
type Binding struct {
	ID               string `json:"id"`
	BindingType      string `json:"binding_type"`
	Name             string `json:"name,omitempty"`
	Enabled          bool   `json:"enabled"`
	SourceDeviceID   string `json:"source_device_id,omitempty"`
	SourceEndpointID string `json:"source_endpoint_id,omitempty"`
	TargetDeviceID   string `json:"target_device_id,omitempty"`
	TargetEndpointID string `json:"target_endpoint_id,omitempty"`
	Bidirectional    bool   `json:"bidirectional,omitempty"`
}

// BindingTarget represents a target for value propagation
type BindingTarget struct {
	DeviceID   string `json:"device_id"`
	EndpointID string `json:"endpoint_id"`
}

// BindingHandler is called to propagate values through bindings
type BindingHandler interface {
	PropagateToTarget(ctx context.Context, target BindingTarget, value any) error
}

// BindingManager manages bindings for a driver
type BindingManager struct {
	mu              sync.RWMutex
	deviceID        string
	baseURL         string
	httpClient      *http.Client
	logger          Logger
	bindings        []Binding
	bindingsAt      time.Time
	bindingsByEp    map[string][]BindingTarget // endpoint_id -> targets
	handler         BindingHandler
	refreshInterval time.Duration
}

// BindingManagerConfig holds configuration for the binding manager
type BindingManagerConfig struct {
	DeviceID        string
	BaseURL         string
	HTTPClient      *http.Client
	Logger          Logger
	Handler         BindingHandler
	RefreshInterval time.Duration // How often to refresh bindings (default: 2s)
}

// NewBindingManager creates a new binding manager
func NewBindingManager(cfg BindingManagerConfig) *BindingManager {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	interval := cfg.RefreshInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}

	return &BindingManager{
		deviceID:        cfg.DeviceID,
		baseURL:         baseURL,
		httpClient:      httpClient,
		logger:          cfg.Logger,
		handler:         cfg.Handler,
		bindingsByEp:    make(map[string][]BindingTarget),
		refreshInterval: interval,
	}
}

// RefreshBindings fetches bindings from controller-core
func (bm *BindingManager) RefreshBindings(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/bindings?source_device_id=%s", bm.baseURL, bm.deviceID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := bm.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to fetch bindings: %s - %s", resp.Status, string(body))
	}

	var bindings []Binding
	if err := json.NewDecoder(resp.Body).Decode(&bindings); err != nil {
		return err
	}

	bm.mu.Lock()
	bm.bindings = bindings
	bm.bindingsAt = time.Now()

	// Build endpoint -> targets map
	bm.bindingsByEp = make(map[string][]BindingTarget)
	for _, b := range bindings {
		if !b.Enabled {
			continue
		}
		if b.SourceEndpointID != "" && b.TargetDeviceID != "" {
			target := BindingTarget{
				DeviceID:   b.TargetDeviceID,
				EndpointID: b.TargetEndpointID,
			}
			bm.bindingsByEp[b.SourceEndpointID] = append(bm.bindingsByEp[b.SourceEndpointID], target)
		}
	}
	bm.mu.Unlock()

	return nil
}

// GetTargetsForEndpoint returns all binding targets for an endpoint
func (bm *BindingManager) GetTargetsForEndpoint(endpointID string) []BindingTarget {
	bm.mu.RLock()
	stale := time.Since(bm.bindingsAt) > bm.refreshInterval
	bm.mu.RUnlock()

	if stale {
		_ = bm.RefreshBindings(context.Background())
	}

	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.bindingsByEp[endpointID]
}

// PropagateValue sends a value to all targets bound to an endpoint
func (bm *BindingManager) PropagateValue(ctx context.Context, endpointID string, value any) error {
	targets := bm.GetTargetsForEndpoint(endpointID)
	if len(targets) == 0 {
		return nil
	}

	if bm.handler == nil {
		if bm.logger != nil {
			bm.logger.Warn("no binding handler set, cannot propagate value")
		}
		return nil
	}

	var lastErr error
	for _, target := range targets {
		if err := bm.handler.PropagateToTarget(ctx, target, value); err != nil {
			if bm.logger != nil {
				bm.logger.Error("failed to propagate to target",
					"target_device", target.DeviceID,
					"target_endpoint", target.EndpointID,
					"error", err)
			}
			lastErr = err
		}
	}
	return lastErr
}

// GetAllBindings returns all cached bindings
func (bm *BindingManager) GetAllBindings() []Binding {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	result := make([]Binding, len(bm.bindings))
	copy(result, bm.bindings)
	return result
}

// IsSourceAuthorized checks if a source device is authorized to send to us
func (bm *BindingManager) IsSourceAuthorized(sourceDeviceID string) bool {
	bm.mu.RLock()
	stale := time.Since(bm.bindingsAt) > bm.refreshInterval
	bm.mu.RUnlock()

	if stale {
		_ = bm.RefreshBindings(context.Background())
	}

	bm.mu.RLock()
	defer bm.mu.RUnlock()

	sourceDeviceID = strings.TrimSpace(strings.ToLower(sourceDeviceID))
	for _, b := range bm.bindings {
		if !b.Enabled {
			continue
		}
		// Check if source is bound to us as target
		if strings.EqualFold(strings.TrimSpace(b.SourceDeviceID), sourceDeviceID) &&
			strings.EqualFold(strings.TrimSpace(b.TargetDeviceID), bm.deviceID) {
			return true
		}
		// Check bidirectional
		if b.Bidirectional {
			if strings.EqualFold(strings.TrimSpace(b.TargetDeviceID), sourceDeviceID) &&
				strings.EqualFold(strings.TrimSpace(b.SourceDeviceID), bm.deviceID) {
				return true
			}
		}
	}
	return false
}
