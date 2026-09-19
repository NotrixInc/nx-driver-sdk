package driversdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// These tests pin the wire contract between the SDK and controller-core. The
// two are separate modules with no shared types, so nothing but a test stops
// one side renaming a field the other reads.

// captureBus stands a fake controller in front of the bus and records the
// request bodies it receives.
type captureBus struct {
	t      *testing.T
	bus    *EndpointBus
	server *httptest.Server

	mu   sync.Mutex
	sent []map[string]any
	resp string
}

func newCaptureBus(t *testing.T) *captureBus {
	t.Helper()
	c := &captureBus{t: t, resp: `{"delivered":[]}`}

	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Errorf("request body was not JSON: %v", err)
		}
		c.mu.Lock()
		c.sent = append(c.sent, decoded)
		resp := c.resp
		c.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(c.server.Close)

	bus, err := NewEndpointBus(EndpointBusConfig{
		DeviceID: "11111111-1111-1111-1111-111111111111",
		BaseURL:  c.server.URL,
	})
	if err != nil {
		t.Fatalf("new endpoint bus: %v", err)
	}
	c.bus = bus
	return c
}

func (c *captureBus) last() map[string]any {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		c.t.Fatal("nothing was sent")
	}
	return c.sent[len(c.sent)-1]
}

// A driver that passes no options sends exactly what it sent before any of this
// existed. Every new field is absent, and absent means today's behaviour.
func TestSendWithNoOptionsIsUnchanged(t *testing.T) {
	c := newCaptureBus(t)

	if _, err := c.bus.SendOnEndpoints(context.Background(), []string{"ch1"}, "set_level", Params{"level": 70}); err != nil {
		t.Fatalf("send: %v", err)
	}

	body := c.last()
	for _, field := range []string{
		"caused_by", "caused_by_endpoint", "command_class",
		"state_sync", "require_ack", "wants_own_echo", "ttl_ms",
	} {
		if _, present := body[field]; present {
			t.Errorf("an unoptioned send set %q; absent must mean today's behaviour", field)
		}
	}
	if body["device_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("device_id = %v", body["device_id"])
	}
	if body["kind"] != KindCommand || body["name"] != "set_level" {
		t.Errorf("kind/name changed: %v %v", body["kind"], body["name"])
	}
}

