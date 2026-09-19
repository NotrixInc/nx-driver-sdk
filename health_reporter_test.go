package driversdk

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// fakeDriver implements just enough of Driver to be probed. Health is the only
// method the reporter touches; the rest exist to satisfy the interface.
type fakeDriver struct {
	health func(ctx context.Context) (HealthStatus, map[string]string)
}

func (f *fakeDriver) ID() string            { return "test.driver" }
func (f *fakeDriver) Version() string       { return "0.0.1" }
func (f *fakeDriver) Type() DriverType      { return DriverTypeDevice }
func (f *fakeDriver) Protocols() []Protocol { return []Protocol{ProtocolIP} }
func (f *fakeDriver) Topologies() []Topology {
	return []Topology{TopologyDirectIP}
}
func (f *fakeDriver) Init(ctx context.Context, deps Dependencies, cfg JSONConfig) error { return nil }
func (f *fakeDriver) Start(ctx context.Context) error                                   { return nil }
func (f *fakeDriver) Stop(ctx context.Context) error                                    { return nil }
func (f *fakeDriver) Health(ctx context.Context) (HealthStatus, map[string]string) {
	return f.health(ctx)
}
func (f *fakeDriver) HandleCommand(ctx context.Context, cmd Command) (CommandResult, error) {
	return CommandResult{}, nil
}
func (f *fakeDriver) Endpoints() ([]Endpoint, error) { return nil, nil }
func (f *fakeDriver) Variables() ([]Variable, error) { return nil, nil }

// capturePublisher records what was published.
type capturePublisher struct {
	Publisher
	mu   sync.Mutex
	vars []VariableUpdate
}

func (c *capturePublisher) PublishVariable(ctx context.Context, v VariableUpdate) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vars = append(c.vars, v)
	return nil
}

func (c *capturePublisher) got() []VariableUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]VariableUpdate, len(c.vars))
	copy(out, c.vars)
	return out
}

func TestProbeReportsHealthyDriver(t *testing.T) {
	drv := &fakeDriver{health: func(ctx context.Context) (HealthStatus, map[string]string) {
		return HealthOK, map[string]string{"reason": "all good"}
	}}
	pub := &capturePublisher{}
	h := NewHealthReporter("dev-1", drv, pub, HealthReporterOptions{})

	rep := h.ProbeAndPublish(context.Background())
	if rep.Status != HealthOK {
		t.Fatalf("status = %q, want OK", rep.Status)
	}
	if rep.Reason != "all good" {
		t.Fatalf("reason = %q, want %q", rep.Reason, "all good")
	}

	got := pub.got()
	if len(got) != 1 {
		t.Fatalf("published %d variables, want 1", len(got))
	}
	if got[0].Key != HealthMetricKey {
		t.Fatalf("key = %q, want %q", got[0].Key, HealthMetricKey)
	}
	var round HealthReport
	if err := json.Unmarshal(got[0].Value, &round); err != nil {
		t.Fatalf("payload does not parse: %v", err)
	}
	if round.Status != HealthOK {
		t.Fatalf("round-tripped status = %q, want OK", round.Status)
	}
}

// The point of the whole file: a driver that wedges must be reported as DOWN
// rather than hanging the reporter. Before this existed, a wedged driver was
// indistinguishable from a healthy one.
func TestProbeTimesOutOnWedgedDriver(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	drv := &fakeDriver{health: func(ctx context.Context) (HealthStatus, map[string]string) {
		<-release // never returns within the probe timeout
		return HealthOK, nil
	}}
	h := NewHealthReporter("dev-1", drv, &capturePublisher{}, HealthReporterOptions{
		Timeout: 50 * time.Millisecond,
	})

	start := time.Now()
	rep := h.Probe(context.Background())
	elapsed := time.Since(start)

	if rep.Status != HealthDown {
		t.Fatalf("status = %q, want DOWN for a wedged driver", rep.Status)
	}
	if rep.Reason == "" {
		t.Fatal("a timed-out probe must carry a reason")
	}
	if elapsed > time.Second {
		t.Fatalf("probe took %s; it must be bounded by the timeout", elapsed)
	}
}

// A third party writes Health(). A panic there must not reach the host.
func TestProbeSurvivesPanickingDriver(t *testing.T) {
	drv := &fakeDriver{health: func(ctx context.Context) (HealthStatus, map[string]string) {
		panic("driver exploded")
	}}
	h := NewHealthReporter("dev-1", drv, &capturePublisher{}, HealthReporterOptions{})

	rep := h.Probe(context.Background())
	if rep.Status != HealthDown {
		t.Fatalf("status = %q, want DOWN after a panic", rep.Status)
	}
	if rep.Reason == "" {
		t.Fatal("a panicking probe must carry a reason")
	}
}

// A driver that returns nothing meaningful is not reported as well.
func TestUnknownStatusBecomesDegraded(t *testing.T) {
	drv := &fakeDriver{health: func(ctx context.Context) (HealthStatus, map[string]string) {
		return HealthStatus("whatever"), nil
	}}
	h := NewHealthReporter("dev-1", drv, &capturePublisher{}, HealthReporterOptions{})

	if got := h.Probe(context.Background()).Status; got != HealthDegraded {
		t.Fatalf("status = %q, want DEGRADED for an unrecognised value", got)
	}
}

func TestStartStopPublishesAndExits(t *testing.T) {
	drv := &fakeDriver{health: func(ctx context.Context) (HealthStatus, map[string]string) {
		return HealthOK, nil
	}}
	pub := &capturePublisher{}
	h := NewHealthReporter("dev-1", drv, pub, HealthReporterOptions{
		Interval: 10 * time.Millisecond,
	})

	h.Start(context.Background())
	time.Sleep(60 * time.Millisecond)
	h.Stop()
	h.Wait()

	if n := len(pub.got()); n < 2 {
		t.Fatalf("published %d reports over ~6 intervals, want at least 2", n)
	}
	if _, at := h.Last(); at.IsZero() {
		t.Fatal("Last() should carry a timestamp after probing")
	}
}

func TestParseHealthReport(t *testing.T) {
	rep, err := ParseHealthReport([]byte(`{"status":"DOWN","reason":"no route to device"}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if rep.Status != HealthDown || rep.Reason != "no route to device" {
		t.Fatalf("got %+v", rep)
	}
	if _, err := ParseHealthReport([]byte(`not json`)); err == nil {
		t.Fatal("expected an error on malformed input")
	}
}
