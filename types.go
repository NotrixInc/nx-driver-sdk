package driversdk

import (
	"encoding/json"
	"time"
)

type DriverType string

const (
	DriverTypeDevice DriverType = "DEVICE"
	DriverTypeHub    DriverType = "HUB"
	DriverTypeChild  DriverType = "CHILD"
)

type Topology string

const (
	TopologyDirectIP Topology = "DIRECT_IP"
	TopologyViaHub   Topology = "VIA_HUB"
)

type Protocol string

const (
	ProtocolIP     Protocol = "IP"
	ProtocolZigbee Protocol = "ZIGBEE"
	ProtocolIR     Protocol = "IR"
	ProtocolRS485  Protocol = "RS485"
	ProtocolModbus Protocol = "MODBUS"
	ProtocolOther  Protocol = "OTHER"
)

type HealthStatus string

const (
	HealthOK       HealthStatus = "OK"
	HealthDegraded HealthStatus = "DEGRADED"
	HealthDown     HealthStatus = "DOWN"
)

type Quality string

const (
	QualityGood      Quality = "GOOD"
	QualityStale     Quality = "STALE"
	QualityEstimated Quality = "ESTIMATED"
	QualityUnknown   Quality = "UNKNOWN"
)

type Source string

const (
	SourceDevice Source = "DEVICE"
	SourceDriver Source = "DRIVER"
	SourceUser   Source = "USER"
	SourceCloud  Source = "CLOUD"
)

// VariableType is the UX/data type expected by the controller/UI.
// Spec: Boolean, Number, Range, Text, Password, Image, Video
// Note: older drivers may still send values like "integer"; host/controller should normalize if needed.

type VariableType string

const (
	VariableTypeBoolean  VariableType = "Boolean"
	VariableTypeNumber   VariableType = "Number"
	VariableTypeRange    VariableType = "Range"
	VariableTypeText     VariableType = "Text"
	VariableTypePassword VariableType = "Password"
	VariableTypeImage    VariableType = "Image"
	VariableTypeVideo    VariableType = "Video"
)

type EndpointDirection string

const (
	EndpointDirectionInput  EndpointDirection = "Input"
	EndpointDirectionOutput EndpointDirection = "Output"
	// Backward-compat for existing templates/drivers.
	EndpointDirectionBidir EndpointDirection = "BIDIR"
)

type EndpointKind string

const (
	EndpointKindAudio   EndpointKind = "Audio"
	EndpointKindVideo   EndpointKind = "Video"
	EndpointKindControl EndpointKind = "Control"
)

type EndpointConnection string

const (
	EndpointConnectionHDMI           EndpointConnection = "HDMI"
	EndpointConnectionVGA            EndpointConnection = "VGA"
	EndpointConnectionComponent      EndpointConnection = "Component"
	EndpointConnectionComposite      EndpointConnection = "Composite"
	EndpointConnectionStereo         EndpointConnection = "Stereo"
	EndpointConnectionSpeaker        EndpointConnection = "Speaker"
	EndpointConnectionDigitalOptical EndpointConnection = "Digital_Optical"
	EndpointConnectionDigitalCoax    EndpointConnection = "Digital_Coax"
	EndpointConnectionIR             EndpointConnection = "IR"
	EndpointConnectionIP             EndpointConnection = "IP"
	EndpointConnectionZigBee         EndpointConnection = "ZigBee"
	EndpointConnectionRS485          EndpointConnection = "RS485"
	EndpointConnectionSerial         EndpointConnection = "Serial"
	EndpointConnectionRelay          EndpointConnection = "Relay"
)

// Command sent from host/controller to driver

type Command struct {
	DeviceID      string // core device UUID
	EndpointKey   string // endpoint key
	CorrelationID string
	Payload       json.RawMessage
	IssuedAt      time.Time
}

// CommandResult returned by driver (host may forward to caller)

type CommandResult struct {
	Success bool
	Message string
	Data    json.RawMessage
}

// Endpoint definition materialized into device_endpoint
// - Direction: Input/Output
// - Kind: Audio/Video/Control
// - Connection: HDMI/VGA/.../IP/ZigBee/etc.
// - Icon: controller/UI icon key or URL (connection-specific)
// - MultiBinding: whether multiple bindings are allowed

type Endpoint struct {
	Key          string // stable id within device
	Name         string
	Direction    EndpointDirection
	Kind         EndpointKind
	Connection   EndpointConnection
	Icon         string
	MultiBinding bool

	// ControlType is the control semantics for Control endpoints (e.g. "switch", "dimmer", "toggle").
	// Prefer this over Type.
	ControlType string
	// Type is kept for backward compatibility with older drivers.
	// Deprecated: use ControlType.
	Type        string
	ValueSchema json.RawMessage
	Meta        map[string]string
}

// NormalizeLegacyFields keeps legacy and new fields in sync.
// Call this before persisting/serializing endpoints if you want both fields populated.
func (e *Endpoint) NormalizeLegacyFields() {
	if e == nil {
		return
	}
	if e.ControlType == "" && e.Type != "" {
		e.ControlType = e.Type
	}
	if e.Type == "" && e.ControlType != "" {
		e.Type = e.ControlType
	}
}

// Variable definition materialized into device_variable
// - Readable/Writable describe whether the controller can read/write it.

type Variable struct {
	Key      string
	Type     VariableType
	Unit     string
	Readable bool
	Writable bool

	// Backward-compat: older code used ReadOnly.
	ReadOnly bool
	Meta     map[string]string
}

type VariableUpdate struct {
	DeviceID string
	Key      string
	Value    json.RawMessage
	Quality  Quality
	Source   Source
	At       time.Time
}

type StateUpdate struct {
	DeviceID string
	State    json.RawMessage
	At       time.Time
}

type EventSeverity string

const (
	EventInfo  EventSeverity = "INFO"
	EventWarn  EventSeverity = "WARN"
	EventError EventSeverity = "ERROR"
)

type DeviceEvent struct {
	DeviceID string
	Type     string
	Payload  json.RawMessage
	Severity EventSeverity
	At       time.Time
}

// Returned by hub drivers

type ChildCandidate struct {
	Protocol     Protocol
	ChildRef     string
	Manufacturer string
	Model        string
	Firmware     string
	Fingerprint  json.RawMessage
	Signal       map[string]json.RawMessage
}
