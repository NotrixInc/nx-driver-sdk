package driversdk

import (
	"context"
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
	HealthOK      HealthStatus = "OK"
	HealthDegraded HealthStatus = "DEGRADED"
	HealthDown    HealthStatus = "DOWN"
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

// Command sent from host/controller to driver
type Command struct {
	DeviceID       string          // core device UUID
	EndpointKey    string          // e.g. "power"
	CorrelationID  string
	Payload        json.RawMessage  // endpoint payload
	IssuedAt       time.Time
}

// CommandResult returned by driver (host may forward to caller)
type CommandResult struct {
	Success bool
	Message string
	Data    json.RawMessage
}

// Endpoint definition materialized into device_endpoint
type Endpoint struct {
	Key        string          // stable id: "power", "level"
	Name       string
	Direction  string          // INPUT|OUTPUT|BIDIR
	Type       string          // taxonomy: switch/dimmer/...
	ValueSchema json.RawMessage // JSON schema for payload/value
	Meta       map[string]string
}

// Variable definition materialized into device_variable
type Variable struct {
	Key      string // "temperature_c"
	Type     string // number/integer/boolean/string/json
	Unit     string // "C", "%", etc.
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
	DeviceID  string
	Type      string
	Payload   json.RawMessage
	Severity  EventSeverity
	At        time.Time
}

// Returned by hub drivers
type ChildCandidate struct {
	Protocol   Protocol
	ChildRef   string // stable within hub namespace: zigbee ieee addr, rs485 slave id, etc.
	Manufacturer string
	Model       string
	Firmware    string
	Fingerprint json.RawMessage // protocol-specific fingerprint (clusters/registers/etc.)
	Signal      map[string]json.RawMessage // optional rssi/lqi/etc.
}
