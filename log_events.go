package driversdk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Structured log events — controller-platform docs/LOGGING.md §5.2.2.
//
// A driver's stdout is kept, but it is prose: it cannot be counted, grouped or
// learned from. EventLog sends the things worth counting as coded events, with
// attributes the controller's catalog allows for each code:
//
//	log := driversdk.NewEventLogFromEnv()
//	defer log.Close()
//	log.Warn(ctx, driversdk.EvDeviceUnreachable, map[string]any{"since_ms": 12000, "attempt": 3})
//	log.Event(ctx, "x.com.acme.lamp.relay_stuck", map[string]any{"relay": 2})
//
// What a driver does NOT send, because core fills it in from the process the
// event came from: its package, version and device. A driver cannot speak as
// another, and cannot write facts core records about it (driver.exited and the
// like are the supervisor's).
//
// Codes come from the controller's catalog (schemas/log-codes.yaml). A driver
// may use its own only under x.<its package>.<event>; those stay on the
// controller. Attributes outside a code's allowlist are dropped there, and
// free text is never kept — put a reason in an enum, not in a sentence.
//
// The trace: an event logged with the context an OnEndpointBatch handler was
// given carries that delivery's trace, so it lands in the same episode as the
// press that caused it. Driver authors do nothing for that.
//
// Never blocks the driver. Events are queued (1,000 at most, oldest kept) and
// sent in batches; a full queue drops and counts. Core also budgets each driver
// (20/s, burst 200) and says so in its own log when one goes over.

// Catalog codes a driver may send (docs/LOGGING.md §4.4, driver.* from the SDK).
const (
	EvConnectOK         = "driver.connect_ok"
	EvConnectFailed     = "driver.connect_failed"
	EvAuthFailed        = "driver.auth_failed"
	EvDeviceUnreachable = "driver.device_unreachable"
	EvDeviceRecovered   = "driver.device_recovered"
	EvProtocolError     = "driver.protocol_error"
	EvPollSlow          = "driver.poll_slow"
	EvConfigInvalid     = "driver.config_invalid"
)

// EventTokenEnv is where the controller puts this process's event token.
const EventTokenEnv = "NOTRIX_DRIVER_EVENT_TOKEN"

