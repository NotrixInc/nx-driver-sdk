package driversdk

import (
	"context"
	"encoding/json"
	"strings"
)

// Message type constants for scene-related messages
const (
	MessageTypeSceneApply      = "scene_apply"
	MessageTypeSceneDeactivate = "scene_deactivate"
)

// SceneApplyPayload is the payload for scene_apply messages from controller-core
type SceneApplyPayload struct {
	DeviceID   string   `json:"device_id"`
	SceneID    string   `json:"scene_id,omitempty"`
	SceneName  string   `json:"scene_name,omitempty"`
	Brightness *float64 `json:"brightness,omitempty"`
	CCT        *float64 `json:"cct,omitempty"`
	On         *bool    `json:"on,omitempty"`
	Power      *bool    `json:"power,omitempty"`

	// Additional fields for extensibility
	Variables map[string]any `json:"variables,omitempty"`
}

// IsPowerOn returns the power state from the payload
// Returns true by default if neither On nor Power is specified
func (p *SceneApplyPayload) IsPowerOn() bool {
	if p.On != nil {
		return *p.On
	}
	if p.Power != nil {
		return *p.Power
	}
	return true // Default to on if not specified
}

// GetBrightness returns the brightness value, defaulting to 100 if not specified
func (p *SceneApplyPayload) GetBrightness() float64 {
	if p.Brightness != nil {
		return *p.Brightness
	}
	return 100.0
}

// GetCCT returns the CCT value if specified, or nil
func (p *SceneApplyPayload) GetCCT() *float64 {
	return p.CCT
}

// SceneHandler is an interface for drivers that support scene operations
type SceneHandler interface {
	// HandleSceneApply is called when a scene should be applied to this driver
	// The implementation should update device variables and forward to bound hardware
	HandleSceneApply(ctx context.Context, payload SceneApplyPayload) error
}

// SceneSupport provides helper methods for scene handling in drivers
type SceneSupport struct {
	deviceID string
	logger   Logger
	handler  SceneHandler
}

// NewSceneSupport creates a new SceneSupport helper
func NewSceneSupport(deviceID string, logger Logger, handler SceneHandler) *SceneSupport {
	return &SceneSupport{
		deviceID: deviceID,
		logger:   logger,
		handler:  handler,
	}
}

// RegisterSceneHandlers registers scene message handlers on the message bus
// This should be called during driver initialization
func (s *SceneSupport) RegisterSceneHandlers(bus *MessageBus) {
	bus.RegisterHandlerFunc(MessageTypeSceneApply, func(ctx context.Context, msg BusMessage) error {
		return s.handleSceneApplyMessage(ctx, msg)
	})
}

// handleSceneApplyMessage processes scene_apply messages from controller-core
func (s *SceneSupport) handleSceneApplyMessage(ctx context.Context, msg BusMessage) error {
	// Only accept scene_apply from controller-core
	if msg.SourceDriver != "" && !strings.EqualFold(msg.SourceDriver, "controller-core") {
		if s.logger != nil {
			s.logger.Debug("scene_apply from non-controller source rejected", "source", msg.SourceDriver)
		}
		return nil
	}

	var payload SceneApplyPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		if s.logger != nil {
			s.logger.Error("failed to parse scene_apply payload", "error", err)
		}
		return err
	}

	// Check if this message is for our device
	if payload.DeviceID != "" && !strings.EqualFold(strings.TrimSpace(payload.DeviceID), strings.TrimSpace(s.deviceID)) {
		return nil
	}

	if s.logger != nil {
		s.logger.Info("applying scene",
			"scene_id", payload.SceneID,
			"scene_name", payload.SceneName,
			"device_id", payload.DeviceID,
			"brightness", payload.Brightness,
			"cct", payload.CCT,
			"on", payload.On,
		)
	}

	// Delegate to the driver's scene handler
	if s.handler != nil {
		return s.handler.HandleSceneApply(ctx, payload)
	}

	return nil
}

// ParseSceneApplyPayload is a helper to parse a scene_apply message payload
func ParseSceneApplyPayload(payload json.RawMessage) (*SceneApplyPayload, error) {
	var data SceneApplyPayload
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	return &data, nil
}
