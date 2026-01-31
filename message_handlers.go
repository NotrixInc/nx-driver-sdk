package driversdk

import (
	"encoding/json"
)

// Common message payload types for driver-to-driver communication

// DimmingPayload is the standard payload for dimming messages
type DimmingPayload struct {
	EndpointKey       string  `json:"endpoint_key,omitempty"`
	SourceEndpointKey string  `json:"source_endpoint_key,omitempty"`
	TargetEndpointKey string  `json:"target_endpoint_key,omitempty"`
	Channel           int     `json:"channel,omitempty"`
	Value             float64 `json:"value,omitempty"`
	Brightness        float64 `json:"brightness,omitempty"`
	Power             bool    `json:"power,omitempty"`
	DeviceID          string  `json:"device_id,omitempty"`
	SourceDeviceID    string  `json:"source_device_id,omitempty"`
	TargetDeviceID    string  `json:"target_device_id,omitempty"`
}

// GetBrightness returns the brightness value, checking both Value and Brightness fields
func (p *DimmingPayload) GetBrightness() float64 {
	if p.Value != 0 {
		return p.Value
	}
	return p.Brightness
}

// PowerPayload is the standard payload for power control messages
type PowerPayload struct {
	Power          bool   `json:"power"`
	On             bool   `json:"on,omitempty"` // Alias for power
	DeviceID       string `json:"device_id,omitempty"`
	TargetDeviceID string `json:"target_device_id,omitempty"`
}

// IsPowerOn returns true if the power should be on
func (p *PowerPayload) IsPowerOn() bool {
	return p.Power || p.On
}

// ScenePayload is the standard payload for scene activation messages
type ScenePayload struct {
	SceneID   string         `json:"scene_id"`
	SceneName string         `json:"scene_name,omitempty"`
	Action    string         `json:"action,omitempty"` // "activate", "deactivate"
	Params    map[string]any `json:"params,omitempty"`
}

// CommandPayload is a generic command payload
type CommandPayload struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// StatusPayload is the standard payload for status update messages
type StatusPayload struct {
	Status    string         `json:"status"`
	DeviceID  string         `json:"device_id,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
	At        int64          `json:"at_unix_ms,omitempty"`
}

// ParsePayload is a helper to parse message payload into a typed struct
func ParsePayload[T any](payload json.RawMessage) (*T, error) {
	var data T
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	return &data, nil
}
