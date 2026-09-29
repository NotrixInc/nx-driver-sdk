package driversdk

import (
	"encoding/json"
	"strings"
)

// Roles replace the single-valued DriverType.
//
// DriverType forces a driver to be exactly one of DEVICE, HUB, CHILD or UI, and
// a television is two of them: it talks to hardware and it presents controls to
// a person. Declared DEVICE it gets reachability and no control surface;
// declared UI it gets controls and no reachability. There is no third option,
// and a TV, a DVD player, a receiver and a single wall dimmer all need one.
//
// The fix is two independent flags rather than a wider enum. Topology stays
// inside the device role on purpose: HUB and CHILD answer *how the driver
// reaches hardware*, while UI answers *whether it faces the user*. Those are
// different questions, and putting them on one axis is what forced the choice.
//
// DriverType is not removed. Every driver written against it keeps compiling and
// keeps working, and RolesFor derives the roles from it.

// DeviceTopology is how a driver reaches hardware.
//
// Distinct from the older Topology type, which describes a driver's supported
// network shapes (DIRECT_IP, VIA_HUB) and means something else. Both exist; this
// is the one the manifest's roles.device.topology carries.
type DeviceTopology string

const (
	DeviceTopologyDirect DeviceTopology = "DIRECT"
	DeviceTopologyHub    DeviceTopology = "HUB"
	DeviceTopologyChild  DeviceTopology = "CHILD"
)

// PublisherClass says who is sending a command, which is independent of what
// kind of command it is (CommandClass). Rank arbitrates *concurrent* commands
// only — it is not ownership, and a durable precedence is an override rather
// than a rank.
//
// Set on the envelope by the SDK from the manifest, never by driver code. A
// driver that declares nothing gets AUTOMATION, the middle of the range and the
// least surprising default.
type PublisherClass string

const (
	PublisherSafety     PublisherClass = "SAFETY"     // rank 4 — fire alarm, panic, load shedding
	PublisherManual     PublisherClass = "MANUAL"     // rank 3 — keypad, app, remote, voice
	PublisherAutomation PublisherClass = "AUTOMATION" // rank 2 — scene, schedule, astronomical
	PublisherSensor     PublisherClass = "SENSOR"     // rank 1 — occupancy, daylight, contact
)

// Rank orders two publishers competing for the same endpoint at the same moment.
// An unrecognised class ranks as AUTOMATION rather than zero, so a typo cannot
// silently lose every contest it enters.
func (p PublisherClass) Rank() int {
	switch PublisherClass(strings.ToUpper(strings.TrimSpace(string(p)))) {
	case PublisherSafety:
		return 4
	case PublisherManual:
		return 3
	case PublisherSensor:
		return 1
	default:
		return 2
	}
}

// Roles is what a driver is, as declared in driver.json.
type Roles struct {
	Device *DeviceRole `json:"device,omitempty"`
	UI     *UIRole     `json:"ui,omitempty"`
}

// DeviceRole is present when the driver talks to hardware.
type DeviceRole struct {
	Topology     DeviceTopology `json:"topology,omitempty"`
	Protocols    []Protocol     `json:"protocols,omitempty"`
	Transport    *Transport     `json:"transport,omitempty"`
	Reachability *Reachability  `json:"reachability,omitempty"`
	Switching    []Switching    `json:"switching,omitempty"`

	// How the controller's AV resolver may drive the device (AV-ROUTING §3.4).
	// All optional: DISCRETE power, no timers and discrete inputs is what a
	// driver that declares none of them gets.
	PowerMode PowerMode `json:"power_mode,omitempty"`
	Timing    *Timing   `json:"timing,omitempty"`
	InputMode InputMode `json:"input_mode,omitempty"`
}

// PowerMode says how the resolver may power a device. A toggle is only ever
// sent when state says it is needed; with neither feedback nor a bound sense
// the device must be ALWAYS_ON, or the resolver refuses to plan through it.
type PowerMode string

const (
	PowerDiscrete           PowerMode = "DISCRETE"
	PowerToggleWithFeedback PowerMode = "TOGGLE_WITH_FEEDBACK"
	PowerToggleWithSense    PowerMode = "TOGGLE_WITH_SENSE"
	PowerAlwaysOn           PowerMode = "ALWAYS_ON"
)

// InputMode: CYCLE is a single "Input" button, selectable only with
// active_input feedback.
type InputMode string

const (
	InputDiscrete InputMode = "DISCRETE"
	InputCycle    InputMode = "CYCLE"
)

// Timing is the resolver's fallback when the device gives no feedback.
// SettleMs is for a passive device (extractor, HDBaseT).
type Timing struct {
	PowerOnMs     int `json:"power_on_ms,omitempty"`
	InputSettleMs int `json:"input_settle_ms,omitempty"`
	CooldownMs    int `json:"cooldown_ms,omitempty"`
	SettleMs      int `json:"settle_ms,omitempty"`
}

