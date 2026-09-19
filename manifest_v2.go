package driversdk

import "encoding/json"

// The version 2 driver manifest, as a Go value a driver can build and emit.
//
// This mirrors packages/capability-catalog/driver.schema.json in the controller.
// The two are deliberately separate types across a trust boundary — the
// controller must validate a stranger's package, not share structs with it — and
// the schema is the contract that keeps them honest.
//
// A driver renders one of these rather than hand-writing JSON, for the same
// reason these packages already render endpoints.json from Endpoints(): a
// declaration maintained by hand beside the code it describes drifts from it,
// and the ISAPI camera package is shipping proof — manifest.json says 0.1.5 and
// capabilities.json says 0.1.0.

// ManifestSchemaVersion is what marks a declaration as version 2.
const ManifestSchemaVersion = 2

// Tiers. A declarative driver carries wire templates instead of a binary.
const (
	TierCompiled    = "compiled"
	TierDeclarative = "declarative"
)

// UI primitives. The console renders these twelve and nothing else, so a
// capability that fits none of them is a finding about the set rather than a
// reason to invent a thirteenth.
const (
	ControlToggle      = "toggle"
	ControlSlider      = "slider"
	ControlButton      = "button"
	ControlButtonGroup = "button_group"
	ControlDpad        = "dpad"
	ControlSelect      = "select"
	ControlColorWheel  = "color_wheel"
	ControlReadout     = "readout"
	ControlGauge       = "gauge"
	ControlText        = "text"
	ControlIcon        = "icon"
	ControlMedia       = "media"
)

// Endpoint flow, declared rather than guessed. The console used to infer it from
// a regular expression over the driver id, so correctness depended on what
// somebody had named a package.
const (
	FlowIn    = "IN"
	FlowOut   = "OUT"
	FlowBidir = "BIDIR"
)

// Signal classes.
const (
	SignalAudio   = "Audio"
	SignalVideo   = "Video"
	SignalControl = "Control"
	SignalPower   = "Power"
	SignalData    = "Data"
)

// DriverManifest is the whole of driver.json.
type DriverManifest struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`

	Tier       string `json:"tier,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	Entrypoint any    `json:"entrypoint,omitempty"`

	PublisherClass PublisherClass `json:"publisher_class,omitempty"`

	Roles       Roles            `json:"roles"`
	DeviceModel *DeviceModel     `json:"device_model,omitempty"`
	Actions     []ManifestAction `json:"actions,omitempty"`
	Ingress     []IngressRule    `json:"ingress,omitempty"`

	// Carried over from version 1. device_types and requires_hub have no v2
	// replacement — roles say what the DRIVER is, these say what the DEVICE is
	// and which hub reaches it.
	// Category is which part of the house this belongs to — Light, Audio,
	// Video, Comfort, Security. The console groups and filters by it.
	Category string `json:"category,omitempty"`

	DeviceTypes  []string         `json:"device_types,omitempty"`
	Capabilities []string         `json:"capabilities,omitempty"`
	RequiresHub  []HubRequirement `json:"requires_hub,omitempty"`

	// DriverType is the deprecated single-valued field, emitted so one package
	// installs on a controller that predates roles as well as one that has them.
	// It must not contradict Roles; the controller refuses a manifest where it
	// does.
	DriverType DriverType `json:"driver_type,omitempty"`
}

type DeviceModel struct {
	Class        string               `json:"class,omitempty"`
	Capabilities []ManifestCapability `json:"capabilities,omitempty"`
}

// ManifestCapability declares state, commands, events, UI and endpoints
// together. Keeping them in one block is the point: as four separate files they
// could disagree, and the endpoint list and the control surface are the same
// fact written twice.
type ManifestCapability struct {
	ID             string             `json:"id"`
	Repeat         *Repeat            `json:"repeat,omitempty"`
	PublisherClass PublisherClass     `json:"publisher_class,omitempty"`
	State          *StateDef          `json:"state,omitempty"`
	Commands       []CommandDef       `json:"commands,omitempty"`
	Events         []ManifestEventDef `json:"events,omitempty"`
	UI             *UIDef             `json:"ui,omitempty"`
	Endpoints      []ManifestEndpoint `json:"endpoints,omitempty"`
	Wire           *WireDef           `json:"wire,omitempty"`
}

