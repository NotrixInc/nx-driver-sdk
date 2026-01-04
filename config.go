package driversdk

import (
	"encoding/json"
	"fmt"
)

// JSONConfig is an opaque JSON blob provided by the host/controller for driver configuration.
// Drivers should Decode() it into a strongly typed struct.
type JSONConfig struct {
	raw json.RawMessage
}

// JsonConfig is kept for backwards compatibility with earlier templates.
type JsonConfig = JSONConfig

func NewJSONConfig(raw []byte) JSONConfig { return JSONConfig{raw: raw} }

func (c JSONConfig) Raw() []byte { return c.raw }

func (c JSONConfig) Decode(v any) error {
	if len(c.raw) == 0 {
		return fmt.Errorf("empty config")
	}
	return json.Unmarshal(c.raw, v)
}
