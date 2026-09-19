package driversdk

import "context"

// Causality — how a driver tells the controller what its publish is a response
// to, so the wiring's loop protection and hop budget actually engage.
//
// Controller-core keeps a visited chain for every crossing: which binding was
// crossed, and in which direction. When a driver relays — receives on one
// endpoint and publishes on another — that chain has to carry over, or the
// relay looks to core like the start of a fresh episode. A cycle in the wiring
// then never trips, and the hop budget restarts at zero every time.
//
// The chain itself does not travel through the driver, on purpose. A driver
// that returned an empty one, by malice or by a bug in an SDK, would switch
// loop protection off for the rest of the episode. So a driver names only the
// message it is responding to, and core looks the rest up.
//
// The usual case costs nothing: the context handed to an OnEndpointBatch
// handler already carries the cause, so a send made with that context (or one
// derived from it) is linked automatically. A driver that publishes later, out
// of a goroutine or a hardware callback, keeps the Cause value and reattaches
// it with ContextWithCause.

// Cause identifies the delivery a publish is a response to.
//
// EndpointKey is which of THIS device's endpoints the causing item arrived on.
// It matters for a translating driver: a CCT light takes one level on `control`
// and emits `warm_white` and `cool_white`, and core cannot work out which
// inbound item those two descend from unless it is told.
type Cause struct {
	MessageID   int64
	EndpointKey string
}

// Valid reports whether the cause names a real delivery. A zero Cause is the
// normal state for a chain the driver started itself.
func (c Cause) Valid() bool { return c.MessageID > 0 }

type causeCtxKey struct{}

// ContextWithCause attaches a cause to ctx. Any send made with the result is
// linked to that delivery.
//
// Use it when the publish does not happen inside the batch handler — a hardware
// write that completes later, a debounce, a goroutine — by keeping the Cause
// value and reattaching it to a fresh context.
func ContextWithCause(ctx context.Context, c Cause) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.Valid() {
		return ctx
	}
	return context.WithValue(ctx, causeCtxKey{}, c)
}

// CauseFromContext reads a cause attached by ContextWithCause, or by the batch
// handler's own context.
func CauseFromContext(ctx context.Context) (Cause, bool) {
	if ctx == nil {
		return Cause{}, false
	}
	c, ok := ctx.Value(causeCtxKey{}).(Cause)
	if !ok || !c.Valid() {
		return Cause{}, false
	}
	return c, true
}

// Cause returns the cause for this batch as a whole.
//
// It names the batch's first item, which is right for the common case of a
// delivery addressing one endpoint. A driver that receives several items and
// relays them separately should use CauseFor on each, so every branch inherits
// its own history rather than the first one's.
func (b EndpointBatch) Cause() Cause {
	c := Cause{MessageID: b.ID}
	if len(b.Items) > 0 {
		c.EndpointKey = b.Items[0].EndpointKey
	}
	return c
}

// CauseFor returns the cause for one item of this batch, named by the endpoint
// of this device it arrived on.
func (b EndpointBatch) CauseFor(endpointKey string) Cause {
	return Cause{MessageID: b.ID, EndpointKey: endpointKey}
}
