package driversdk

import (
	"encoding/json"
	"strings"
)

// Commands from the controller's AV resolver (AV-ROUTING §3.6, §9).
//
// When a person picks a source for a screen, the controller works out the path
// through the cables and commands each device on it directly: power on, select
// an input, then — while it plays — volume, transport, the d-pad. Each arrives
// as an ordinary command batch on this device's own queue, with one item:
//
//	name:   the command            "select"
//	item:   the endpoint it concerns — the input a select chooses, the zone a
//	        volume belongs to, or "" for the device as a whole
//	params: {"cap": "cap.av_input@v1", "instance": "main_input",
//	         "command": "select", "value": "hdmi_in3"}
//
// Rules for the driver (§3.6):
//
//   - Execute one command, acknowledge it by reporting the resulting state.
//     Never retry a TOGGLE or a PULSE yourself; the controller decides retries.
//   - cap.av_input select receives an ENDPOINT KEY. Translate it to the
//     device's own input code inside the driver. The input number is never
//     configured anywhere else.
//   - A device that refuses commands while waking either queues for at most its
//     power_on_ms or fails the command. Never drop one silently.
//   - No activity logic in drivers: no automatic input switching, no macros, no
//     CEC follow-me. The resolver owns the order.
//   - A network audio driver turns "zone Z selects input I" into the system's
//     own group / join call; a select of null means leave the group.

// CapabilityCommand is one resolver command, decoded.
type CapabilityCommand struct {
	// Cap is the versioned capability id, e.g. "cap.power@v1".
	Cap string
	// Instance is the capability instance, "" for the un-instanced one.
	Instance string
	// Command is the command id: set, select, step, toggle, play, press.
	Command string
	// Value is the command's value, absent for a PULSE.
	Value json.RawMessage
	// EndpointKey is the endpoint the command concerns.
	EndpointKey string
	// Class is the envelope's command class.
	Class CommandClass
}

// Base is the capability without its version: "cap.power".
func (c CapabilityCommand) Base() string {
	id := strings.ToLower(strings.TrimSpace(c.Cap))
	if i := strings.Index(id, "@"); i >= 0 {
		return id[:i]
	}
	return id
}

// Is reports whether this is the given capability and command, e.g.
// c.Is("cap.power", "set").
func (c CapabilityCommand) Is(capability, command string) bool {
	base := strings.ToLower(strings.TrimSpace(capability))
	if i := strings.Index(base, "@"); i >= 0 {
		base = base[:i]
	}
	return c.Base() == base && strings.EqualFold(c.Command, command)
}

// Bool reads a boolean value.
func (c CapabilityCommand) Bool() (bool, bool) {
	var b bool
	if json.Unmarshal(c.Value, &b) != nil {
		return false, false
	}
	return b, true
}

// String reads a string value. A JSON null reads as ("", true): a select of
// nothing.
func (c CapabilityCommand) String() (string, bool) {
	if strings.TrimSpace(string(c.Value)) == "null" {
		return "", true
	}
	var s string
	if json.Unmarshal(c.Value, &s) != nil {
		return "", false
	}
	return s, true
}

// Number reads a numeric value.
func (c CapabilityCommand) Number() (float64, bool) {
	var n float64
	if json.Unmarshal(c.Value, &n) != nil {
		return 0, false
	}
	return n, true
}

// CapabilityCommands decodes the resolver commands in a batch. Items that do
// not name a capability — ordinary binding crossings — are not returned, so a
// driver can hand the batch to both this and its existing handling.
func (b EndpointBatch) CapabilityCommands() []CapabilityCommand {
	if b.Kind != KindCommand {
		return nil
	}
	var out []CapabilityCommand
	for i := range b.Items {
		it := &b.Items[i]
		var p struct {
			Cap      string          `json:"cap"`
			Instance string          `json:"instance"`
			Command  string          `json:"command"`
			Value    json.RawMessage `json:"value"`
		}
		if len(it.RawParams) == 0 || json.Unmarshal(it.RawParams, &p) != nil || p.Cap == "" {
			continue
		}
		if p.Command == "" {
			p.Command = b.Name
		}
		out = append(out, CapabilityCommand{
			Cap: p.Cap, Instance: p.Instance, Command: p.Command, Value: p.Value,
			EndpointKey: it.EndpointKey, Class: b.CommandClass,
		})
	}
	return out
}
