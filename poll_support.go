package driversdk

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PollFunc is the function called on each poll interval
// Return nil for success, error for failure
type PollFunc func(ctx context.Context) error

// PollStatus represents the current polling status
type PollStatus struct {
	IsRunning           bool      `json:"is_running"`
	LastPollTime        time.Time `json:"last_poll_time,omitempty"`
	LastSuccessTime     time.Time `json:"last_success_time,omitempty"`
	LastErrorTime       time.Time `json:"last_error_time,omitempty"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	TotalPolls          int64     `json:"total_polls"`
	TotalFailures       int64     `json:"total_failures"`
}

// PollerOptions contains configuration for the poller
type PollerOptions struct {
	// Logger for logging poll events
	Logger Logger

	// MaxConsecutiveFailures before marking unhealthy (0 = unlimited)
	MaxConsecutiveFailures int

	// OnSuccess callback when poll succeeds
	OnSuccess func()

	// OnError callback when poll fails
	OnError func(err error)

	// OnUnhealthy callback when consecutive failures exceed max
	OnUnhealthy func(failures int)

	// InitialDelay before first poll (default: 0)
	InitialDelay time.Duration
}

// Poller provides a managed polling loop with status reporting
type Poller struct {
	interval time.Duration
	pollFn   PollFunc
	opts     PollerOptions

	mu                  sync.RWMutex
	running             atomic.Bool
	lastPollTime        time.Time
	lastSuccessTime     time.Time
	lastErrorTime       time.Time
	lastError           error
	consecutiveFailures int
	totalPolls          int64
	totalFailures       int64

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewPoller creates a new poller with the given interval and poll function
func NewPoller(interval time.Duration, pollFn PollFunc, opts PollerOptions) *Poller {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Poller{
		interval: interval,
		pollFn:   pollFn,
		opts:     opts,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the polling loop
func (p *Poller) Start(ctx context.Context) {
	if p.running.Swap(true) {
		return // Already running
	}

	p.stopCh = make(chan struct{})
	p.wg.Add(1)
	go p.loop(ctx)
}

// Stop stops the polling loop
func (p *Poller) Stop() {
	if !p.running.Swap(false) {
		return // Not running
	}
	close(p.stopCh)
	p.wg.Wait()
}

// IsRunning returns true if the poller is running
func (p *Poller) IsRunning() bool {
	return p.running.Load()
}

// IsHealthy returns true if the poller is healthy (no consecutive failures exceeding max)
func (p *Poller) IsHealthy() bool {
	if p.opts.MaxConsecutiveFailures <= 0 {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.consecutiveFailures < p.opts.MaxConsecutiveFailures
}

// ConsecutiveFailures returns the current consecutive failure count
func (p *Poller) ConsecutiveFailures() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.consecutiveFailures
}

// LastPollTime returns the last poll time
func (p *Poller) LastPollTime() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastPollTime
}

// LastError returns the last error
func (p *Poller) LastError() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastError
}

// Status returns the current poll status
func (p *Poller) Status() PollStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := PollStatus{
		IsRunning:           p.running.Load(),
		LastPollTime:        p.lastPollTime,
		LastSuccessTime:     p.lastSuccessTime,
		LastErrorTime:       p.lastErrorTime,
		ConsecutiveFailures: p.consecutiveFailures,
		TotalPolls:          p.totalPolls,
		TotalFailures:       p.totalFailures,
	}
	if p.lastError != nil {
		status.LastError = p.lastError.Error()
	}
	return status
}

// PollNow triggers an immediate poll (non-blocking)
func (p *Poller) PollNow() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), p.interval)
		defer cancel()
		p.doPoll(ctx)
	}()
}

func (p *Poller) loop(ctx context.Context) {
	defer p.wg.Done()

	// Initial delay
	if p.opts.InitialDelay > 0 {
		select {
		case <-time.After(p.opts.InitialDelay):
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}

	// Initial poll
	p.doPoll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.doPoll(ctx)
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (p *Poller) doPoll(ctx context.Context) {
	p.mu.Lock()
	p.lastPollTime = time.Now()
	p.totalPolls++
	p.mu.Unlock()

	err := p.pollFn(ctx)

	p.mu.Lock()
	if err != nil {
		p.lastError = err
		p.lastErrorTime = time.Now()
		p.consecutiveFailures++
		p.totalFailures++
		failures := p.consecutiveFailures
		p.mu.Unlock()

		if p.opts.Logger != nil {
			p.opts.Logger.Error("poll failed", "error", err, "consecutive_failures", failures)
		}

		if p.opts.OnError != nil {
			p.opts.OnError(err)
		}

		// Check for unhealthy state
		if p.opts.MaxConsecutiveFailures > 0 && failures >= p.opts.MaxConsecutiveFailures {
			if p.opts.OnUnhealthy != nil {
				p.opts.OnUnhealthy(failures)
			}
		}
	} else {
		p.lastError = nil
		p.lastSuccessTime = time.Now()
		p.consecutiveFailures = 0
		p.mu.Unlock()

		if p.opts.OnSuccess != nil {
			p.opts.OnSuccess()
		}
	}
}

// SetInterval changes the polling interval (takes effect on next tick)
func (p *Poller) SetInterval(interval time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.interval = interval
}

// GetInterval returns the current polling interval
func (p *Poller) GetInterval() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.interval
}
