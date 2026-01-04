package driversdk

import (
	"context"
	"time"
)

// Driver talks to controller-core via Publisher.
type Publisher interface {
	UpsertDevice(ctx context.Context, d DeviceDescriptor) error
	UpsertEndpoints(ctx context.Context, deviceID string, eps []Endpoint) error
	UpsertVariables(ctx context.Context, deviceID string, vars []Variable) error

	PublishState(ctx context.Context, s StateUpdate) error
	PublishVariable(ctx context.Context, v VariableUpdate) error
	PublishEvent(ctx context.Context, e DeviceEvent) error
}

// Host-provided dependencies
type Dependencies struct {
	Publisher Publisher
	Logger    Logger
	Clock     Clock
}

type Logger interface {
	Debug(msg string, kv ...any)
	Info(msg string, kv ...any)
	Warn(msg string, kv ...any)
	Error(msg string, kv ...any)
}

type Clock interface {
	Now() time.Time
}

// Minimal device descriptor; host maps to proto/DB
type DeviceDescriptor struct {
	DeviceID           string // core UUID (if known) or empty for first upsert
	DriverID           string // driver ID string
	ExternalDeviceKey  string // stable key used by driver-host/controller (mac/ip/child_ref)
	DisplayName        string
	DeviceType         string
	Manufacturer       string
	Model              string
	Firmware           string
	IPAddress          string
	MACAddress         string

	ConnectionCategory string // DIRECT_IP | VIA_HUB
	Protocol           string // IP | ZIGBEE | IR | RS485 | ...

	ParentDeviceID     string // hub device UUID for children
	ExternalID         string // child_ref for VIA_HUB devices
	Meta               map[string]string
}
