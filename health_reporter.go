package driversdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Driver.Health() has been part of this interface since the beginning and, until
// this file, nothing in the platform ever called it. The supervisor in
// controller-core noticed a driver process *exiting* and nothing else, so a
// driver that wedged — a synchronous device read with no timeout, a deadlock, a
// device that accepts a connection and never answers — stayed "running" forever
// and its devices stayed on screen looking healthy.
//
// HealthReporter is the thing that calls it. It runs one goroutine per driver
// instance, probes Health() on an interval, and publishes the answer to core as
// the reserved `_health` metric.
//
// Three properties matter more than the reporting itself, because Health() is
// driver code and a third party wrote it:
//
//   - a probe that blocks must not block the reporter, so each probe runs in its
//     own goroutine and is abandoned on timeout;
//   - a probe that panics must not take down the host, so every call is wrapped
//     in recover();
//   - a probe that times out is itself the signal core needs — that is exactly
//     what a hung driver looks like from outside — so it is reported as DOWN
//     rather than swallowed.
//
// Transport note: `_health` travels over PublishVariable (the telemetry path)
// rather than an RPC of its own, following the existing `_poll_ok`/`_poll_err`
// convention in core_service.go. That keeps this change out of the proto
// definitions entirely. When the driver contract is next revised it should
// become a first-class call.
const (
	// HealthMetricKey is the reserved telemetry metric carrying a health report.
	HealthMetricKey = "_health"

	// DefaultHealthInterval is how often Health() is probed when the caller does
	// not say. Frequent enough that a wedged driver is noticed inside a minute,
	// rare enough to be invisible next to ordinary polling.
	DefaultHealthInterval = 15 * time.Second

	// DefaultHealthTimeout bounds one probe. A driver that cannot answer
	// "are you alive" within this has already failed the question.
	DefaultHealthTimeout = 3 * time.Second
)

// HealthReport is the payload published under HealthMetricKey. Core parses this
// shape; keep it additive.
type HealthReport struct {
	Status HealthStatus      `json:"status"`
	Reason string            `json:"reason,omitempty"`
	Detail map[string]string `json:"detail,omitempty"`
}

// HealthReporterOptions configures a reporter. The zero value is usable.
type HealthReporterOptions struct {
	// Interval between probes. Defaults to DefaultHealthInterval.
	Interval time.Duration
	// Timeout bounds a single probe. Defaults to DefaultHealthTimeout.
	Timeout time.Duration
	// Logger is optional.
	Logger Logger
	// Clock is optional; defaults to the system clock.
	Clock Clock
	// OnReport, if set, is called with every report before it is published.
	// Useful for tests and for drivers that want to react to their own health.
	OnReport func(HealthReport)
}

// HealthReporter probes a driver's Health() and publishes the result.
type HealthReporter struct {
	deviceID string
	drv      Driver
	pub      Publisher
	opts     HealthReporterOptions

	mu         sync.RWMutex
	last       HealthReport
	lastAt     time.Time
	probeCount int64

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// NewHealthReporter builds a reporter for one driver instance. deviceID is the
// core device UUID the report is about.
func NewHealthReporter(deviceID string, drv Driver, pub Publisher, opts HealthReporterOptions) *HealthReporter {
	if opts.Interval <= 0 {
		opts.Interval = DefaultHealthInterval
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultHealthTimeout
	}
	if opts.Clock == nil {
		opts.Clock = NewSystemClock()
	}
	return &HealthReporter{
		deviceID: deviceID,
		drv:      drv,
		pub:      pub,
		opts:     opts,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start begins probing. It returns immediately. Probing stops when ctx is
// cancelled or Stop is called, whichever happens first.
func (h *HealthReporter) Start(ctx context.Context) {
	if h == nil || h.drv == nil {
		return
	}
	go h.run(ctx)
}

func (h *HealthReporter) run(ctx context.Context) {
	defer close(h.doneCh)

	// Report once immediately so core is not waiting a full interval to learn
	// that a freshly started driver is alive.
	h.ProbeAndPublish(ctx)

	t := time.NewTicker(h.opts.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case <-t.C:
			h.ProbeAndPublish(ctx)
		}
	}
}

// Stop ends probing. It is safe to call more than once.
func (h *HealthReporter) Stop() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() { close(h.stopCh) })
}

// Wait blocks until the reporter's loop has exited.
func (h *HealthReporter) Wait() {
	if h == nil {
		return
	}
	<-h.doneCh
}

