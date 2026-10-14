package driversdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestEventLogSendsWithTokenAndTrace(t *testing.T) {
	var mu sync.Mutex
	var got []LogEvent
	var token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []LogEvent `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got = append(got, body.Events...)
		token = r.Header.Get("X-Notrix-Driver-Token")
		mu.Unlock()
	}))
	defer srv.Close()

	l := NewEventLog(srv.URL, "tok-1", nil)
	ctx := ContextWithTrace(ContextWithCause(context.Background(), Cause{MessageID: 9}), 4242)
	l.Warn(ctx, EvDeviceUnreachable, map[string]any{"since_ms": 1200}, WithEndpoint("ch1"))
	l.Close()

	mu.Lock()
	defer mu.Unlock()
	if token != "tok-1" || len(got) != 1 {
		t.Fatalf("token %q, %d events", token, len(got))
	}
	e := got[0]
	if e.Code != EvDeviceUnreachable || e.Level != "WARN" || e.Trace != 4242 || e.CausedBy != 9 || e.Endpoint != "ch1" {
		t.Fatalf("event %+v", e)
	}
}

func TestEventLogWithoutTokenIsSilent(t *testing.T) {
	l := NewEventLog("http://127.0.0.1:1", "", nil)
	l.Event(context.Background(), EvConnectOK, nil)
	l.Close()
	if l.Dropped() != 0 {
		t.Fatal("a driver run by hand should drop nothing and send nothing")
	}
}
