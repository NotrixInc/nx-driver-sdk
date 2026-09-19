package driversdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// EndpointBus is the driver's side of endpoint-addressed messaging.
//
// A driver names only its own endpoints. Controller-core resolves each against
// the wiring the installer built, groups the result by target device, and
// delivers one batch per device with the target endpoint already filled in. A
// driver never learns a peer's driver id, device id or channel number, and it
// never parses an address out of a payload.
//
// This replaces MessageBus + BindingManager for driver-to-driver traffic: no
// binding cache, no polling the binding table, no working out who to send to.

// Endpoint directions. Two endpoints may be
// bound only if they share a class and face opposite ways.
const (
	DirectionInput  = "Input"
	DirectionOutput = "Output"
	DirectionBidir  = "Bidirectional"
)

// Message kinds. Commands are collapsed by the controller when a newer one
// arrives for the same endpoints — a driver that stalls mid-ramp wakes to the
// current level rather than replaying every step it missed. Events are never
// collapsed.
const (
	KindCommand = "command"
	KindEvent   = "event"
	// KindRefusal is a message the controller puts on this driver's own queue
	// to say what happened to something it published. Refusals arrive here as
	// well as in the send result, because the two can differ in timing.
	KindRefusal = "nack"
)

// EndpointSpec is one endpoint a driver exposes. The full set is pushed with
// DeclareEndpoints and may change while the driver runs.
type EndpointSpec struct {
	Key       string `json:"key"`
	Name      string `json:"name,omitempty"`
	Direction string `json:"direction"`
	// Class decides what may be wired to what, e.g. "DC_Dimmer".
	Class string `json:"class,omitempty"`
	Type  string `json:"type,omitempty"`
	// MultiBinding allows more than one binding on this endpoint. Nil means yes.
	MultiBinding *bool             `json:"multi_binding,omitempty"`
	ValueType    string            `json:"value_type,omitempty"`
	Unit         string            `json:"unit,omitempty"`
	Meta         map[string]string `json:"meta,omitempty"`

	// CommandClass is what kind of command this endpoint carries. Declaring it
	// once here is the point: an author who has to remember ABSOLUTE, RELATIVE
	// or TOGGLE at every call site will eventually forget, and the failure is
	// silent — a cursor that moves two rows for eleven presses.
	//
	// Empty means ABSOLUTE, which is what every endpoint declared before this
	// field existed already meant.
	CommandClass CommandClass `json:"command_class,omitempty"`
}

// Params carries a message's arguments.
type Params map[string]any

