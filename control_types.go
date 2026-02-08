package driversdk

import (
	"sync"
)

// Control type constants
const (
	ControlTypeDimmer      = "dimmer"
	ControlTypeSwitch      = "switch"
	ControlTypeCCT         = "cct"
	ControlTypeColor       = "color"
	ControlTypeLevel       = "level"
	ControlTypePosition    = "position"
	ControlTypeTemperature = "temperature"
	ControlTypeHumidity    = "humidity"
	ControlTypeMotion      = "motion"
	ControlTypeContact     = "contact"
	ControlTypeText        = "text"
	ControlTypeSelect      = "select"
	ControlTypeButton      = "button"
)

// Control represents a UI control for the driver
type Control struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Min          float64  `json:"min,omitempty"`
	Max          float64  `json:"max,omitempty"`
	Step         float64  `json:"step,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Options      []string `json:"options,omitempty"` // For select type
	ReadOnly     bool     `json:"read_only,omitempty"`
	DefaultValue any      `json:"default_value,omitempty"`
}

// DimmerControl creates a dimmer control (0-100%)
func DimmerControl(id, name string) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeDimmer,
		Min:  0,
		Max:  100,
		Step: 1,
		Unit: "%",
	}
}

// SwitchControl creates a switch/power control
func SwitchControl(id, name string) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeSwitch,
	}
}

// CCTControl creates a CCT (correlated color temperature) control
func CCTControl(id, name string, minK, maxK float64) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeCCT,
		Min:  minK,
		Max:  maxK,
		Step: 100,
		Unit: "K",
	}
}

// ColorControl creates an RGB color control
func ColorControl(id, name string) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeColor,
	}
}

// LevelControl creates a level control with custom range
func LevelControl(id, name string, min, max, step float64, unit string) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeLevel,
		Min:  min,
		Max:  max,
		Step: step,
		Unit: unit,
	}
}

// PositionControl creates a position control (e.g., for blinds, 0-100%)
func PositionControl(id, name string) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypePosition,
		Min:  0,
		Max:  100,
		Step: 1,
		Unit: "%",
	}
}

// TemperatureControl creates a temperature control
func TemperatureControl(id, name string, min, max float64, unit string) Control {
	if unit == "" {
		unit = "°C"
	}
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeTemperature,
		Min:  min,
		Max:  max,
		Step: 0.5,
		Unit: unit,
	}
}

// SelectControl creates a select/dropdown control
func SelectControl(id, name string, options []string) Control {
	return Control{
		ID:      id,
		Name:    name,
		Type:    ControlTypeSelect,
		Options: options,
	}
}

// ButtonControl creates a button control
func ButtonControl(id, name string) Control {
	return Control{
		ID:   id,
		Name: name,
		Type: ControlTypeButton,
	}
}

// ControlState tracks the current state of controls
type ControlState struct {
	mu     sync.RWMutex
	values map[string]any
}

// NewControlState creates a new control state tracker
func NewControlState() *ControlState {
	return &ControlState{
		values: make(map[string]any),
	}
}

// Set sets a control value
func (cs *ControlState) Set(key string, value any) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.values[key] = value
}

// Get returns a control value
func (cs *ControlState) Get(key string) (any, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	v, ok := cs.values[key]
	return v, ok
}

// GetFloat returns a control value as float64
func (cs *ControlState) GetFloat(key string) (float64, bool) {
	v, ok := cs.Get(key)
	if !ok {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

// GetBool returns a control value as bool
func (cs *ControlState) GetBool(key string) (bool, bool) {
	v, ok := cs.Get(key)
	if !ok {
		return false, false
	}
	if b, ok := v.(bool); ok {
		return b, true
	}
	return false, false
}

// GetString returns a control value as string
func (cs *ControlState) GetString(key string) (string, bool) {
	v, ok := cs.Get(key)
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

// SetBrightness sets the brightness value (0-100)
func (cs *ControlState) SetBrightness(value float64) {
	cs.Set("brightness", clampValue(value, 0, 100))
}

// GetBrightness returns the brightness value
func (cs *ControlState) GetBrightness() float64 {
	v, _ := cs.GetFloat("brightness")
	return v
}

// SetPower sets the power state
func (cs *ControlState) SetPower(on bool) {
	cs.Set("power", on)
}

// IsPowerOn returns true if power is on
func (cs *ControlState) IsPowerOn() bool {
	v, _ := cs.GetBool("power")
	return v
}

// SetCCT sets the CCT value
func (cs *ControlState) SetCCT(value float64) {
	cs.Set("cct", value)
}

// GetCCT returns the CCT value
func (cs *ControlState) GetCCT() float64 {
	v, _ := cs.GetFloat("cct")
	return v
}

// SetColor sets RGB color values
func (cs *ControlState) SetColor(r, g, b int) {
	cs.Set("color_r", r)
	cs.Set("color_g", g)
	cs.Set("color_b", b)
}

// GetColor returns RGB color values
func (cs *ControlState) GetColor() (r, g, b int) {
	rv, _ := cs.Get("color_r")
	gv, _ := cs.Get("color_g")
	bv, _ := cs.Get("color_b")
	if ri, ok := rv.(int); ok {
		r = ri
	}
	if gi, ok := gv.(int); ok {
		g = gi
	}
	if bi, ok := bv.(int); ok {
		b = bi
	}
	return
}

// SetPosition sets the position value (0-100)
func (cs *ControlState) SetPosition(value float64) {
	cs.Set("position", clampValue(value, 0, 100))
}

// GetPosition returns the position value
func (cs *ControlState) GetPosition() float64 {
	v, _ := cs.GetFloat("position")
	return v
}

// ToMap returns all control values as a map
func (cs *ControlState) ToMap() map[string]any {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	result := make(map[string]any, len(cs.values))
	for k, v := range cs.values {
		result[k] = v
	}
	return result
}

// FromMap sets control values from a map
func (cs *ControlState) FromMap(m map[string]any) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for k, v := range m {
		cs.values[k] = v
	}
}

// clampValue clamps a value between min and max
func clampValue(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
