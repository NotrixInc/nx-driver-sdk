package driversdk

import "strings"

// Send options and the command class.
//
// Everything here is additive. A driver that passes no options behaves exactly
// as it did before these existed: ABSOLUTE, no flags, no cause beyond whatever
// the context carries.

// CommandClass says what kind of thing a command is, which is what decides
// whether the controller may collapse it, retry it, or replay it after the
// device has been away.
//
//	ABSOLUTE  a value. The same meaning however many arrive, so a newer one
//	          replaces a pending one and a stalled driver wakes to the current
//	          level instead of replaying every step it missed.
//	RELATIVE  a step. It accumulates, so it is never discarded — pending steps
//	          are summed. Eleven cursor presses must move the cursor eleven
//	          rows, not two.
//	TOGGLE    a flip. A repeat is a different outcome, so it is never collapsed
//	          and never retried: a retried toggle produces the opposite of what
//	          was asked.
//
// Neither RELATIVE nor TOGGLE survives an outage. Neither means anything
// relative to a state the device has lost.
type CommandClass string

const (
	CommandAbsolute CommandClass = "ABSOLUTE"
	CommandRelative CommandClass = "RELATIVE"
	CommandToggle   CommandClass = "TOGGLE"
)

func normalizeCommandClass(s string) CommandClass {
	switch CommandClass(strings.ToUpper(strings.TrimSpace(s))) {
	case CommandRelative:
		return CommandRelative
	case CommandToggle:
		return CommandToggle
	default:
		return CommandAbsolute
	}
}

type sendOptions struct {
	cause         Cause
	causeSet      bool
	commandClass  CommandClass
	classSet      bool
	correlationID string
	requireAck    bool
	stateSync     bool
	wantsOwnEcho  bool
	ttlMs         int64
}

// EndpointOption adjusts one endpoint-bus send.
//
// Named apart from the MessageBus SendOption on purpose: the two buses are
// different surfaces, and a driver migrating from one to the other should get a
// compile error rather than a silently ignored option.
type EndpointOption func(*sendOptions)

// Caused links this publish to the delivery that prompted it, so the wiring's
// loop protection and hop budget carry across the relay.
//
// Prefer letting the context carry it — a send made with the batch handler's
// context is linked automatically. Use this when the publish happens somewhere
// the context could not reach.
func Caused(c Cause) EndpointOption {
	return func(o *sendOptions) {
		o.cause = c
		o.causeSet = true
	}
}

// WithCommandClass overrides the class for this send.
//
// It is usually unnecessary: the class is taken from the endpoint's own
// declaration, so an author who declared `cursor` as RELATIVE once does not
// have to remember it at every call site. Use this for an endpoint that
// genuinely carries more than one kind of command.
func WithCommandClass(c CommandClass) EndpointOption {
	return func(o *sendOptions) {
		o.commandClass = c
		o.classSet = true
	}
}

// Correlate tags the publish so a reply or a refusal can be matched
// back to it.
func Correlate(id string) EndpointOption {
	return func(o *sendOptions) { o.correlationID = strings.TrimSpace(id) }
}

// ExpireAfter drops the message if it has not been delivered within ms
// milliseconds. Zero means it never expires on its own.
func ExpireAfter(ms int64) EndpointOption {
	return func(o *sendOptions) { o.ttlMs = ms }
}

// RequireAck asks to be told what happened to this publish. Refusals are
// delivered to OnRefusal handlers as well as returned in the send result, which
// matters when the two differ in timing.
func RequireAck() EndpointOption {
	return func(o *sendOptions) { o.requireAck = true }
}

// AsStateSync marks the publish as "this is what I am" rather than "this
// changed".
//
// The controller updates retained state and notifies subscribers, but does not
// cross any binding. Use it for the report a driver makes on start: a FOLLOW
// binding cannot tell a sync from a change, so without this flag one device
// coming back up commands its neighbours into whatever level it happens to
// have.
func AsStateSync() EndpointOption {
	return func(o *sendOptions) { o.stateSync = true }
}

// WantsOwnEcho asks to receive echoes of chains this endpoint originated,
// which the controller otherwise suppresses.
func WantsOwnEcho() EndpointOption {
	return func(o *sendOptions) { o.wantsOwnEcho = true }
}

func buildSendOptions(opts []EndpointOption) sendOptions {
	var o sendOptions
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return o
}
