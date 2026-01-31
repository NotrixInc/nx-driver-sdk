package driversdk

import "encoding/json"

// Standard filenames included in a driver package.
const (
	CapabilitiesFileName = "capabilities.json"
	EventsFileName       = "events.json"
)

// DriverCapabilitiesSpec describes what a driver can do.
//
// This file is intended for humans + tooling/UX. Keep it forward-compatible:
// - Prefer adding fields over changing existing meanings.
// - Use SchemaVersion for evolution.
type DriverCapabilitiesSpec struct {
	SchemaVersion int `json:"schema_version"`

	// Optional: declare the driver identity the spec belongs to.
	DriverID   string `json:"driver_id,omitempty"`
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	DriverType string `json:"driver_type,omitempty"` // DEVICE|HUB|CHILD|UI (see DriverType* constants in types.go)

	// Optional feature flags/tooling hints.
	Supports map[string]bool `json:"supports,omitempty"`
	Features map[string]any  `json:"features,omitempty"`

	// Optional: reference/summary of endpoints/variables/events (not authoritative).
	Summary map[string]any `json:"summary,omitempty"`

	// Free-form extension point.
	Meta map[string]any `json:"meta,omitempty"`
}

// DriverEventsSpec describes which events a device/driver may emit.
//
// The runtime event payloads that drivers publish (DeviceEvent) are not required
// to exactly match these schemas, but tooling/UI can use this to render and validate.
type DriverEventsSpec struct {
	SchemaVersion int                     `json:"schema_version"`
	Events        []DriverEventDefinition `json:"events"`
}

type DriverEventDefinition struct {
	Type          string          `json:"type"`                     // stable event type key
	Name          string          `json:"name,omitempty"`           // display name
	Description   string          `json:"description,omitempty"`    // human description
	Severity      string          `json:"severity,omitempty"`       // INFO|WARN|ERROR (optional)
	PayloadSchema json.RawMessage `json:"payload_schema,omitempty"` // JSON Schema (optional)
	Meta          map[string]any  `json:"meta,omitempty"`
}