// Repeat expands one block into the copies it stands for, substituting {var}.
// Ten dimmer channels are one declaration rather than ten near-identical blocks
// that can drift apart.
type Repeat struct {
	Var  string `json:"var"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

type StateDef struct {
	Key        string   `json:"key"`
	Type       string   `json:"type"`
	Min        *float64 `json:"min,omitempty"`
	Max        *float64 `json:"max,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Retained   *bool    `json:"retained,omitempty"`
	Reportable *bool    `json:"reportable,omitempty"`
}

type CommandDef struct {
	ID         string          `json:"id"`
	Class      CommandClass    `json:"class,omitempty"`
	TimeoutMs  int             `json:"timeout_ms,omitempty"`
	Idempotent *bool           `json:"idempotent,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type ManifestEventDef struct {
	ID       string          `json:"id"`
	Severity string          `json:"severity,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type UIDef struct {
	Control     string `json:"control,omitempty"`
	Label       string `json:"label,omitempty"`
	Group       string `json:"group,omitempty"`
	OptionsFrom string `json:"options_from,omitempty"`
	ReadOnly    *bool  `json:"read_only,omitempty"`
}

// ManifestEndpoint is an endpoint as declared in the manifest. Distinct from
// Endpoint, which is what a running driver reports.
type ManifestEndpoint struct {
	Key           string `json:"key"`
	Name          string `json:"name,omitempty"`
	Flow          string `json:"flow"`
	Signal        string `json:"signal,omitempty"`
	Connector     string `json:"connector,omitempty"`
	MultiBinding  *bool  `json:"multi_binding,omitempty"`
	ClassKind     string `json:"class_kind,omitempty"`
	AddressedOnly *bool  `json:"addressed_only,omitempty"`

	// WantsEcho asks to see the result of chains this endpoint originates.
	// Optimistic display is only safe when a device does exactly what it was
	// told, and devices clamp, ramp, fail part-way and refuse: a slider that
	// commanded 90 to a dimmer trimmed at 50 goes on showing 90 forever
	// otherwise.
	WantsEcho *bool `json:"wants_echo,omitempty"`
}

type WireDef struct {
	Read  *WireOp         `json:"read,omitempty"`
	Write *WireOp         `json:"write,omitempty"`
	Parse json.RawMessage `json:"parse,omitempty"`
}

type WireOp struct {
	Method  string          `json:"method,omitempty"`
	Path    string          `json:"path,omitempty"`
	Headers json.RawMessage `json:"headers,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
	Extract string          `json:"extract,omitempty"`
}

// ManifestAction is a maintenance action offered to the installer.
type ManifestAction struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Confirm *bool  `json:"confirm,omitempty"`
}

type HubRequirement struct {
	HubDriverID string `json:"hub_driver_id"`
	MinVersion  string `json:"min_version,omitempty"`
}

type IngressRule struct {
	Kind  string          `json:"kind"`
	Path  string          `json:"path,omitempty"`
	Auth  string          `json:"auth,omitempty"`
	Port  int             `json:"port,omitempty"`
	Group string          `json:"group,omitempty"`
	Match json.RawMessage `json:"match,omitempty"`
}

// Bool is a helper for the optional booleans above, where the difference between
// "false" and "not declared" is load-bearing.
func Bool(v bool) *bool { return &v }

// Float is the same for optional numbers.
func Float(v float64) *float64 { return &v }

// Render writes the manifest as the JSON that ships in the package.
//
// It fills in schema_version and the deprecated driver_type from the roles, so a
// caller cannot emit a manifest whose two descriptions of itself disagree — the
// controller refuses one that does.
func (m DriverManifest) Render() ([]byte, error) {
	m.SchemaVersion = ManifestSchemaVersion
	m.DriverType = m.Roles.LegacyDriverType()

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
