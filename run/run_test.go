package run

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	driversdk "github.com/NotrixInc/nx-driver-sdk"
)

type fakeInstance struct {
	id      string
	log     driversdk.Logger
	started *sync.Map
	stopped *sync.Map
	panicOn string
}

func (f *fakeInstance) Init(_ context.Context, deps driversdk.Dependencies, cfg driversdk.JSONConfig) error {
	f.log = deps.Logger
	var c struct {
		Name string `json:"name"`
	}
	return cfg.Decode(&c)
}

func (f *fakeInstance) Start(context.Context) error {
	if f.panicOn == "start" {
		panic("boom")
	}
	f.log.Info("hello from " + f.id)
	f.started.Store(f.id, true)
	return nil
}

func (f *fakeInstance) Stop(context.Context) error {
	f.stopped.Store(f.id, true)
	return nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func testProgram(started, stopped *sync.Map, keys *sync.Map) Program {
	return Program{
		DriverID: "com.example.test",
		New: func(d Device) (Instance, error) {
			f := &fakeInstance{id: d.ID, started: started, stopped: stopped}
			if d.ID == "dev-panic" {
				f.panicOn = "start"
			}
			return f, nil
		},
		Publisher: func(_, driverID, key string, _ driversdk.Logger) (driversdk.Publisher, io.Closer, error) {
			keys.Store(key, driverID)
			return driversdk.NewNoopPublisher(), nopCloser{}, nil
		},
	}
}

func writeConfig(t *testing.T, dir, id string) string {
	t.Helper()
	p := filepath.Join(dir, id+".json")
	if err := os.WriteFile(p, []byte(`{"name":"`+id+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func cmdLine(t *testing.T, c command) string {
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// Several devices run in one host; each one's log lines carry its id; a device
// whose start panics fails alone; closing stdin stops everything.
func TestHostRunsSeveralDevicesAndIsolatesAFailingOne(t *testing.T) {
	t.Setenv("CORE_GRPC_ADDR", "127.0.0.1:1")
	dir := t.TempDir()
	var started, stopped, keys sync.Map

	var in strings.Builder
	in.WriteString(cmdLine(t, command{Op: "start", DeviceID: "dev-a", ConfigPath: writeConfig(t, dir, "dev-a"), ExternalKey: "ip:10.0.0.9"}))
	in.WriteString(cmdLine(t, command{Op: "start", DeviceID: "dev-b", ConfigPath: writeConfig(t, dir, "dev-b")}))
	in.WriteString(cmdLine(t, command{Op: "start", DeviceID: "dev-panic", ConfigPath: writeConfig(t, dir, "dev-panic")}))
	in.WriteString(cmdLine(t, command{Op: "start", DeviceID: "dev-noconfig", ConfigPath: filepath.Join(dir, "missing.json")}))

	var out bytes.Buffer
	h := newHost(testProgram(&started, &stopped, &keys), &out)
	if err := h.serve(strings.NewReader(in.String())); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"dev-a", "dev-b"} {
		if _, ok := started.Load(id); !ok {
			t.Errorf("%s did not start", id)
		}
		if _, ok := stopped.Load(id); !ok {
			t.Errorf("%s was not stopped when stdin closed", id)
		}
	}
	if _, ok := keys.Load("ip:10.0.0.9"); !ok {
		t.Error("the external key core passed was not used")
	}
	if _, ok := keys.Load("dev-b"); !ok {
		t.Error("a device with no external key did not fall back to its id")
	}

	text := out.String()
	for _, want := range []string{
		`@@nxhost {"event":"started","device_id":"dev-a"}`,
		`@@nxhost {"event":"started","device_id":"dev-b"}`,
		`@@nxhost {"event":"stopped","device_id":"dev-a"}`,
		`"event":"failed","device_id":"dev-panic","error":"panic starting device: boom"`,
		`"event":"failed","device_id":"dev-noconfig"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %s\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "hello from dev-a") && !strings.HasPrefix(line, "@dev-a ") {
			t.Errorf("a device's log line does not carry its id: %q", line)
		}
	}
}

// A stop for a device waits for its start, and a stop for a device that is not
// running still answers, so core is never left waiting.
func TestHostStopOrderingAndUnknownStop(t *testing.T) {
	t.Setenv("CORE_GRPC_ADDR", "127.0.0.1:1")
	dir := t.TempDir()
	var started, stopped, keys sync.Map

	in := cmdLine(t, command{Op: "start", DeviceID: "dev-a", ConfigPath: writeConfig(t, dir, "dev-a")}) +
		cmdLine(t, command{Op: "stop", DeviceID: "dev-a"}) +
		cmdLine(t, command{Op: "stop", DeviceID: "dev-never"})

	var out bytes.Buffer
	if err := newHost(testProgram(&started, &stopped, &keys), &out).serve(strings.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Index(text, `"started","device_id":"dev-a"`) > strings.Index(text, `"stopped","device_id":"dev-a"`) {
		t.Errorf("dev-a was stopped before it started:\n%s", text)
	}
	if !strings.Contains(text, `"stopped","device_id":"dev-never"`) {
		t.Errorf("a stop for a device not running was not answered:\n%s", text)
	}
}