func (p Params) Float(key string) float64 {
	switch v := p[key].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

func (p Params) Int(key string) int { return int(p.Float(key)) }

func (p Params) Bool(key string) bool {
	if v, ok := p[key].(bool); ok {
		return v
	}
	return false
}

func (p Params) String(key string) string {
	if v, ok := p[key].(string); ok {
		return v
	}
	return ""
}

func (p Params) Has(key string) bool { _, ok := p[key]; return ok }

// EndpointCommand is one addressed item inside a batch. EndpointKey is an
// endpoint on the *receiving* driver.
type EndpointCommand struct {
	EndpointKey string          `json:"endpoint_key"`
	BindingID   string          `json:"binding_id,omitempty"`
	RawParams   json.RawMessage `json:"params,omitempty"`

	// SourceEndpointKey is the publisher's endpoint this item came from, so a
	// batch drawn from several sources can be told apart.
	SourceEndpointKey string `json:"source_endpoint_key,omitempty"`

	// BindingDir is which way the binding was crossed: "fwd" on the command
	// path, "rev" on the notification path back.
	BindingDir string `json:"binding_dir,omitempty"`

	// Hops is how deep this item is in its causal chain. Diagnostic.
	Hops uint8 `json:"hops,omitempty"`

	params Params
}

// Params decodes the item's arguments once and caches the result.
func (c *EndpointCommand) Params() Params {
	if c.params == nil {
		c.params = Params{}
		if len(c.RawParams) > 0 {
			_ = json.Unmarshal(c.RawParams, &c.params)
		}
	}
	return c.params
}

// EndpointBatch is everything one send addressed to this device, delivered
// together so the driver can act on it in a single pass — one write to the
// hardware rather than one per endpoint.
type EndpointBatch struct {
	ID int64 `json:"id"`
	// Kind is one of KindCommand, KindEvent or KindRefusal.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// SourceDeviceID is the device that published. The controller sends this as
	// `source_driver_id`; it was read here as `source_device_id` for long
	// enough that the field has always decoded empty.
	SourceDeviceID string            `json:"source_driver_id"`
	CorrelationID  string            `json:"correlation_id,omitempty"`
	TSUnixMs       int64             `json:"ts_unix_ms"`
	Items          []EndpointCommand `json:"items"`

	// CommandClass is what kind of command this is. Empty means ABSOLUTE.
	CommandClass CommandClass `json:"command_class,omitempty"`
	// Hops is the depth of the deepest item. Diagnostic.
	Hops uint8 `json:"hops,omitempty"`
	// Trace is the causal episode this belongs to, useful in logs when working
	// out which press produced which write.
	Trace uint64 `json:"trace,omitempty"`

	// Set when Kind is KindRefusal.
	NackReason string `json:"nack_reason,omitempty"`
	NackDetail string `json:"nack_detail,omitempty"`
	NackSource string `json:"nack_source_endpoint_key,omitempty"`
	NackTarget string `json:"nack_target_endpoint_key,omitempty"`
}

// EndpointBatchHandler receives a whole batch.
type EndpointBatchHandler func(ctx context.Context, batch EndpointBatch) error

type EndpointBusConfig struct {
	DeviceID   string
	BaseURL    string
	HTTPClient *http.Client
	Logger     Logger
}

type EndpointBus struct {
	deviceID   string
	baseURL    string
	httpClient *http.Client
	logger     Logger

	mu       sync.RWMutex
	handlers []EndpointBatchHandler
	refusals []RefusalHandler
	// declaredClasses is what DeclareEndpoints last published, so a send can
	// take its command class from the endpoint definition rather than from the
	// author remembering it at the call site.
	declaredClasses map[string]CommandClass

	afterID int64
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// RefusalHandler receives a refusal the controller put on this driver's queue.
type RefusalHandler func(ctx context.Context, refusal Refusal)

func NewEndpointBus(cfg EndpointBusConfig) (*EndpointBus, error) {
	deviceID := strings.TrimSpace(cfg.DeviceID)
	if deviceID == "" {
		return nil, fmt.Errorf("device ID is required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	for _, env := range []string{"CORE_HTTP_ADDR", "CONTROLLER_CORE_HTTP_ADDR"} {
		if baseURL != "" {
			break
		}
		baseURL = strings.TrimSpace(os.Getenv(env))
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// Must exceed the longest long poll (30s) so a quiet bus does not look
		// like a timeout.
		httpClient = &http.Client{Timeout: 40 * time.Second}
	}

	return &EndpointBus{
		deviceID:   deviceID,
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		logger:     cfg.Logger,
		stopCh:     make(chan struct{}),
	}, nil
}

func NewEndpointBusFromEnv(deviceID string, logger Logger) (*EndpointBus, error) {
	return NewEndpointBus(EndpointBusConfig{DeviceID: deviceID, Logger: logger})
}

func (b *EndpointBus) DeviceID() string { return b.deviceID }

// OnEndpointBatch registers a handler for incoming batches.
func (b *EndpointBus) OnEndpointBatch(h EndpointBatchHandler) {
	if h == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// OnRefusal registers a handler for refusals the controller reports back.
//
// A send already returns its refusals; this is for the ones that arrive out of
// band, and for drivers that want one place to log everything the wiring
// rejected rather than checking at each call site.
func (b *EndpointBus) OnRefusal(h RefusalHandler) {
	if h == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refusals = append(b.refusals, h)
}

// DeclareEndpoints publishes the driver's complete endpoint set. Call it on
// start and again whenever configuration changes the set — adding a load, or
// removing one. Endpoints that disappear are retired; a binding that pointed at
// one is kept and reattaches if the key comes back.
func (b *EndpointBus) DeclareEndpoints(ctx context.Context, specs []EndpointSpec) error {
	body, err := json.Marshal(map[string]any{"endpoints": specs})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/v1/devices/%s/endpoints", b.baseURL, b.deviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("declare endpoints: %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}

	// Remember what each endpoint carries, so sends can be classed without the
	// author restating it every time.
	classes := make(map[string]CommandClass, len(specs))
	for _, spec := range specs {
		classes[spec.Key] = normalizeCommandClass(string(spec.CommandClass))
	}
	b.mu.Lock()
	b.declaredClasses = classes
	b.mu.Unlock()

	return nil
}

// Refusal is one outcome that was not a delivery, in the controller's own
// words. Every refusal is named: a command that was refused is not the same
// thing as one that quietly went nowhere, and a driver that cannot tell the two
// apart cannot report either.
type Refusal struct {
	// Reason is a stable identifier: NACK_LOOP_VISITED, NACK_HOP_LIMIT,
	// NACK_UNBOUND, NACK_ORPHANED, NACK_RATE_LIMITED and so on.
	Reason        string `json:"reason"`
	Detail        string `json:"detail,omitempty"`
	SourceKey     string `json:"source_endpoint_key,omitempty"`
	TargetDevice  string `json:"target_device_id,omitempty"`
	TargetKey     string `json:"target_endpoint_key,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ReasonUnbound is the one refusal that is usually not a fault: nobody has
// wired that endpoint yet, which is a normal state on a part-commissioned job.
const ReasonUnbound = "NACK_UNBOUND"

func (r Refusal) String() string {
	if r.Detail == "" {
		return r.Reason
	}
	return r.Reason + ": " + r.Detail
}

// SendResultEndpoints reports where a send actually went.
type SendResultEndpoints struct {
	Delivered []struct {
		DeviceID  string   `json:"device_id"`
		MessageID int64    `json:"message_id"`
		Endpoints []string `json:"endpoint_keys"`
		Hops      uint8    `json:"hops,omitempty"`
	} `json:"delivered"`

	// Refused names every crossing that did not happen, and why. Unbound and
	// Orphaned below are two of those reasons, kept as their own lists because
	// callers already read them.
	Refused []Refusal `json:"refused,omitempty"`

	Unbound  []string `json:"unbound,omitempty"`
	Orphaned []string `json:"orphaned,omitempty"`

	// StateSync reports that the publish updated retained state and notified
	// subscribers without crossing any binding. It is the correct outcome for
	// AsStateSync, not a failure.
	StateSync bool `json:"state_sync,omitempty"`

	// Trace is the causal episode this publish belongs to.
	Trace uint64 `json:"trace,omitempty"`
}

// HardRefusals returns the refusals worth reporting, leaving out endpoints
// nobody has wired yet.
func (r *SendResultEndpoints) HardRefusals() []Refusal {
	if r == nil {
		return nil
	}
	out := make([]Refusal, 0, len(r.Refused))
	for _, ref := range r.Refused {
		if ref.Reason == ReasonUnbound {
			continue
		}
		out = append(out, ref)
	}
	return out
}

// SendOnEndpoints issues one command out of the named endpoints of this device.
// Whatever is wired to them receives it; endpoints nobody has wired are reported
// in Unbound and are not an error.
//
// Sending on several endpoints in one call is the point: the controller groups
// the fan-out per target device, so a light driving three channels of one module
// produces one batch and one write to the hardware.
func (b *EndpointBus) SendOnEndpoints(ctx context.Context, endpointKeys []string, name string, params Params, opts ...EndpointOption) (*SendResultEndpoints, error) {
	return b.send(ctx, endpointKeys, KindCommand, name, params, opts)
}

// NotifyOnEndpoint reports a change out of one of this device's endpoints.
// Status travels back along the same binding the command arrived on.
func (b *EndpointBus) NotifyOnEndpoint(ctx context.Context, endpointKey string, name string, params Params, opts ...EndpointOption) (*SendResultEndpoints, error) {
	return b.send(ctx, []string{endpointKey}, KindEvent, name, params, opts)
}

// ReportState publishes what this device currently is, without crossing any
// binding.
//
// This is the report to make on start, and after reconnecting to hardware. A
// FOLLOW binding cannot tell "this is what I am" from "this changed", so
// reporting a boot state as an ordinary notification has one device commanding
// its neighbours into whatever level it happened to come up at.
func (b *EndpointBus) ReportState(ctx context.Context, endpointKey string, name string, params Params, opts ...EndpointOption) (*SendResultEndpoints, error) {
	return b.send(ctx, []string{endpointKey}, KindEvent, name, params, append(opts, AsStateSync()))
}

// EndpointArgs pairs one of this device's endpoints with the arguments for it.
type EndpointArgs struct {
	Key    string `json:"key"`
	Params Params `json:"params,omitempty"`
}

// SendPerEndpoint issues one command whose arguments differ per endpoint — a
// tunable white light setting warm and cool to different levels, for instance.
// It is still one request and still groups per target device, so the receiving
// driver gets a single batch.
func (b *EndpointBus) SendPerEndpoint(ctx context.Context, name string, args []EndpointArgs, opts ...EndpointOption) (*SendResultEndpoints, error) {
	return b.sendPerEndpoint(ctx, KindCommand, name, args, opts)
}

// NotifyPerEndpoint reports changes out of several of this device's endpoints
// in one publish, each with its own arguments — a module confirming the levels
// three channels reached after a single write, for instance.
//
// It is the notification twin of SendPerEndpoint. Reporting each endpoint with
// its own NotifyOnEndpoint costs one request per endpoint, and a light holding
// ten loads through a ramp would then see ten deliveries per step instead of
// one. Combined with AsStateSync it is also the whole-device boot report.
//
// A cause passed here should name only the message, not an endpoint: every
// source endpoint then inherits the chain of the item that arrived on that same
// endpoint, which is right for a driver reporting back where it was commanded.
func (b *EndpointBus) NotifyPerEndpoint(ctx context.Context, name string, args []EndpointArgs, opts ...EndpointOption) (*SendResultEndpoints, error) {
	return b.sendPerEndpoint(ctx, KindEvent, name, args, opts)
}

func (b *EndpointBus) sendPerEndpoint(ctx context.Context, kind, name string, args []EndpointArgs, opts []EndpointOption) (*SendResultEndpoints, error) {
	if len(args) == 0 {
		return &SendResultEndpoints{}, nil
	}
	keys := make([]string, 0, len(args))
	for _, a := range args {
		keys = append(keys, a.Key)
	}
	body := map[string]any{
		"device_id": b.deviceID,
		"endpoints": args,
		"kind":      kind,
		"name":      name,
	}
	b.applyOptions(ctx, body, keys, opts)
	return b.sendRaw(ctx, body)
}

func (b *EndpointBus) send(ctx context.Context, keys []string, kind, name string, params Params, opts []EndpointOption) (*SendResultEndpoints, error) {
	if len(keys) == 0 {
		return &SendResultEndpoints{}, nil
	}
	if params == nil {
		params = Params{}
	}

	body := map[string]any{
		"device_id":     b.deviceID,
		"endpoint_keys": keys,
		"kind":          kind,
		"name":          name,
		"params":        params,
	}
	b.applyOptions(ctx, body, keys, opts)
	return b.sendRaw(ctx, body)
}

// applyOptions fills in everything the caller did not have to think about: the
// cause carried by the context, the command class declared on the endpoint, and
// whichever flags were asked for.
func (b *EndpointBus) applyOptions(ctx context.Context, body map[string]any, keys []string, opts []EndpointOption) {
	o := buildSendOptions(opts)

	// An explicit Caused() wins; otherwise the context a batch handler was
	// given carries it, so the common relay needs no argument at all.
	cause := o.cause
	if !o.causeSet {
		if fromCtx, ok := CauseFromContext(ctx); ok {
			cause = fromCtx
		}
	}
	if cause.Valid() {
		body["caused_by"] = cause.MessageID
		if cause.EndpointKey != "" {
			// Which of OUR endpoints the causing item landed on. A translating
			// driver publishes on different endpoints than it received on, and
			// without this the controller cannot join the two.
			body["caused_by_endpoint"] = cause.EndpointKey
		}
	}

	class := o.commandClass
	if !o.classSet {
		class = b.declaredCommandClass(keys)
	}
	if class != "" && class != CommandAbsolute {
		body["command_class"] = string(class)
	}

	if o.correlationID != "" {
		body["correlation_id"] = o.correlationID
	}
	if o.ttlMs > 0 {
		body["ttl_ms"] = o.ttlMs
	}
	if o.requireAck {
		body["require_ack"] = true
	}
	if o.stateSync {
		body["state_sync"] = true
	}
	if o.wantsOwnEcho {
		body["wants_own_echo"] = true
	}
}

// declaredCommandClass reads the class off the endpoints being published from,
// so an author declares it once rather than at every call site.
//
// If the endpoints in one send disagree — which would mean mixing a step and a
// value in a single publish — the safe answer is ABSOLUTE: collapsing a value
// is harmless, while treating a value as a step would add levels together.
func (b *EndpointBus) declaredCommandClass(keys []string) CommandClass {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if len(b.declaredClasses) == 0 {
		return CommandAbsolute
	}

	var found CommandClass
	for _, k := range keys {
		c, ok := b.declaredClasses[k]
		if !ok {
			c = CommandAbsolute
		}
		if found == "" {
			found = c
			continue
		}
		if found != c {
			return CommandAbsolute
		}
	}
	if found == "" {
		return CommandAbsolute
	}
	return found
}

func (b *EndpointBus) sendRaw(ctx context.Context, payloadBody map[string]any) (*SendResultEndpoints, error) {
	body, err := json.Marshal(payloadBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/v1/endpoint-messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("send on endpoints: %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}

	var out SendResultEndpoints
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Start begins receiving batches. Existing queue content is skipped: a driver
// that has just started should act on what happens next, not replay commands
// issued while it was down.
func (b *EndpointBus) Start(ctx context.Context) error {
	b.fastForward()
	b.wg.Add(1)
	go b.pollLoop()
	return nil
}

func (b *EndpointBus) Stop(ctx context.Context) error {
	close(b.stopCh)
	b.wg.Wait()
	return nil
}

func (b *EndpointBus) fastForward() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches, err := b.poll(ctx, b.afterID, 0, 500)
	if err != nil {
		return
	}
	for _, m := range batches {
		if m.ID > b.afterID {
			b.afterID = m.ID
		}
	}
}

func (b *EndpointBus) pollLoop() {
	defer b.wg.Done()

	for {
		select {
		case <-b.stopCh:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		batches, err := b.poll(ctx, b.afterID, 10*time.Second, 200)
		cancel()

		if err != nil {
			if b.logger != nil {
				b.logger.Debug("endpoint bus poll failed", "err", err.Error())
			}
			select {
			case <-b.stopCh:
				return
			case <-time.After(750 * time.Millisecond):
			}
			continue
		}

		for _, batch := range batches {
			if batch.ID > b.afterID {
				b.afterID = batch.ID
			}
			b.dispatch(batch)
		}
	}
}

// poll reads this device's queue. Deliveries are keyed by device id, so a driver
// receives only what was addressed to the device it runs.
func (b *EndpointBus) poll(ctx context.Context, afterID int64, wait time.Duration, limit int) ([]EndpointBatch, error) {
	url := fmt.Sprintf("%s/v1/driver-messages?driver_id=%s&after_id=%d&limit=%d",
		b.baseURL, b.deviceID, afterID, limit)
	if wait > 0 {
		url += fmt.Sprintf("&wait_ms=%d", int64(wait/time.Millisecond))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("poll: %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}

	var out struct {
		Messages []EndpointBatch `json:"messages"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, err
	}

	// The same queue still carries legacy driver-addressed messages during the
	// migration; those have no items and are not ours. A refusal has no items
	// either and IS ours, so it is let through on its kind — without this it
	// looks exactly like legacy traffic and every refusal the controller
	// reports is dropped here.
	batches := make([]EndpointBatch, 0, len(out.Messages))
	for _, m := range out.Messages {
		if m.Kind != KindRefusal && len(m.Items) == 0 {
			continue
		}
		batches = append(batches, m)
	}
	return batches, nil
}

func (b *EndpointBus) dispatch(batch EndpointBatch) {
	if batch.Kind == KindRefusal {
		b.dispatchRefusal(batch)
		return
	}

	b.mu.RLock()
	handlers := make([]EndpointBatchHandler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	// The handler's context carries the cause, so a relay published with this
	// context — or one derived from it — is linked to the delivery that
	// prompted it without the driver passing anything. A driver handling items
	// separately should use batch.CauseFor(item.EndpointKey) instead, so each
	// branch inherits its own history rather than the first item's.
	ctx := ContextWithCause(context.Background(), batch.Cause())

	for _, h := range handlers {
		if err := h(ctx, batch); err != nil && b.logger != nil {
			b.logger.Debug("endpoint batch handler failed", "name", batch.Name, "err", err.Error())
		}
	}
}

func (b *EndpointBus) dispatchRefusal(batch EndpointBatch) {
	refusal := Refusal{
		Reason:        batch.NackReason,
		Detail:        batch.NackDetail,
		SourceKey:     batch.NackSource,
		TargetKey:     batch.NackTarget,
		CorrelationID: batch.CorrelationID,
	}

	b.mu.RLock()
	handlers := make([]RefusalHandler, len(b.refusals))
	copy(handlers, b.refusals)
	b.mu.RUnlock()

	if len(handlers) == 0 {
		// Better in the log than nowhere: a refusal with no handler is still
		// something an installer will need to see on a support call.
		if b.logger != nil && refusal.Reason != ReasonUnbound {
			b.logger.Debug("publish refused", "reason", refusal.Reason, "detail", refusal.Detail)
		}
		return
	}

	ctx := context.Background()
	for _, h := range handlers {
		h(ctx, refusal)
	}
}