// UIRole is present when the driver presents a surface to a person.
//
// A UI driver does no I/O at all: it never opens a socket, never waits on a
// device, never parses a vendor's reply. That makes it the one kind of driver
// that cannot block a sibling on a shared host, and therefore the one that can
// be packed densest.
type UIRole struct {
	Surface string   `json:"surface,omitempty"`
	Groups  []string `json:"groups,omitempty"`
}

// Transport is declared so the SDK can own the client. A driver holding its own
// HTTP client cannot be held to the async-only, every-call-under-a-timeout rule
// a shared host depends on.
type Transport struct {
	Kind        string   `json:"kind,omitempty"`
	Scheme      string   `json:"scheme,omitempty"`
	DefaultPort int      `json:"default_port,omitempty"`
	Discovery   []string `json:"discovery,omitempty"`
	Auth        string   `json:"auth,omitempty"`
	TimeoutMs   int      `json:"timeout_ms,omitempty"`
}

// Reachability is how the controller decides a device is answering. Core owns
// that verdict; it used to be computed in a browser, which meant nothing held an
// opinion unless somebody happened to be looking at the page.
type Reachability struct {
	Mode         string `json:"mode,omitempty"` // POLL | PUSH | NONE
	IntervalMs   int    `json:"interval_ms,omitempty"`
	StaleAfterMs int    `json:"stale_after_ms,omitempty"`
	Probe        string `json:"probe,omitempty"`
}

// Switching declares what the binding graph cannot see: that an input reaches an
// output *inside* a matrix. Without it a path through an amplifier is two
// disconnected fragments and no traversal can join them.
//
// AV-ROUTING §3.1: Outputs is matrix shorthand ({output} in Selector is
// substituted per output); an empty Selector is a fixed route (a player's
// origin to its HDMI out); ExtraSteps run after the selector; routes sharing
// Linked share one selector choice; LatencyMs is audio latency the route adds.
// Selector names a cap.av_input@v1 instance, or "cap.av_input@v1" for the
// device's un-instanced one. Inputs are endpoint keys — the resolver sends the
// key, never a number, and the driver translates it.
type Switching struct {
	Output     string      `json:"output,omitempty"`
	Outputs    []string    `json:"outputs,omitempty"`
	Inputs     []string    `json:"inputs"`
	Selector   string      `json:"selector,omitempty"`
	ExtraSteps []ExtraStep `json:"extra_steps,omitempty"`
	Linked     string      `json:"linked,omitempty"`
	LatencyMs  int         `json:"latency_ms,omitempty"`
}

// ExtraStep is a command the resolver sends after a route's selector.
type ExtraStep struct {
	Instance string          `json:"instance"`
	Command  string          `json:"command"`
	Value    json.RawMessage `json:"value,omitempty"`
}

func (r Roles) HasDevice() bool { return r.Device != nil }
func (r Roles) HasUI() bool     { return r.UI != nil }

// Topology returns the declared topology, defaulting to DIRECT. A UI-only driver
// has none and returns the empty string.
func (r Roles) Topology() DeviceTopology {
	if r.Device == nil {
		return ""
	}
	if t := DeviceTopology(strings.ToUpper(strings.TrimSpace(string(r.Device.Topology)))); t != "" {
		return t
	}
	return DeviceTopologyDirect
}

// LegacyDriverType renders roles back into the old single-valued field for a
// consumer that has not migrated. It is lossy by construction — a driver that is
// both reports DEVICE — which is the limitation roles exist to remove, so
// nothing new should be written against it.
func (r Roles) LegacyDriverType() DriverType {
	if r.Device != nil {
		switch r.Topology() {
		case DeviceTopologyHub:
			return DriverTypeHub
		case DeviceTopologyChild:
			return DriverTypeChild
		default:
			return DriverTypeDevice
		}
	}
	if r.UI != nil {
		return DriverTypeUI
	}
	return ""
}

// RolesFor derives roles from a legacy DriverType. A driver that declares
// nothing becomes a direct device driver, which is what every existing consumer
// already assumed for an unset type.
func RolesFor(t DriverType) Roles {
	switch DriverType(strings.ToUpper(strings.TrimSpace(string(t)))) {
	case DriverTypeUI:
		return Roles{UI: &UIRole{}}
	case DriverTypeHub:
		return Roles{Device: &DeviceRole{Topology: DeviceTopologyHub}}
	case DriverTypeChild:
		return Roles{Device: &DeviceRole{Topology: DeviceTopologyChild}}
	default:
		return Roles{Device: &DeviceRole{Topology: DeviceTopologyDirect}}
	}
}

// RoledDriver is the opt-in a driver implements to declare roles directly rather
// than having them derived. Deliberately a separate interface: adding a method
// to Driver would break every driver already written against it, and the derived
// answer is correct for all of them.
type RoledDriver interface {
	Driver
	Roles() Roles
}

// RolesOf returns what a driver is, however it chose to say so.
func RolesOf(d Driver) Roles {
	if rd, ok := d.(RoledDriver); ok {
		r := rd.Roles()
		if r.Device != nil || r.UI != nil {
			return r
		}
	}
	return RolesFor(d.Type())
}
