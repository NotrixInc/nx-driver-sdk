// Package run is a driver binary's whole main (DRIVERS-SDK.md §5.1, C9).
//
//	func main() {
//		run.Main(run.Program{
//			DriverID:  driver.DriverID,
//			New:       func(d run.Device) (run.Instance, error) { return driver.New(d.ID), nil },
//			Publisher: publisher.Factory,
//		})
//	}
//
// It runs the binary one of two ways, chosen by controller-core:
//
//   - Standalone, `driver -device_id <id> -config <path>`: one device per
//     process, exactly as every driver ran before. A driver built on this
//     package still works on a controller that only knows this way.
//
//   - Hosted, `driver -host`: every device of the package in one process.
//     Core starts and stops devices with one JSON line each on stdin and reads
//     what happened from stdout (protocol below). Forty lights are one process
//     instead of forty, each of which carried its own Go runtime, gRPC
//     connection and timers.
//
// Hosted protocol. Core → host, one JSON object per line on stdin:
//
//	{"op":"start","device_id":"…","config_path":"…","external_key":"…","ip":"…","event_token":"…"}
//	{"op":"stop","device_id":"…"}
//
// Host → core, on stdout:
//
//	@@nxhost {"event":"started","device_id":"…"}
//	@@nxhost {"event":"stopped","device_id":"…"}
//	@@nxhost {"event":"failed","device_id":"…","error":"…"}
//
// and every line a device logs is prefixed "@<device_id> " so core files it
// under that device. When stdin closes — core went away — every device is
// stopped and the process exits.
//
// What hosting cannot do is survive a panic on a goroutine a driver started
// itself: Go ends the process, and core restarts the host with all its
// devices. Init, Start and Stop are run under recover, so a driver failing
// there fails only its own device.
package run

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	driversdk "github.com/NotrixInc/nx-driver-sdk"
)