// Last returns the most recent report and when it was taken. The zero time
// means nothing has been probed yet.
func (h *HealthReporter) Last() (HealthReport, time.Time) {
	if h == nil {
		return HealthReport{}, time.Time{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.last, h.lastAt
}

// ProbeAndPublish takes one reading and sends it. Exported so a driver can
// force a report at a moment it knows matters — straight after a reconnect, or
// when it has just given up on a device.
func (h *HealthReporter) ProbeAndPublish(ctx context.Context) HealthReport {
	rep := h.Probe(ctx)

	h.mu.Lock()
	h.last = rep
	h.lastAt = h.opts.Clock.Now()
	h.probeCount++
	at := h.lastAt
	h.mu.Unlock()

	if h.opts.OnReport != nil {
		h.opts.OnReport(rep)
	}
	h.publish(ctx, rep, at)
	return rep
}

// Probe calls Driver.Health() once, bounded and guarded. It never panics and
// never blocks longer than the configured timeout.
func (h *HealthReporter) Probe(ctx context.Context) HealthReport {
	if h == nil || h.drv == nil {
		return HealthReport{Status: HealthDown, Reason: "no driver"}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	probeCtx, cancel := context.WithTimeout(ctx, h.opts.Timeout)
	defer cancel()

	type result struct {
		status HealthStatus
		detail map[string]string
		panicV any
	}

	// Buffered so the goroutine can always finish and be collected even after
	// we have stopped waiting for it. An abandoned probe must not leak a
	// blocked send.
	resCh := make(chan result, 1)

	go func() {
		var r result
		defer func() {
			if v := recover(); v != nil {
				r = result{panicV: v}
			}
			resCh <- r
		}()
		st, detail := h.drv.Health(probeCtx)
		r = result{status: st, detail: detail}
	}()

	select {
	case r := <-resCh:
		if r.panicV != nil {
			// A driver that panics answering "are you alive" is not alive.
			reason := fmt.Sprintf("health probe panicked: %v", r.panicV)
			h.logf("error", "health probe panicked", "device_id", h.deviceID, "panic", r.panicV)
			return HealthReport{Status: HealthDown, Reason: reason}
		}
		return HealthReport{
			Status: normalizeHealthStatus(r.status),
			Reason: r.detail["reason"],
			Detail: r.detail,
		}

	case <-probeCtx.Done():
		// The distinction matters. Our own timeout means the driver is wedged
		// and core should act on it. A cancelled parent context means we are
		// shutting down and nothing is wrong.
		if ctx.Err() != nil {
			return HealthReport{Status: HealthDown, Reason: "driver stopping"}
		}
		reason := fmt.Sprintf("health probe timed out after %s", h.opts.Timeout)
		h.logf("warn", "health probe timed out", "device_id", h.deviceID, "timeout", h.opts.Timeout.String())
		return HealthReport{Status: HealthDown, Reason: reason}
	}
}

func (h *HealthReporter) publish(ctx context.Context, rep HealthReport, at time.Time) {
	if h.pub == nil {
		return
	}
	body, err := json.Marshal(rep)
	if err != nil {
		return
	}
	quality := QualityGood
	if rep.Status == HealthDown {
		quality = QualityStale
	}
	// Publishing is best effort by design: a controller we cannot reach is not
	// a reason to stop probing, and the next tick will carry a fresh reading.
	if err := h.pub.PublishVariable(ctx, VariableUpdate{
		DeviceID: h.deviceID,
		Key:      HealthMetricKey,
		Value:    body,
		Quality:  quality,
		Source:   SourceDriver,
		At:       at,
	}); err != nil {
		h.logf("debug", "publishing health failed", "device_id", h.deviceID, "err", err)
	}
}

func (h *HealthReporter) logf(level, msg string, kv ...any) {
	if h.opts.Logger == nil {
		return
	}
	switch level {
	case "error":
		h.opts.Logger.Error(msg, kv...)
	case "warn":
		h.opts.Logger.Warn(msg, kv...)
	default:
		h.opts.Logger.Debug(msg, kv...)
	}
}

// normalizeHealthStatus maps whatever a driver returned onto the three defined
// values. An unrecognised or empty status is treated as DEGRADED rather than OK:
// a driver that cannot say clearly that it is well should not be reported as
// well.
func normalizeHealthStatus(s HealthStatus) HealthStatus {
	switch s {
	case HealthOK, HealthDegraded, HealthDown:
		return s
	default:
		return HealthDegraded
	}
}

// ParseHealthReport reads a report published under HealthMetricKey. Core uses
// this; it lives here so the two sides cannot drift.
func ParseHealthReport(raw []byte) (HealthReport, error) {
	var rep HealthReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return HealthReport{}, err
	}
	rep.Status = normalizeHealthStatus(rep.Status)
	return rep, nil
}
