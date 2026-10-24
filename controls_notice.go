package driversdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// KindControls is core handing a driver its device's Control tab values: the
// whole set, each time a person, a scene or a routine writes them. It arrives on
// the endpoint bus's long poll, so a driver learns of a change at once and
// never polls its device row for it.
const KindControls = "controls"

// KindChildControls tells a hub that one or more of its children's controls
// were written. It carries nothing: the hub reads the children written since its
// last read. It reaches the batch handlers like any other batch.
const KindChildControls = "child_controls"

// Who wrote a device's controls, as core records it.
const (
	ControlsSourceUser    = "USER"
	ControlsSourceScene   = "SCENE"
	ControlsSourceRoutine = "ROUTINE"
)

// Controls is a device's Control tab values at one moment.
type Controls struct {
	Values map[string]any
	// Source is who wrote them: ControlsSourceUser, ControlsSourceScene,
	// ControlsSourceRoutine, or whatever else core recorded. Empty when unknown.
	Source string
	// TSUnixMs is when core wrote them. Each delivery is newer than the last.
	TSUnixMs int64
	// Initial is set on the one delivery made when the bus starts: the values
	// the row already held. They are a record of the past, not a request —
	// applying them would drive the device back to wherever a slider was last
	// left, over everything that happened since. Most drivers only note them.
	Initial bool
	// Variables is the state the driver last published, as core retained it.
	// Set only on the Initial delivery, so a driver restores what it was
	// without reading its row itself; nil on every later one.
	Variables map[string]any
	// Pending is set on the start-up delivery when these controls were written
	// while no instance of the driver was there to receive them — it was
	// restarting, crashed, or being updated — and core says so. Such a delivery
	// is a request, not history: Initial is false, so a driver applies it like
	// any write. Variables is still set, as on every start-up delivery.
	Pending bool
}

// Number reads a numeric control, accepting numbers sent as strings.
func (c Controls) Number(key string) (float64, bool) {
	switch v := c.Values[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// Bool reads a boolean control, accepting the forms the console and scenes
// send: true/false, "on"/"off", "true"/"false", 1/0.
func (c Controls) Bool(key string) (bool, bool) {
	switch v := c.Values[key].(type) {
	case bool:
		return v, true
	case float64:
		return v != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "on", "1":
			return true, true
		case "false", "off", "0":
			return false, true
		}
	}
	return false, false
}

// String reads a text control.
func (c Controls) String(key string) (string, bool) {
	s, ok := c.Values[key].(string)
	return s, ok
}

// Has reports whether the control is set at all.
func (c Controls) Has(key string) bool { _, ok := c.Values[key]; return ok }

// ControlsHandler receives a device's Control tab values.
type ControlsHandler func(ctx context.Context, c Controls)

// OnControls registers a handler for the device's Control tab values.
//
// Once the bus starts, the handler is called once with what the row already
// holds (Controls.Initial), then with every newer write. Register it before
// Start. Without one, controls notices reach the batch handlers as a batch of
// kind KindControls, as they always have.
func (b *EndpointBus) OnControls(h ControlsHandler) {
	if h == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.controls = append(b.controls, h)
}

func (b *EndpointBus) controlsHandlers() []ControlsHandler {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]ControlsHandler, len(b.controls))
	copy(out, b.controls)
	return out
}

// ReadControls reads the device's Control tab values, and the variables core
// retained for it, from core. The bus does this itself when it starts; a driver
// has no reason to call it on a timer.
func (b *EndpointBus) ReadControls(ctx context.Context) (Controls, error) {
	endpoint := fmt.Sprintf("%s/v1/devices?id=%s", b.baseURL, url.QueryEscape(b.deviceID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Controls{}, err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return Controls{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return Controls{}, fmt.Errorf("read controls: %s", resp.Status)
	}

	var dev struct {
		MetaJSON struct {
			Controls  map[string]any `json:"controls"`
			Source    string         `json:"controls_source"`
			TS        json.Number    `json:"controls_ts_unix_ms"`
			Variables map[string]any `json:"variables"`
		} `json:"meta_json"`
		ControlsPending bool `json:"controls_pending"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
		return Controls{}, err
	}
	return Controls{
		Values:    dev.MetaJSON.Controls,
		Source:    dev.MetaJSON.Source,
		TSUnixMs:  numberToMs(dev.MetaJSON.TS),
		Variables: dev.MetaJSON.Variables,
		Pending:   dev.ControlsPending,
	}, nil
}

func numberToMs(n json.Number) int64 {
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return int64(f)
	}
	return 0
}

// controlsFromNotice decodes a notice's payload. ok is false when it carries
// none — an older core, or a write that failed — and the row must be read.
func controlsFromNotice(batch EndpointBatch) (Controls, bool) {
	if len(batch.Payload) == 0 {
		return Controls{}, false
	}
	var p struct {
		Controls map[string]any `json:"controls"`
		Source   string         `json:"source"`
		TS       json.Number    `json:"ts_unix_ms"`
	}
	if err := json.Unmarshal(batch.Payload, &p); err != nil || p.Controls == nil {
		return Controls{}, false
	}
	return Controls{Values: p.Controls, Source: p.Source, TSUnixMs: numberToMs(p.TS)}, true
}

// seedControls makes the Initial delivery, once. It reports whether that is
// done, so the poll loop retries while core is unreachable at start.
func (b *EndpointBus) seedControls() bool {
	if b.controlsSeeded {
		return true
	}
	handlers := b.controlsHandlers()
	if len(handlers) == 0 {
		b.controlsSeeded = true
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	c, err := b.ReadControls(ctx)
	cancel()
	if err != nil {
		if b.logger != nil {
			b.logger.Debug("reading controls at start failed", "err", err.Error())
		}
		return false
	}
	b.controlsSeeded = true
	b.lastControlsTS = c.TSUnixMs
	// Controls the previous instance never received are a request; anything
	// else the row holds is a record of the past.
	c.Initial = !c.Pending
	b.deliverControls(handlers, c)
	return true
}

// dispatchControls hands a notice to the controls handlers. Without any, it is
// left to the batch handlers.
func (b *EndpointBus) dispatchControls(batch EndpointBatch) bool {
	handlers := b.controlsHandlers()
	if len(handlers) == 0 {
		return false
	}

	c, ok := controlsFromNotice(batch)
	if !ok {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		read, err := b.ReadControls(ctx)
		cancel()
		if err != nil {
			if b.logger != nil {
				b.logger.Warn("reading controls after a notice failed", "err", err.Error())
			}
			return true
		}
		c = read
		c.Variables = nil
	}

	// A notice can trail the start-up read, or two writes can land in one
	// poll; only something newer than what was delivered is passed on. A
	// notice from before the start-up read finished is the past.
	if c.TSUnixMs > 0 && c.TSUnixMs <= b.lastControlsTS {
		return true
	}
	if c.TSUnixMs > b.lastControlsTS {
		b.lastControlsTS = c.TSUnixMs
	}
	// The first notice after a failed start-up read is still a real change, not
	// a record: it is delivered as one.
	b.controlsSeeded = true
	b.deliverControls(handlers, c)
	return true
}

func (b *EndpointBus) deliverControls(handlers []ControlsHandler, c Controls) {
	if c.Values == nil {
		c.Values = map[string]any{}
	}
	for _, h := range handlers {
		h(context.Background(), c)
	}
}