// Instance is one device's driver, as every driver already implements it.
type Instance interface {
	Init(ctx context.Context, deps driversdk.Dependencies, cfg driversdk.JSONConfig) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Device is what the controller says about one device when it starts it.
type Device struct {
	ID          string
	ConfigPath  string
	Config      []byte
	ExternalKey string
	IP          string
	EventToken  string
}

// PublisherFactory connects one device to controller-core. The returned
// closer is called when the device stops.
type PublisherFactory func(coreAddr, driverID, externalKey string, log driversdk.Logger) (driversdk.Publisher, io.Closer, error)

// Program describes a driver binary.
type Program struct {
	// DriverID is the package id, used when the controller does not pass one.
	DriverID string
	// New builds the instance for one device.
	New func(d Device) (Instance, error)
	// Publisher connects a device to core.
	Publisher PublisherFactory
	// ExternalKey is the key a device publishes under when the controller
	// passed none. Nil means the device id.
	ExternalKey func(d Device) (string, error)
}

// HostFlag is the flag controller-core passes to run a package hosted.
const HostFlag = "host"

const (
	stopTimeout = 5 * time.Second
	eventPrefix = "@@nxhost "
)

// Main runs the program and exits.
func Main(p Program) {
	deviceID := flag.String("device_id", "", "device UUID (standalone)")
	cfgPath := flag.String("config", "", "path to the device's config JSON (standalone)")
	hosted := flag.Bool(HostFlag, false, "serve every device of the package, as controller-core directs on stdin")
	flag.Parse()

	var err error
	if *hosted {
		err = newHost(p, os.Stdout).serve(os.Stdin)
	} else {
		err = standalone(p, *deviceID, *cfgPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "driver:", err)
		os.Exit(1)
	}
}

func coreGRPCAddr() string {
	return firstEnv("CORE_GRPC_ADDR", "CONTROLLER_CORE_GRPC_ADDR", "GRPC_ADDR")
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

func (p Program) driverID() string {
	if id := firstEnv("DRIVER_ID"); id != "" {
		return id
	}
	return p.DriverID
}

func (p Program) externalKey(d Device) (string, error) {
	if k := strings.TrimSpace(d.ExternalKey); k != "" {
		return k, nil
	}
	if p.ExternalKey != nil {
		return p.ExternalKey(d)
	}
	return d.ID, nil
}

// ── Standalone ───────────────────────────────────────────────────────────

func standalone(p Program, deviceID, cfgPath string) error {
	if deviceID == "" {
		return errors.New("missing -device_id")
	}
	if cfgPath == "" {
		return errors.New("missing -config path")
	}
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	d := Device{
		ID: deviceID, ConfigPath: cfgPath, Config: cfg,
		ExternalKey: firstEnv("EXTERNAL_DEVICE_KEY"),
		IP:          firstEnv("DEVICE_IP"),
		EventToken:  firstEnv(driversdk.EventTokenEnv),
	}

	log := driversdk.NewStdLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dev, err := startDevice(ctx, p, d, log)
	if err != nil {
		return err
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	dev.stop()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "stopped"})
	return nil
}

// ── One device ───────────────────────────────────────────────────────────

type device struct {
	inst   Instance
	closer io.Closer
	log    driversdk.Logger
}

// startDevice builds, initialises and starts one device. A panic in the
// driver's own Init or Start is returned as an error, not a crash.
func startDevice(ctx context.Context, p Program, d Device, log driversdk.Logger) (dev *device, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic starting device: %v", r)
		}
	}()

	key, err := p.externalKey(d)
	if err != nil {
		return nil, err
	}
	addr := coreGRPCAddr()
	if addr == "" {
		return nil, errors.New("missing CORE_GRPC_ADDR")
	}
	pub, closer, err := p.Publisher(addr, p.driverID(), key, log)
	if err != nil {
		return nil, err
	}

	inst, err := p.New(d)
	if err != nil {
		_ = closer.Close()
		return nil, err
	}
	deps := driversdk.Dependencies{Publisher: pub, Logger: log, Clock: driversdk.NewSystemClock()}
	if err := inst.Init(ctx, deps, driversdk.NewJSONConfig(d.Config)); err != nil {
		_ = closer.Close()
		return nil, fmt.Errorf("init: %w", err)
	}
	if err := inst.Start(ctx); err != nil {
		_ = closer.Close()
		return nil, fmt.Errorf("start: %w", err)
	}
	return &device{inst: inst, closer: closer, log: log}, nil
}

func (d *device) stop() {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error("panic stopping device", "panic", fmt.Sprint(r))
		}
		_ = d.closer.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	_ = d.inst.Stop(ctx)
}

// ── Hosted ───────────────────────────────────────────────────────────────

type host struct {
	p   Program
	out *lineWriter

	mu      sync.Mutex
	devices map[string]*device
	// queues hold each device's commands, run in order by one worker per
	// device: a stop that arrives while its start is still running waits for
	// it, and a device slow to start does not hold up the others.
	queues map[string]chan command
	wg     sync.WaitGroup
}

type command struct {
	Op          string `json:"op"`
	DeviceID    string `json:"device_id"`
	ConfigPath  string `json:"config_path"`
	ExternalKey string `json:"external_key"`
	IP          string `json:"ip"`
	EventToken  string `json:"event_token"`
}

func newHost(p Program, out io.Writer) *host {
	return &host{
		p:       p,
		out:     &lineWriter{w: out},
		devices: map[string]*device{},
		queues:  map[string]chan command{},
	}
}

// serve runs commands from in until it closes or the process is told to
// stop, then stops every device.
func (h *host) serve(in io.Reader) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	lines := make(chan []byte)
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(lines)
	}()

loop:
	for {
		select {
		case <-sig:
			break loop
		case line, ok := <-lines:
			if !ok {
				break loop
			}
			h.handle(line)
		}
	}
	h.mu.Lock()
	for _, q := range h.queues {
		close(q)
	}
	h.queues = map[string]chan command{}
	h.mu.Unlock()
	h.wg.Wait()
	h.stopAll()
	return nil
}