// The cause travels from the batch handler's context to the send, with no
// argument at the call site. This is what makes loop protection and the hop
// budget engage across a relay at all.
func TestCauseTravelsThroughTheContext(t *testing.T) {
	c := newCaptureBus(t)

	batch := EndpointBatch{
		ID:    4242,
		Kind:  KindCommand,
		Items: []EndpointCommand{{EndpointKey: "control"}},
	}
	ctx := ContextWithCause(context.Background(), batch.Cause())

	if _, err := c.bus.SendPerEndpoint(ctx, "set_level", []EndpointArgs{
		{Key: "warm_white", Params: Params{"level": 70}},
		{Key: "cool_white", Params: Params{"level": 40}},
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	body := c.last()
	if got := body["caused_by"]; got != float64(4242) {
		t.Fatalf("caused_by = %v, want 4242 — the relay is starting a fresh chain", got)
	}
	// A translating driver publishes on endpoints it did not receive on, so the
	// controller has to be told which inbound item these descend from.
	if got := body["caused_by_endpoint"]; got != "control" {
		t.Fatalf("caused_by_endpoint = %v, want \"control\"", got)
	}
}

// A driver that publishes later — out of a goroutine or a hardware callback —
// keeps the Cause and reattaches it.
func TestCauseCanBeCarriedExplicitly(t *testing.T) {
	c := newCaptureBus(t)

	cause := Cause{MessageID: 77, EndpointKey: "control"}
	if _, err := c.bus.SendOnEndpoints(context.Background(), []string{"ch1"}, "set_level", nil, Caused(cause)); err != nil {
		t.Fatalf("send: %v", err)
	}

	body := c.last()
	if body["caused_by"] != float64(77) || body["caused_by_endpoint"] != "control" {
		t.Fatalf("explicit cause did not reach the wire: %v", body)
	}
}

// An explicit Caused() beats whatever the context happens to carry.
func TestExplicitCauseBeatsTheContext(t *testing.T) {
	c := newCaptureBus(t)

	ctx := ContextWithCause(context.Background(), Cause{MessageID: 1, EndpointKey: "a"})
	if _, err := c.bus.SendOnEndpoints(ctx, []string{"ch1"}, "x", nil,
		Caused(Cause{MessageID: 2, EndpointKey: "b"})); err != nil {
		t.Fatalf("send: %v", err)
	}

	if body := c.last(); body["caused_by"] != float64(2) || body["caused_by_endpoint"] != "b" {
		t.Fatalf("the context overrode an explicit cause: %v", body)
	}
}

// The class comes off the endpoint declaration, so an author states it once
// rather than at every call site — three fields to remember is three fields an
// author will eventually forget.
func TestCommandClassComesFromTheDeclaration(t *testing.T) {
	c := newCaptureBus(t)

	err := c.bus.DeclareEndpoints(context.Background(), []EndpointSpec{
		{Key: "cursor", Direction: DirectionOutput, CommandClass: CommandRelative},
		{Key: "power", Direction: DirectionOutput, CommandClass: CommandToggle},
		{Key: "ch1", Direction: DirectionOutput},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}

	if _, err := c.bus.SendOnEndpoints(context.Background(), []string{"cursor"}, "move", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := c.last()["command_class"]; got != string(CommandRelative) {
		t.Fatalf("cursor sent command_class %v, want RELATIVE", got)
	}

	if _, err := c.bus.SendOnEndpoints(context.Background(), []string{"power"}, "toggle", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got := c.last()["command_class"]; got != string(CommandToggle) {
		t.Fatalf("power sent command_class %v, want TOGGLE", got)
	}

	// An endpoint that declared nothing stays absent on the wire, which the
	// controller reads as ABSOLUTE.
	if _, err := c.bus.SendOnEndpoints(context.Background(), []string{"ch1"}, "set_level", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, present := c.last()["command_class"]; present {
		t.Fatal("an undeclared endpoint sent a command_class")
	}
}

// One send covering endpoints that disagree falls back to ABSOLUTE. Collapsing
// a value is harmless; treating a value as a step would add levels together.
func TestMixedClassesFallBackToAbsolute(t *testing.T) {
	c := newCaptureBus(t)

	err := c.bus.DeclareEndpoints(context.Background(), []EndpointSpec{
		{Key: "a", CommandClass: CommandRelative},
		{Key: "b", CommandClass: CommandToggle},
	})
	if err != nil {
		t.Fatalf("declare: %v", err)
	}

	if _, err := c.bus.SendOnEndpoints(context.Background(), []string{"a", "b"}, "x", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, present := c.last()["command_class"]; present {
		t.Fatal("a mixed send named a class rather than falling back to ABSOLUTE")
	}
}

// ReportState says "this is what I am", which must not cross a binding — or one
// device coming up commands its neighbours into whatever level it booted at.
func TestReportStateSetsTheSyncFlag(t *testing.T) {
	c := newCaptureBus(t)

	if _, err := c.bus.ReportState(context.Background(), "ch1", "level_changed", Params{"level": 30}); err != nil {
		t.Fatalf("report: %v", err)
	}

	body := c.last()
	if body["state_sync"] != true {
		t.Fatal("ReportState did not set state_sync")
	}
	if body["kind"] != KindEvent {
		t.Fatalf("ReportState sent kind %v, want event", body["kind"])
	}
}

// Refusals in the send response are decoded rather than dropped. The controller
// reports every crossing that did not happen; a driver that cannot read them
// cannot tell a refusal from a command that quietly went nowhere.
func TestRefusalsAreDecodedFromTheResponse(t *testing.T) {
	c := newCaptureBus(t)
	c.resp = `{
	  "delivered": [{"device_id":"d","message_id":9,"endpoint_keys":["load1"],"hops":2}],
	  "refused": [
	    {"reason":"NACK_LOOP_VISITED","detail":"binding x:fwd is already in this chain","source_endpoint_key":"ch2"},
	    {"reason":"NACK_UNBOUND","detail":"no binding names this endpoint","source_endpoint_key":"ch3"}
	  ],
	  "trace": 555
	}`

	res, err := c.bus.SendOnEndpoints(context.Background(), []string{"ch1", "ch2", "ch3"}, "set_level", nil)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(res.Refused) != 2 {
		t.Fatalf("decoded %d refusals, want 2", len(res.Refused))
	}
	if res.Refused[0].Reason != "NACK_LOOP_VISITED" || res.Refused[0].SourceKey != "ch2" {
		t.Fatalf("first refusal decoded wrong: %+v", res.Refused[0])
	}
	if res.Trace != 555 {
		t.Fatalf("trace = %d, want 555", res.Trace)
	}
	if len(res.Delivered) != 1 || res.Delivered[0].Hops != 2 {
		t.Fatalf("delivery decoded wrong: %+v", res.Delivered)
	}

	// An unwired endpoint is a normal state on a part-commissioned job and is
	// not worth waking anyone over.
	hard := res.HardRefusals()
	if len(hard) != 1 || hard[0].Reason != "NACK_LOOP_VISITED" {
		t.Fatalf("HardRefusals = %+v, want just the loop", hard)
	}
}

// A refusal arriving on the queue reaches OnRefusal. It has no items, so the
// filter that skips legacy driver-addressed traffic used to swallow it.
func TestQueuedRefusalsReachTheHandler(t *testing.T) {
	bus, err := NewEndpointBus(EndpointBusConfig{
		DeviceID: "22222222-2222-2222-2222-222222222222",
		BaseURL:  "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}

	var got []Refusal
	var mu sync.Mutex
	bus.OnRefusal(func(ctx context.Context, r Refusal) {
		mu.Lock()
		got = append(got, r)
		mu.Unlock()
	})

	bus.dispatch(EndpointBatch{
		ID:            5,
		Kind:          KindRefusal,
		NackReason:    "NACK_HOP_LIMIT",
		NackDetail:    "hop budget exhausted before this crossing",
		NackSource:    "ch1",
		CorrelationID: "corr-9",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("handler saw %d refusals, want 1", len(got))
	}
	if got[0].Reason != "NACK_HOP_LIMIT" || got[0].CorrelationID != "corr-9" || got[0].SourceKey != "ch1" {
		t.Fatalf("refusal decoded wrong: %+v", got[0])
	}
}

// A batch handler is handed a context that already carries the cause, and a
// refusal never reaches a batch handler.
func TestDispatchRoutesByKind(t *testing.T) {
	bus, err := NewEndpointBus(EndpointBusConfig{
		DeviceID: "33333333-3333-3333-3333-333333333333",
		BaseURL:  "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("new bus: %v", err)
	}

	var seen []Cause
	bus.OnEndpointBatch(func(ctx context.Context, b EndpointBatch) error {
		c, _ := CauseFromContext(ctx)
		seen = append(seen, c)
		return nil
	})
	bus.OnRefusal(func(ctx context.Context, r Refusal) {})

	bus.dispatch(EndpointBatch{ID: 11, Kind: KindCommand, Items: []EndpointCommand{{EndpointKey: "load1"}}})
	bus.dispatch(EndpointBatch{ID: 12, Kind: KindRefusal, NackReason: "NACK_UNBOUND"})

	if len(seen) != 1 {
		t.Fatalf("batch handler ran %d times, want 1 — a refusal reached it", len(seen))
	}
	if seen[0].MessageID != 11 || seen[0].EndpointKey != "load1" {
		t.Fatalf("handler context carried %+v, want {11 load1}", seen[0])
	}
}

// CauseFor names one item of a multi-item batch, so a driver relaying items
// separately gives each branch its own history rather than the first item's.
func TestCauseForNamesTheItem(t *testing.T) {
	batch := EndpointBatch{ID: 8, Items: []EndpointCommand{
		{EndpointKey: "load1"}, {EndpointKey: "load2"},
	}}

	if got := batch.Cause(); got.EndpointKey != "load1" {
		t.Fatalf("batch cause = %+v, want the first item", got)
	}
	if got := batch.CauseFor("load2"); got.MessageID != 8 || got.EndpointKey != "load2" {
		t.Fatalf("CauseFor = %+v", got)
	}

	var zero Cause
	if zero.Valid() {
		t.Fatal("a zero cause reported itself valid")
	}
	if _, ok := CauseFromContext(ContextWithCause(context.Background(), zero)); ok {
		t.Fatal("a zero cause was attached to a context")
	}
}

// The controller sends the publishing device as source_driver_id. This field
// was read as source_device_id and had always decoded empty.
func TestBatchDecodesTheSourceDevice(t *testing.T) {
	var batch EndpointBatch
	raw := `{"id":3,"kind":"command","name":"set_level",
	         "source_driver_id":"aaaa-bbbb","ts_unix_ms":17,
	         "command_class":"RELATIVE","hops":2,"trace":99,
	         "items":[{"endpoint_key":"load1","binding_dir":"fwd","hops":2,
	                   "source_endpoint_key":"ch1","params":{"level":70}}]}`
	if err := json.Unmarshal([]byte(raw), &batch); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if batch.SourceDeviceID != "aaaa-bbbb" {
		t.Fatalf("SourceDeviceID = %q, want the publisher", batch.SourceDeviceID)
	}
	if batch.CommandClass != CommandRelative || batch.Hops != 2 || batch.Trace != 99 {
		t.Fatalf("envelope fields decoded wrong: %+v", batch)
	}
	item := batch.Items[0]
	if item.SourceEndpointKey != "ch1" || item.BindingDir != "fwd" || item.Hops != 2 {
		t.Fatalf("item fields decoded wrong: %+v", item)
	}
	if item.Params().Float("level") != 70 {
		t.Fatalf("params lost: %v", item.Params())
	}
}

// A module confirming several channels after one write reports them in one
// publish, each with its own level, so the light holding those loads receives
// one batch rather than one per channel.
func TestNotifyPerEndpointIsOneEventWithPerEndpointArguments(t *testing.T) {
	c := newCaptureBus(t)

	if _, err := c.bus.NotifyPerEndpoint(context.Background(), "level_changed", []EndpointArgs{
		{Key: "ch1", Params: Params{"level": 41}},
		{Key: "ch2", Params: Params{"level": 31}},
	}, Caused(Cause{MessageID: 12})); err != nil {
		t.Fatalf("notify: %v", err)
	}

	body := c.last()
	if body["kind"] != KindEvent || body["name"] != "level_changed" {
		t.Fatalf("kind/name = %v %v, want an event", body["kind"], body["name"])
	}
	eps, _ := body["endpoints"].([]any)
	if len(eps) != 2 {
		t.Fatalf("endpoints = %v, want two with their own params", body["endpoints"])
	}
	second, _ := eps[1].(map[string]any)
	params, _ := second["params"].(map[string]any)
	if second["key"] != "ch2" || params["level"] != float64(31) {
		t.Fatalf("second endpoint lost its arguments: %v", second)
	}
	if body["caused_by"] != float64(12) {
		t.Fatalf("caused_by = %v, want 12", body["caused_by"])
	}
	// Naming no endpoint lets each channel inherit its own inbound chain.
	if _, present := body["caused_by_endpoint"]; present {
		t.Fatal("a message-only cause sent caused_by_endpoint")
	}

	if _, err := c.bus.NotifyPerEndpoint(context.Background(), "level_changed", []EndpointArgs{
		{Key: "ch1", Params: Params{"level": 0}},
	}, AsStateSync()); err != nil {
		t.Fatalf("state report: %v", err)
	}
	if c.last()["state_sync"] != true {
		t.Fatal("NotifyPerEndpoint dropped AsStateSync")
	}
}
