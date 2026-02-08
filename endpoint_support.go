package driversdk

import (
	"encoding/json"
	"sync"
)

// Additional endpoint connection types not in types.go
const (
	EndpointConnectionDCDimmer  EndpointConnection = "DC_Dimmer"
	EndpointConnectionAnalog    EndpointConnection = "Analog"
	EndpointConnectionDigital   EndpointConnection = "Digital"
	EndpointConnectionZWave     EndpointConnection = "ZWave"
	EndpointConnectionBluetooth EndpointConnection = "Bluetooth"
)

// EndpointType constants (aliases for EndpointKind)
const (
	EndpointTypeControl = "Control"
	EndpointTypeAudio   = "Audio"
	EndpointTypeVideo   = "Video"
	EndpointTypeData    = "Data"
)

// EndpointDef represents an endpoint definition for JSON loading
// This is separate from the Endpoint type in types.go to avoid conflicts
type EndpointDef struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Direction    string          `json:"direction"`               // Input, Output, BIDIR
	Type         string          `json:"type"`                    // Control, Audio, Video, Data
	Connection   string          `json:"connection,omitempty"`    // DC_Dimmer, Relay, HDMI, etc.
	MultiBinding bool            `json:"multi_binding,omitempty"` // Allow multiple bindings
	ValueSchema  json.RawMessage `json:"value_schema,omitempty"`  // JSON Schema for value validation
}

// ToEndpoint converts EndpointDef to the SDK Endpoint type
func (ed *EndpointDef) ToEndpoint() Endpoint {
	return Endpoint{
		Key:          ed.ID,
		Name:         ed.Name,
		Direction:    EndpointDirection(ed.Direction),
		Kind:         EndpointKind(ed.Type),
		Connection:   EndpointConnection(ed.Connection),
		MultiBinding: ed.MultiBinding,
		ValueSchema:  ed.ValueSchema,
	}
}

// EndpointValue represents a value for an endpoint
type EndpointValue struct {
	EndpointID string `json:"endpoint_id"`
	Value      any    `json:"value"`
	Source     string `json:"source,omitempty"` // DEVICE, USER, SCENE, BINDING, etc.
	Timestamp  int64  `json:"timestamp,omitempty"`
}

// EndpointHandler is called when an endpoint value changes
type EndpointHandler interface {
	OnEndpointValueChange(endpointID string, value any, source string) error
}

// EndpointHandlerFunc is a function adapter for EndpointHandler
type EndpointHandlerFunc func(endpointID string, value any, source string) error

func (f EndpointHandlerFunc) OnEndpointValueChange(endpointID string, value any, source string) error {
	return f(endpointID, value, source)
}

// EndpointRegistry manages endpoints for a driver
type EndpointRegistry struct {
	mu        sync.RWMutex
	endpoints map[string]*Endpoint
	values    map[string]any
	handlers  []EndpointHandler
}

// NewEndpointRegistry creates a new endpoint registry
func NewEndpointRegistry() *EndpointRegistry {
	return &EndpointRegistry{
		endpoints: make(map[string]*Endpoint),
		values:    make(map[string]any),
	}
}

// RegisterEndpoint adds an endpoint to the registry
func (r *EndpointRegistry) RegisterEndpoint(ep Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints[ep.Key] = &ep
}

// RegisterEndpoints adds multiple endpoints to the registry
func (r *EndpointRegistry) RegisterEndpoints(eps []Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range eps {
		r.endpoints[eps[i].Key] = &eps[i]
	}
}

// GetEndpoint returns an endpoint by key
func (r *EndpointRegistry) GetEndpoint(key string) *Endpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.endpoints[key]
}

// ListEndpoints returns all registered endpoints
func (r *EndpointRegistry) ListEndpoints() []Endpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Endpoint, 0, len(r.endpoints))
	for _, ep := range r.endpoints {
		result = append(result, *ep)
	}
	return result
}

// SetEndpointValue sets the current value for an endpoint
func (r *EndpointRegistry) SetEndpointValue(endpointID string, value any, source string) error {
	r.mu.Lock()
	r.values[endpointID] = value
	handlers := make([]EndpointHandler, len(r.handlers))
	copy(handlers, r.handlers)
	r.mu.Unlock()

	// Notify handlers
	for _, h := range handlers {
		if err := h.OnEndpointValueChange(endpointID, value, source); err != nil {
			return err
		}
	}
	return nil
}

// GetEndpointValue returns the current value for an endpoint
func (r *EndpointRegistry) GetEndpointValue(endpointID string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[endpointID]
	return v, ok
}

// AddHandler adds a handler for endpoint value changes
func (r *EndpointRegistry) AddHandler(h EndpointHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, h)
}

// AddHandlerFunc adds a function handler for endpoint value changes
func (r *EndpointRegistry) AddHandlerFunc(f EndpointHandlerFunc) {
	r.AddHandler(f)
}

// LoadEndpointsFromJSON loads endpoints from a JSON byte slice (e.g., from endpoints.json)
func LoadEndpointsFromJSON(data []byte) ([]Endpoint, error) {
	var container struct {
		Endpoints []Endpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(data, &container); err != nil {
		return nil, err
	}
	return container.Endpoints, nil
}

// NumberValueSchema creates a JSON schema for a number value with min/max
func NumberValueSchema(min, max float64) json.RawMessage {
	schema := map[string]any{
		"type":    "number",
		"minimum": min,
		"maximum": max,
	}
	data, _ := json.Marshal(schema)
	return data
}

// BooleanValueSchema creates a JSON schema for a boolean value
func BooleanValueSchema() json.RawMessage {
	schema := map[string]any{
		"type": "boolean",
	}
	data, _ := json.Marshal(schema)
	return data
}

// StringValueSchema creates a JSON schema for a string value
func StringValueSchema() json.RawMessage {
	schema := map[string]any{
		"type": "string",
	}
	data, _ := json.Marshal(schema)
	return data
}

// EnumValueSchema creates a JSON schema for an enum value
func EnumValueSchema(values []string) json.RawMessage {
	schema := map[string]any{
		"type": "string",
		"enum": values,
	}
	data, _ := json.Marshal(schema)
	return data
}