// LogEvent is one event on the wire.
type LogEvent struct {
	Code      string         `json:"code"`
	Level     string         `json:"level,omitempty"`
	Attrs     map[string]any `json:"attrs,omitempty"`
	TS        int64          `json:"ts,omitempty"`
	Trace     uint64         `json:"trace,omitempty"`
	CausedBy  int64          `json:"caused_by,omitempty"`
	Endpoint  string         `json:"endpoint,omitempty"`
	Outcome   string         `json:"outcome,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	LatencyUS int64          `json:"latency_us,omitempty"`
}

// EventOption adds a common field to one event.
type EventOption func(*LogEvent)

// WithEndpoint names the endpoint the event is about.
func WithEndpoint(key string) EventOption { return func(e *LogEvent) { e.Endpoint = key } }

// WithOutcome records an outcome as a fact, with a reason from the catalog's
// closed list (TIMEOUT, DEVICE_OFFLINE, AUTH, …).
func WithOutcome(outcome, reason string) EventOption {
	return func(e *LogEvent) { e.Outcome, e.Reason = outcome, reason }
}

// WithLatency records how long the thing took.
func WithLatency(d time.Duration) EventOption {
	return func(e *LogEvent) { e.LatencyUS = d.Microseconds() }
}

// EventLog sends structured events to the controller.
type EventLog struct {
	url    string
	token  string
	client *http.Client

	mu      sync.Mutex
	queue   []LogEvent
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	dropped atomic.Int64
	closed  atomic.Bool
}

const (
	eventQueueMax  = 1000
	eventBatchMax  = 100
	eventFlushTick = time.Second
)

// NewEventLogFromEnv builds a log from the environment the controller starts a
// driver with. Without a token — a driver run by hand — events are dropped
// silently: logging must never be the reason a driver will not start.
func NewEventLogFromEnv() *EventLog {
	base := ""
	for _, env := range []string{"CORE_HTTP_ADDR", "CONTROLLER_CORE_HTTP_ADDR"} {
		if base = strings.TrimSpace(os.Getenv(env)); base != "" {
			break
		}
	}
	if base == "" {
		base = "http://127.0.0.1:8090"
	}
	return NewEventLog(strings.TrimRight(base, "/"), os.Getenv(EventTokenEnv), nil)
}

// NewEventLog builds a log posting to baseURL with token.
func NewEventLog(baseURL, token string, client *http.Client) *EventLog {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	l := &EventLog{
		url: baseURL + "/v1/drivers/self/events", token: strings.TrimSpace(token), client: client,
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go l.run()
	return l
}

// Event logs one event at the code's own level.
func (l *EventLog) Event(ctx context.Context, code string, attrs map[string]any, opts ...EventOption) {
	l.emit(ctx, code, "", attrs, opts)
}

// Warn logs one event at WARN (or the code's floor, if higher).
func (l *EventLog) Warn(ctx context.Context, code string, attrs map[string]any, opts ...EventOption) {
	l.emit(ctx, code, "WARN", attrs, opts)
}

// Error logs one event at ERROR.
func (l *EventLog) Error(ctx context.Context, code string, attrs map[string]any, opts ...EventOption) {
	l.emit(ctx, code, "ERROR", attrs, opts)
}

// Dropped is how many events this process dropped because its queue was full.
func (l *EventLog) Dropped() int64 { return l.dropped.Load() }

func (l *EventLog) emit(ctx context.Context, code, level string, attrs map[string]any, opts []EventOption) {
	if l == nil || l.token == "" || l.closed.Load() {
		return
	}
	e := LogEvent{Code: code, Level: level, Attrs: attrs, TS: time.Now().UnixMilli()}
	if t, ok := TraceFromContext(ctx); ok {
		e.Trace = t
	}
	if c, ok := CauseFromContext(ctx); ok {
		e.CausedBy = c.MessageID
	}
	for _, o := range opts {
		o(&e)
	}
	l.mu.Lock()
	if len(l.queue) >= eventQueueMax {
		l.mu.Unlock()
		l.dropped.Add(1)
		return
	}
	l.queue = append(l.queue, e)
	full := len(l.queue) >= eventBatchMax
	l.mu.Unlock()
	if full {
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
}

func (l *EventLog) run() {
	defer close(l.done)
	t := time.NewTicker(eventFlushTick)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			l.flush()
			return
		case <-t.C:
			l.flush()
		case <-l.wake:
			l.flush()
		}
	}
}

func (l *EventLog) flush() {
	for {
		l.mu.Lock()
		n := len(l.queue)
		if n == 0 {
			l.mu.Unlock()
			return
		}
		if n > eventBatchMax {
			n = eventBatchMax
		}
		batch := append([]LogEvent(nil), l.queue[:n]...)
		l.queue = l.queue[n:]
		l.mu.Unlock()

		body, _ := json.Marshal(map[string]any{"events": batch})
		req, err := http.NewRequest(http.MethodPost, l.url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Notrix-Driver-Token", l.token)
		resp, err := l.client.Do(req)
		if err != nil {
			// The controller is restarting or busy: these are dropped, not
			// retried, so a driver never builds up a backlog it cannot send.
			l.dropped.Add(int64(len(batch)))
			return
		}
		_ = resp.Body.Close()
	}
}

// Close sends what is queued and stops.
func (l *EventLog) Close() {
	if l == nil || !l.closed.CompareAndSwap(false, true) {
		return
	}
	close(l.stop)
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
	}
}

// ── Trace in context ──────────────────────────────────────────────────

type traceCtxKey struct{}

// ContextWithTrace attaches the episode a delivery belonged to. The batch
// handler's context already carries it; this is for work that continues later.
func ContextWithTrace(ctx context.Context, trace uint64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if trace == 0 {
		return ctx
	}
	return context.WithValue(ctx, traceCtxKey{}, trace)
}

// TraceFromContext reads the episode a context belongs to.
func TraceFromContext(ctx context.Context) (uint64, bool) {
	if ctx == nil {
		return 0, false
	}
	t, ok := ctx.Value(traceCtxKey{}).(uint64)
	return t, ok && t != 0
}
