package driversdk

import (
	"strings"
)

// Connection status constants
const (
	ConnectionStatusOnline       = "online"
	ConnectionStatusOffline      = "offline"
	ConnectionStatusNotAddressed = "not_addressed"
	ConnectionStatusConnecting   = "connecting"
	ConnectionStatusError        = "error"
)

// Device category constants
const (
	DeviceCategoryLight      = "Light"
	DeviceCategorySwitch     = "Switch"
	DeviceCategorySensor     = "Sensor"
	DeviceCategoryThermostat = "Thermostat"
	DeviceCategoryBlind      = "Blind"
	DeviceCategoryLock       = "Lock"
	DeviceCategoryCamera     = "Camera"
	DeviceCategoryMedia      = "Media"
	DeviceCategoryHVAC       = "HVAC"
	DeviceCategoryOther      = "Other"
)

// DeviceMetadata contains common metadata for a device
type DeviceMetadata struct {
	// Connection info
	IPAddress  string `json:"ip_address,omitempty"`
	Port       int    `json:"port,omitempty"`
	MACAddress string `json:"mac_address,omitempty"`
	Hostname   string `json:"hostname,omitempty"`

	// Device info
	Manufacturer    string `json:"manufacturer,omitempty"`
	Model           string `json:"model,omitempty"`
	SerialNumber    string `json:"serial_number,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	HardwareVersion string `json:"hardware_version,omitempty"`

	// Status
	ConnectionStatus string `json:"connection_status,omitempty"`
	LastSeen         int64  `json:"last_seen,omitempty"` // Unix timestamp

	// Capabilities
	Channels     int             `json:"channels,omitempty"`
	Capabilities []string        `json:"capabilities,omitempty"`
	Features     map[string]bool `json:"features,omitempty"`

	// Extra metadata
	Extra map[string]any `json:"extra,omitempty"`
}

// NewDeviceMetadata creates a new device metadata instance
func NewDeviceMetadata() *DeviceMetadata {
	return &DeviceMetadata{
		ConnectionStatus: ConnectionStatusNotAddressed,
		Features:         make(map[string]bool),
		Extra:            make(map[string]any),
	}
}

// SetAddress sets the IP address and port
func (m *DeviceMetadata) SetAddress(ip string, port int) {
	m.IPAddress = ip
	m.Port = port
	if ip != "" {
		m.ConnectionStatus = ConnectionStatusConnecting
	}
}

// IsAddressed returns true if the device has an address
func (m *DeviceMetadata) IsAddressed() bool {
	return m.IPAddress != "" || m.Hostname != "" || m.MACAddress != ""
}

// SetOnline sets the connection status to online
func (m *DeviceMetadata) SetOnline() {
	m.ConnectionStatus = ConnectionStatusOnline
}

// SetOffline sets the connection status to offline
func (m *DeviceMetadata) SetOffline() {
	m.ConnectionStatus = ConnectionStatusOffline
}

// IsOnline returns true if the device is online
func (m *DeviceMetadata) IsOnline() bool {
	return m.ConnectionStatus == ConnectionStatusOnline
}

// HasCapability checks if the device has a specific capability
func (m *DeviceMetadata) HasCapability(cap string) bool {
	cap = strings.ToLower(strings.TrimSpace(cap))
	for _, c := range m.Capabilities {
		if strings.ToLower(strings.TrimSpace(c)) == cap {
			return true
		}
	}
	return false
}

// AddCapability adds a capability to the device
func (m *DeviceMetadata) AddCapability(cap string) {
	if !m.HasCapability(cap) {
		m.Capabilities = append(m.Capabilities, cap)
	}
}

// HasFeature checks if a feature is enabled
func (m *DeviceMetadata) HasFeature(feature string) bool {
	return m.Features[feature]
}

// SetFeature sets a feature enabled/disabled
func (m *DeviceMetadata) SetFeature(feature string, enabled bool) {
	if m.Features == nil {
		m.Features = make(map[string]bool)
	}
	m.Features[feature] = enabled
}

// ToMap converts metadata to a map for JSON serialization
func (m *DeviceMetadata) ToMap() map[string]any {
	result := make(map[string]any)

	if m.IPAddress != "" {
		result["ip_address"] = m.IPAddress
	}
	if m.Port > 0 {
		result["port"] = m.Port
	}
	if m.MACAddress != "" {
		result["mac_address"] = m.MACAddress
	}
	if m.Hostname != "" {
		result["hostname"] = m.Hostname
	}
	if m.Manufacturer != "" {
		result["manufacturer"] = m.Manufacturer
	}
	if m.Model != "" {
		result["model"] = m.Model
	}
	if m.SerialNumber != "" {
		result["serial_number"] = m.SerialNumber
	}
	if m.FirmwareVersion != "" {
		result["firmware_version"] = m.FirmwareVersion
	}
	if m.HardwareVersion != "" {
		result["hardware_version"] = m.HardwareVersion
	}
	if m.ConnectionStatus != "" {
		result["connection_status"] = m.ConnectionStatus
	}
	if m.LastSeen > 0 {
		result["last_seen"] = m.LastSeen
	}
	if m.Channels > 0 {
		result["channels"] = m.Channels
	}
	if len(m.Capabilities) > 0 {
		result["capabilities"] = m.Capabilities
	}
	if len(m.Features) > 0 {
		result["features"] = m.Features
	}
	for k, v := range m.Extra {
		result[k] = v
	}

	return result
}

// MetadataFromConfig extracts device metadata from a config map
func MetadataFromConfig(cfg map[string]any) *DeviceMetadata {
	m := NewDeviceMetadata()

	if v, ok := cfg["ip_address"].(string); ok {
		m.IPAddress = v
	}
	if v, ok := cfg["ip"].(string); ok {
		m.IPAddress = v
	}
	if v, ok := cfg["host"].(string); ok {
		if m.IPAddress == "" {
			m.IPAddress = v
		}
	}

	if v, ok := cfg["port"].(float64); ok {
		m.Port = int(v)
	}
	if v, ok := cfg["port"].(int); ok {
		m.Port = v
	}

	if v, ok := cfg["mac_address"].(string); ok {
		m.MACAddress = v
	}
	if v, ok := cfg["mac"].(string); ok {
		m.MACAddress = v
	}

	if v, ok := cfg["hostname"].(string); ok {
		m.Hostname = v
	}

	if v, ok := cfg["manufacturer"].(string); ok {
		m.Manufacturer = v
	}
	if v, ok := cfg["model"].(string); ok {
		m.Model = v
	}
	if v, ok := cfg["serial_number"].(string); ok {
		m.SerialNumber = v
	}
	if v, ok := cfg["firmware_version"].(string); ok {
		m.FirmwareVersion = v
	}
	if v, ok := cfg["hardware_version"].(string); ok {
		m.HardwareVersion = v
	}
	if v, ok := cfg["channels"].(float64); ok {
		m.Channels = int(v)
	}
	if v, ok := cfg["channels"].(int); ok {
		m.Channels = v
	}

	if m.IsAddressed() {
		m.ConnectionStatus = ConnectionStatusConnecting
	}

	return m
}

// MetadataProvider is implemented by drivers that provide metadata
type MetadataProvider interface {
	GetMetadata() *DeviceMetadata
}
