package driversdk

import (
	"context"
	"log"
	"os"
	"sync"
	"time"
)

// NewNoopPublisher returns a Publisher implementation that discards all messages.
// Useful for local driver development without a running controller-core.
func NewNoopPublisher() Publisher { return &noopPublisher{} }

type noopPublisher struct{}

func (n *noopPublisher) UpsertDevice(ctx context.Context, d DeviceDescriptor) error { return nil }
func (n *noopPublisher) UpsertEndpoints(ctx context.Context, deviceID string, eps []Endpoint) error {
	return nil
}
func (n *noopPublisher) UpsertVariables(ctx context.Context, deviceID string, vars []Variable) error {
	return nil
}
func (n *noopPublisher) PublishState(ctx context.Context, s StateUpdate) error { return nil }
func (n *noopPublisher) PublishVariable(ctx context.Context, v VariableUpdate) error { return nil }
func (n *noopPublisher) PublishEvent(ctx context.Context, e DeviceEvent) error { return nil }

// NewStdLogger returns a simple Logger backed by the standard library log package.
// driver-host should provide a structured logger in production.
func NewStdLogger() Logger { 
	l := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	return &stdLogger{l: l}
}

type stdLogger struct {
	l  *log.Logger
	mu sync.Mutex
}

func (s *stdLogger) Debug(msg string, kv ...any) { s.printf("DEBUG", msg, kv...) }
func (s *stdLogger) Info(msg string, kv ...any)  { s.printf("INFO", msg, kv...) }
func (s *stdLogger) Warn(msg string, kv ...any)  { s.printf("WARN", msg, kv...) }
func (s *stdLogger) Error(msg string, kv ...any) { s.printf("ERROR", msg, kv...) }

func (s *stdLogger) printf(level, msg string, kv ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(kv) == 0 {
		s.l.Printf("%s %s", level, msg)
		return
	}
	s.l.Printf("%s %s %v", level, msg, kv)
}

// NewSystemClock returns a Clock that uses time.Now().
func NewSystemClock() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
