package driversdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCore serves the device row and a queue of controls notices, the way
// controller-core does.
type fakeCore struct {
	mu       sync.Mutex
	row      string
	queue    []map[string]any
	rowReads atomic.Int32
}

func (f *fakeCore) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/v1/devices":
		f.rowReads.Add(1)
		f.mu.Lock()
		row := f.row
		f.mu.Unlock()
		_, _ = w.Write([]byte(row))
	case r.URL.Path == "/v1/driver-messages":
		f.mu.Lock()
		msgs := f.queue
		f.queue = nil
		f.mu.Unlock()
		if len(msgs) == 0 && r.URL.Query().Get("wait_ms") != "" {
			time.Sleep(20 * time.Millisecond)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": msgs})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCore) push(m map[string]any) {
	f.mu.Lock()
	f.queue = append(f.queue, m)
	f.mu.Unlock()
}

func startControlsBus(t *testing.T, f *fakeCore) (*EndpointBus, chan Controls) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	bus, err := NewEndpointBus(EndpointBusConfig{DeviceID: "dev-1", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan Controls, 10)
	bus.OnControls(func(_ context.Context, c Controls) { got <- c })
	if err := bus.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Stop(context.Background()) })
	return bus, got
}

func next(t *testing.T, got chan Controls) Controls {
	t.Helper()
	select {
	case c := <-got:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no controls delivered")
	}
	return Controls{}
}

// The bus reads the row once at start, then takes every change from notices
// without reading it again.
func TestControlsAreSeededOnceThenPushed(t *testing.T) {
	f := &fakeCore{row: `{"meta_json":{"controls":{"brightness":30},"controls_source":"USER","controls_ts_unix_ms":100,"variables":{"color":"#ff0000"}}}`}
	_, got := startControlsBus(t, f)

	first := next(t, got)
	if !first.Initial || first.TSUnixMs != 100 {
		t.Fatalf("first delivery = %+v, want the initial row", first)
	}
	if v, _ := first.Number("brightness"); v != 30 {
		t.Fatalf("brightness = %v", v)
	}
	if first.Variables["color"] != "#ff0000" {
		t.Fatalf("the initial delivery must carry the retained variables, got %v", first.Variables)
	}

	f.push(map[string]any{"id": 5, "kind": KindControls,
		"payload_json": map[string]any{"controls": map[string]any{"brightness": 70, "power": "on"}, "source": "SCENE", "ts_unix_ms": 200}})
	c := next(t, got)
	if c.Initial || c.Source != ControlsSourceScene || c.TSUnixMs != 200 || c.Variables != nil {
		t.Fatalf("pushed delivery = %+v", c)
	}
	if on, ok := c.Bool("power"); !ok || !on {
		t.Fatalf("power = %v %v", on, ok)
	}
	if n := f.rowReads.Load(); n != 1 {
		t.Fatalf("the row was read %d times; a pushed change must not read it", n)
	}
}

// A notice no newer than what was delivered is the past, and is dropped.
func TestStaleControlsNoticesAreDropped(t *testing.T) {
	f := &fakeCore{row: `{"meta_json":{"controls":{"x":1},"controls_ts_unix_ms":500}}`}
	_, got := startControlsBus(t, f)
	next(t, got)

	f.push(map[string]any{"id": 6, "kind": KindControls,
		"payload_json": map[string]any{"controls": map[string]any{"x": 0}, "ts_unix_ms": 400}})
	f.push(map[string]any{"id": 7, "kind": KindControls,
		"payload_json": map[string]any{"controls": map[string]any{"x": 2}, "ts_unix_ms": 600}})
	c := next(t, got)
	if v, _ := c.Number("x"); v != 2 {
		t.Fatalf("delivered %+v, want only the newer notice", c)
	}
}

// An older core sends a notice with nothing in it; the bus reads the row.
func TestEmptyControlsNoticeReadsTheRow(t *testing.T) {
	f := &fakeCore{row: `{"meta_json":{"controls":{"x":1},"controls_ts_unix_ms":10}}`}
	_, got := startControlsBus(t, f)
	next(t, got)

	f.mu.Lock()
	f.row = `{"meta_json":{"controls":{"x":9},"controls_ts_unix_ms":20}}`
	f.mu.Unlock()
	f.push(map[string]any{"id": 8, "kind": KindControls})

	c := next(t, got)
	if v, _ := c.Number("x"); v != 9 || c.Initial {
		t.Fatalf("delivered %+v, want the row read after the notice", c)
	}
}

// Without a controls handler, a notice still reaches the batch handlers, as it
// did before OnControls existed.
func TestControlsNoticeFallsBackToBatchHandlers(t *testing.T) {
	bus, err := NewEndpointBus(EndpointBusConfig{DeviceID: "d", BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	var kind string
	bus.OnEndpointBatch(func(_ context.Context, b EndpointBatch) error { kind = b.Kind; return nil })
	bus.dispatch(EndpointBatch{ID: 1, Kind: KindControls})
	if kind != KindControls {
		t.Fatalf("batch handler saw %q", kind)
	}
}

func TestControlsValueReaders(t *testing.T) {
	c := Controls{Values: map[string]any{"a": "42.5", "b": float64(0), "c": "OFF", "d": "text"}}
	if v, ok := c.Number("a"); !ok || v != 42.5 {
		t.Fatalf("Number(a) = %v %v", v, ok)
	}
	if v, ok := c.Bool("b"); !ok || v {
		t.Fatalf("Bool(b) = %v %v", v, ok)
	}
	if v, ok := c.Bool("c"); !ok || v {
		t.Fatalf("Bool(c) = %v %v", v, ok)
	}
	if v, ok := c.String("d"); !ok || !strings.EqualFold(v, "text") {
		t.Fatalf("String(d) = %v %v", v, ok)
	}
	if c.Has("zz") {
		t.Fatal("Has(zz)")
	}
}

// A hub's child-controls notice survives the poll and reaches the batch
// handlers.
func TestChildControlsNoticeReachesTheBatchHandlers(t *testing.T) {
	f := &fakeCore{row: `{"meta_json":{}}`}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	bus, err := NewEndpointBus(EndpointBusConfig{DeviceID: "hub", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	bus.OnEndpointBatch(func(_ context.Context, b EndpointBatch) error { got <- b.Kind; return nil })
	if err := bus.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bus.Stop(context.Background()) })

	f.push(map[string]any{"id": 9, "kind": KindChildControls})
	select {
	case k := <-got:
		if k != KindChildControls {
			t.Fatalf("kind %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the notice was dropped")
	}
}