func (h *host) handle(line []byte) {
	if len(strings.TrimSpace(string(line))) == 0 {
		return
	}
	var c command
	if err := json.Unmarshal(line, &c); err != nil || c.DeviceID == "" {
		h.out.line(fmt.Sprintf("host: ignoring a command it cannot read: %q", line))
		return
	}
	h.queue(c.DeviceID) <- c
}

// queue returns a device's command queue, starting its worker on first use.
func (h *host) queue(id string) chan command {
	h.mu.Lock()
	defer h.mu.Unlock()
	q := h.queues[id]
	if q == nil {
		q = make(chan command, 64)
		h.queues[id] = q
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			for c := range q {
				switch c.Op {
				case "start":
					h.start(c)
				case "stop":
					h.stop(c.DeviceID)
				default:
					h.out.line(fmt.Sprintf("host: unknown op %q", c.Op))
				}
			}
		}()
	}
	return q
}

func (h *host) start(c command) {
	h.mu.Lock()
	_, running := h.devices[c.DeviceID]
	h.mu.Unlock()
	if running {
		h.event("started", c.DeviceID, "")
		return
	}

	cfg, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		h.event("failed", c.DeviceID, err.Error())
		return
	}
	d := Device{
		ID: c.DeviceID, ConfigPath: c.ConfigPath, Config: cfg,
		ExternalKey: c.ExternalKey, IP: c.IP, EventToken: c.EventToken,
	}
	log := &deviceLogger{id: c.DeviceID, out: h.out}
	dev, err := startDevice(context.Background(), h.p, d, log)
	if err != nil {
		h.event("failed", c.DeviceID, err.Error())
		return
	}
	h.mu.Lock()
	h.devices[c.DeviceID] = dev
	h.mu.Unlock()
	h.event("started", c.DeviceID, "")
}

func (h *host) stop(id string) {
	h.mu.Lock()
	dev := h.devices[id]
	delete(h.devices, id)
	h.mu.Unlock()
	if dev != nil {
		dev.stop()
	}
	h.event("stopped", id, "")
}

func (h *host) stopAll() {
	h.mu.Lock()
	ids := make([]string, 0, len(h.devices))
	for id := range h.devices {
		ids = append(ids, id)
	}
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			h.stop(id)
		}(id)
	}
	wg.Wait()
}

func (h *host) event(kind, id, errText string) {
	b, _ := json.Marshal(struct {
		Event    string `json:"event"`
		DeviceID string `json:"device_id"`
		Error    string `json:"error,omitempty"`
	}{kind, id, errText})
	h.out.line(eventPrefix + string(b))
}

// ── Output ───────────────────────────────────────────────────────────────

// lineWriter writes whole lines, one at a time, so lines from devices logging
// at once never interleave.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) line(s string) {
	s = strings.TrimRight(s, "\n")
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.w, s+"\n")
}

// deviceLogger is a device's logger in a hosted process: every line carries
// the device id, which core strips and files the line under.
type deviceLogger struct {
	id  string
	out *lineWriter
}

func (d *deviceLogger) Debug(msg string, kv ...any) { d.print("DEBUG", msg, kv) }
func (d *deviceLogger) Info(msg string, kv ...any)  { d.print("INFO", msg, kv) }
func (d *deviceLogger) Warn(msg string, kv ...any)  { d.print("WARN", msg, kv) }
func (d *deviceLogger) Error(msg string, kv ...any) { d.print("ERROR", msg, kv) }

func (d *deviceLogger) print(level, msg string, kv []any) {
	ts := time.Now().Format("2006/01/02 15:04:05.000000")
	line := fmt.Sprintf("@%s %s %s %s", d.id, ts, level, msg)
	if len(kv) > 0 {
		line += fmt.Sprintf(" %v", kv)
	}
	// One device's message is one line, so a newline inside it cannot be
	// mistaken for a line of another device's.
	d.out.line(strings.ReplaceAll(line, "\n", " "))
}
