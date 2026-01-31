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

	"github.com/google/uuid"
)

// MessageDeliveryMode specifies how a message should be delivered
type MessageDeliveryMode string

const (
	// DeliveryUnicast sends message to a specific driver by ID
	DeliveryUnicast MessageDeliveryMode = "UNICAST"
	// DeliveryBroadcast sends message to all drivers (controller-core filters by bindings)
	DeliveryBroadcast MessageDeliveryMode = "BROADCAST"
	// DeliveryMulticast sends message to a group of drivers
	DeliveryMulticast MessageDeliveryMode = "MULTICAST"
)

// MessagePriority defines the priority level for messages
type MessagePriority string

const (
	PriorityLow    MessagePriority = "LOW"
	PriorityNormal MessagePriority = "NORMAL"
	PriorityHigh   MessagePriority = "HIGH"
)

// AckStatus represents the acknowledgment status
type AckStatus string

const (
	AckStatusPending  AckStatus = "PENDING"
	AckStatusAcked    AckStatus = "ACKED"
	AckStatusNacked   AckStatus = "NACKED"
	AckStatusTimedOut AckStatus = "TIMED_OUT"
)

// Reserved message types for ACK system
const (
	MessageTypeAck  = "_ack"
	MessageTypeNack = "_nack"
)

// BusMessage represents a message on the driver bus
type BusMessage struct {
	ID            int64               `json:"id"`
	SourceDriver  string              `json:"source_driver_id"`
	TargetDriver  string              `json:"target_driver_id,omitempty"` // For unicast
	TargetGroup   []string            `json:"target_group,omitempty"`     // For multicast
	DeliveryMode  MessageDeliveryMode `json:"delivery_mode"`
	Type          string              `json:"type"`
	Payload       json.RawMessage     `json:"payload_json"`
	CorrelationID string              `json:"correlation_id,omitempty"`
	Priority      MessagePriority     `json:"priority,omitempty"`
	TSUnixMs      int64               `json:"ts_unix_ms"`
	TTLMs         int64               `json:"ttl_ms,omitempty"` // Time-to-live in milliseconds (0 = no expiry)
	RequireAck    bool                `json:"require_ack,omitempty"`
}

// AckPayload is the payload for ACK/NACK messages
type AckPayload struct {
	OriginalMessageID   int64  `json:"original_message_id"`
	OriginalType        string `json:"original_type"`
	OriginalCorrelation string `json:"original_correlation_id,omitempty"`
	Status              string `json:"status"` // "acked" or "nacked"
	Reason              string `json:"reason,omitempty"`
}

// PendingAck tracks a message awaiting acknowledgment
type PendingAck struct {
	MessageID     int64
	CorrelationID string
	TargetDriver  string
	SentAt        time.Time
	TimeoutMs     int64
	ResultCh      chan AckResult
}

// AckResult is returned when an ACK/NACK is received or timeout occurs
type AckResult struct {
	Status        AckStatus
	TargetDriver  string
	Reason        string
	ReceivedAt    time.Time
	CorrelationID string
}

// MessageHandler is the interface for handling incoming messages
// The driver is responsible for authorization using its own binding information
type MessageHandler interface {
	// Handle processes the message. The driver should authorize based on its bindings.
	// Return nil to auto-ACK, return error to auto-NACK (if RequireAck is true)
	Handle(ctx context.Context, msg BusMessage) error
}

// MessageHandlerFunc is a simple function-based message handler
type MessageHandlerFunc func(ctx context.Context, msg BusMessage) error

// MessageBus provides a simple transport for driver-to-driver messaging
// Note: MessageBus does NOT cache bindings. Drivers are responsible for:
// - Obtaining binding info to determine destination driver IDs
// - Authorizing received messages based on their bindings
// When a driver is deleted, controller-core automatically deletes related bindings.
type MessageBus struct {
	driverID   string
	baseURL    string
	httpClient *http.Client
	logger     Logger

	handlersMu sync.RWMutex
	handlers   map[string][]MessageHandler // messageType -> handlers

	// Pending ACKs tracking
	pendingAcksMu sync.Mutex
	pendingAcks   map[string]*PendingAck // correlationID -> PendingAck

	afterID int64

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// MessageBusConfig holds configuration for the message bus
type MessageBusConfig struct {
	DriverID   string
	BaseURL    string       // controller-core HTTP address
	HTTPClient *http.Client // optional custom HTTP client
	Logger     Logger       // optional logger
}

// NewMessageBus creates a new MessageBus instance
func NewMessageBus(cfg MessageBusConfig) (*MessageBus, error) {
	driverID := strings.TrimSpace(cfg.DriverID)
	if driverID == "" {
		return nil, fmt.Errorf("driver ID is required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("CORE_HTTP_ADDR"))
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("CONTROLLER_CORE_HTTP_ADDR"))
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8090"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 40 * time.Second,
		}
	}

	return &MessageBus{
		driverID:    driverID,
		baseURL:     baseURL,
		httpClient:  httpClient,
		logger:      cfg.Logger,
		handlers:    make(map[string][]MessageHandler),
		pendingAcks: make(map[string]*PendingAck),
		stopCh:      make(chan struct{}),
	}, nil
}

// NewMessageBusFromEnv creates a MessageBus using environment variables
func NewMessageBusFromEnv(driverID string, logger Logger) (*MessageBus, error) {
	return NewMessageBus(MessageBusConfig{
		DriverID: driverID,
		Logger:   logger,
	})
}

// DriverID returns the driver ID this bus is associated with
func (mb *MessageBus) DriverID() string {
	return mb.driverID
}

// RegisterHandler registers a message handler for a specific message type
// Use "*" as messageType to handle all message types
func (mb *MessageBus) RegisterHandler(messageType string, handler MessageHandler) {
	mb.handlersMu.Lock()
	defer mb.handlersMu.Unlock()
	mb.handlers[messageType] = append(mb.handlers[messageType], handler)
}

// RegisterHandlerFunc registers a simple function handler for a message type
func (mb *MessageBus) RegisterHandlerFunc(messageType string, fn MessageHandlerFunc) {
	mb.RegisterHandler(messageType, &simpleHandler{fn: fn})
}

type simpleHandler struct {
	fn MessageHandlerFunc
}

func (h *simpleHandler) Handle(ctx context.Context, msg BusMessage) error {
	return h.fn(ctx, msg)
}

// Send sends a unicast message to a specific driver
// The caller (driver) is responsible for determining the targetDriverID from its bindings
func (mb *MessageBus) Send(ctx context.Context, targetDriverID string, msgType string, payload any, opts ...SendOption) (*SendResult, error) {
	cfg := defaultSendConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return mb.publish(ctx, &BusMessage{
		SourceDriver:  mb.driverID,
		TargetDriver:  strings.TrimSpace(targetDriverID),
		DeliveryMode:  DeliveryUnicast,
		Type:          msgType,
		CorrelationID: cfg.CorrelationID,
		Priority:      cfg.Priority,
		TTLMs:         cfg.TTLMs,
	}, payload)
}

// Broadcast sends a message to all drivers
// Controller-core will filter delivery based on existing bindings
func (mb *MessageBus) Broadcast(ctx context.Context, msgType string, payload any, opts ...SendOption) (*SendResult, error) {
	cfg := defaultSendConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return mb.publish(ctx, &BusMessage{
		SourceDriver:  mb.driverID,
		DeliveryMode:  DeliveryBroadcast,
		Type:          msgType,
		CorrelationID: cfg.CorrelationID,
		Priority:      cfg.Priority,
		TTLMs:         cfg.TTLMs,
	}, payload)
}

// Multicast sends a message to a group of specific drivers
// The caller (driver) is responsible for determining the targetDriverIDs from its bindings
func (mb *MessageBus) Multicast(ctx context.Context, targetDriverIDs []string, msgType string, payload any, opts ...SendOption) (*SendResult, error) {
	cfg := defaultSendConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	targets := make([]string, 0, len(targetDriverIDs))
	for _, id := range targetDriverIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			targets = append(targets, id)
		}
	}

	return mb.publish(ctx, &BusMessage{
		SourceDriver:  mb.driverID,
		TargetGroup:   targets,
		DeliveryMode:  DeliveryMulticast,
		Type:          msgType,
		CorrelationID: cfg.CorrelationID,
		Priority:      cfg.Priority,
		TTLMs:         cfg.TTLMs,
	}, payload)
}

// SendResult contains the result of a send operation
type SendResult struct {
	DeliveredTo   []string `json:"delivered_to"`
	MessageID     int64    `json:"message_id"`
	CorrelationID string   `json:"correlation_id,omitempty"`
}

// SendOption configures send behavior
type SendOption func(*sendConfig)

type sendConfig struct {
	CorrelationID string
	Priority      MessagePriority
	TTLMs         int64
	RequireAck    bool
	AckTimeoutMs  int64
}

func defaultSendConfig() sendConfig {
	return sendConfig{
		Priority:     PriorityNormal,
		TTLMs:        30000, // 30 seconds default TTL
		AckTimeoutMs: 5000,  // 5 seconds default ACK timeout
	}
}

// WithCorrelationID sets a correlation ID for request/response tracking
func WithCorrelationID(id string) SendOption {
	return func(c *sendConfig) { c.CorrelationID = id }
}

// WithPriority sets the message priority
func WithPriority(p MessagePriority) SendOption {
	return func(c *sendConfig) { c.Priority = p }
}

// WithTTL sets the time-to-live in milliseconds
func WithTTL(ms int64) SendOption {
	return func(c *sendConfig) { c.TTLMs = ms }
}

// WithAck requires acknowledgment from recipient(s)
func WithAck() SendOption {
	return func(c *sendConfig) { c.RequireAck = true }
}

// WithAckTimeout sets the timeout for waiting for ACK (in milliseconds)
func WithAckTimeout(ms int64) SendOption {
	return func(c *sendConfig) { c.AckTimeoutMs = ms }
}

func (mb *MessageBus) publish(ctx context.Context, msg *BusMessage, payload any) (*SendResult, error) {
	// Marshal payload
	var payloadJSON json.RawMessage
	switch t := payload.(type) {
	case nil:
		payloadJSON = json.RawMessage("{}")
	case json.RawMessage:
		payloadJSON = t
	case []byte:
		payloadJSON = json.RawMessage(t)
	case string:
		payloadJSON = json.RawMessage(t)
	default:
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal payload: %w", err)
		}
		payloadJSON = json.RawMessage(b)
	}
	if len(payloadJSON) == 0 {
		payloadJSON = json.RawMessage("{}")
	}
	msg.Payload = payloadJSON

	reqBody, _ := json.Marshal(map[string]any{
		"source_driver_id": msg.SourceDriver,
		"target_driver_id": msg.TargetDriver,
		"target_group":     msg.TargetGroup,
		"delivery_mode":    msg.DeliveryMode,
		"type":             msg.Type,
		"payload_json":     msg.Payload,
		"correlation_id":   msg.CorrelationID,
		"priority":         msg.Priority,
		"ttl_ms":           msg.TTLMs,
		"require_ack":      msg.RequireAck,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mb.baseURL+"/v1/driver-messages", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := mb.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("publish failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out struct {
		DeliveredTo []string        `json:"delivered_to"`
		Messages    []DriverMessage `json:"messages"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}

	result := &SendResult{
		DeliveredTo:   out.DeliveredTo,
		CorrelationID: msg.CorrelationID,
	}
	if len(out.Messages) > 0 {
		result.MessageID = out.Messages[0].ID
	}
	return result, nil
}

// SendWithAck sends a message and waits for acknowledgment
// Returns AckResult with the acknowledgment status
func (mb *MessageBus) SendWithAck(ctx context.Context, targetDriverID string, msgType string, payload any, opts ...SendOption) (*AckResult, error) {
	cfg := defaultSendConfig()
	cfg.RequireAck = true
	for _, opt := range opts {
		opt(&cfg)
	}

	// Generate correlation ID if not provided
	correlationID := cfg.CorrelationID
	if correlationID == "" {
		correlationID = uuid.New().String()
	}

	// Create result channel
	resultCh := make(chan AckResult, 1)

	// Register pending ACK
	pending := &PendingAck{
		CorrelationID: correlationID,
		TargetDriver:  targetDriverID,
		SentAt:        time.Now(),
		TimeoutMs:     cfg.AckTimeoutMs,
		ResultCh:      resultCh,
	}

	mb.pendingAcksMu.Lock()
	mb.pendingAcks[correlationID] = pending
	mb.pendingAcksMu.Unlock()

	// Send the message
	result, err := mb.publish(ctx, &BusMessage{
		SourceDriver:  mb.driverID,
		TargetDriver:  strings.TrimSpace(targetDriverID),
		DeliveryMode:  DeliveryUnicast,
		Type:          msgType,
		CorrelationID: correlationID,
		Priority:      cfg.Priority,
		TTLMs:         cfg.TTLMs,
		RequireAck:    true,
	}, payload)

	if err != nil {
		// Remove pending ACK on send failure
		mb.pendingAcksMu.Lock()
		delete(mb.pendingAcks, correlationID)
		mb.pendingAcksMu.Unlock()
		return nil, err
	}

	pending.MessageID = result.MessageID

	// Wait for ACK or timeout
	timeout := time.Duration(cfg.AckTimeoutMs) * time.Millisecond
	select {
	case ackResult := <-resultCh:
		return &ackResult, nil
	case <-time.After(timeout):
		// Timeout - remove pending and return timeout result
		mb.pendingAcksMu.Lock()
		delete(mb.pendingAcks, correlationID)
		mb.pendingAcksMu.Unlock()
		return &AckResult{
			Status:        AckStatusTimedOut,
			TargetDriver:  targetDriverID,
			Reason:        fmt.Sprintf("ACK timeout after %dms", cfg.AckTimeoutMs),
			CorrelationID: correlationID,
		}, nil
	case <-ctx.Done():
		// Context cancelled
		mb.pendingAcksMu.Lock()
		delete(mb.pendingAcks, correlationID)
		mb.pendingAcksMu.Unlock()
		return nil, ctx.Err()
	}
}

// SendAck sends an acknowledgment for a received message
func (mb *MessageBus) SendAck(ctx context.Context, originalMsg BusMessage) error {
	if originalMsg.CorrelationID == "" {
		return nil // No correlation ID, nothing to ACK
	}

	ackPayload := AckPayload{
		OriginalMessageID:   originalMsg.ID,
		OriginalType:        originalMsg.Type,
		OriginalCorrelation: originalMsg.CorrelationID,
		Status:              "acked",
	}

	_, err := mb.Send(ctx, originalMsg.SourceDriver, MessageTypeAck, ackPayload,
		WithCorrelationID(originalMsg.CorrelationID),
		WithPriority(PriorityHigh),
		WithTTL(10000),
	)
	return err
}

// SendNack sends a negative acknowledgment for a received message
func (mb *MessageBus) SendNack(ctx context.Context, originalMsg BusMessage, reason string) error {
	if originalMsg.CorrelationID == "" {
		return nil // No correlation ID, nothing to NACK
	}

	nackPayload := AckPayload{
		OriginalMessageID:   originalMsg.ID,
		OriginalType:        originalMsg.Type,
		OriginalCorrelation: originalMsg.CorrelationID,
		Status:              "nacked",
		Reason:              reason,
	}

	_, err := mb.Send(ctx, originalMsg.SourceDriver, MessageTypeNack, nackPayload,
		WithCorrelationID(originalMsg.CorrelationID),
		WithPriority(PriorityHigh),
		WithTTL(10000),
	)
	return err
}

// Start begins listening for incoming messages
func (mb *MessageBus) Start(ctx context.Context) error {
	// Fast-forward past existing messages
	mb.fastForward()

	// Start message polling loop
	mb.wg.Add(1)
	go mb.pollLoop()

	return nil
}

// Stop stops the message bus
func (mb *MessageBus) Stop(ctx context.Context) error {
	close(mb.stopCh)
	mb.wg.Wait()
	return nil
}

func (mb *MessageBus) fastForward() {
	after := mb.afterID
	for i := 0; i < 6; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		resp, err := mb.poll(ctx, after, 0, 500)
		cancel()
		if err != nil {
			return
		}
		if len(resp) == 0 {
			break
		}
		for _, m := range resp {
			if m.ID > after {
				after = m.ID
			}
		}
	}
	mb.afterID = after
}

func (mb *MessageBus) pollLoop() {
	defer mb.wg.Done()

	for {
		select {
		case <-mb.stopCh:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		messages, err := mb.poll(ctx, mb.afterID, 10*time.Second, 200)
		cancel()

		if err != nil {
			if mb.logger != nil {
				mb.logger.Debug("message bus poll failed", "err", err.Error())
			}
			time.Sleep(750 * time.Millisecond)
			continue
		}

		for _, msg := range messages {
			if msg.ID > mb.afterID {
				mb.afterID = msg.ID
			}
			mb.dispatchMessage(msg)
		}
	}
}

func (mb *MessageBus) poll(ctx context.Context, afterID int64, wait time.Duration, limit int) ([]BusMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}

	url := fmt.Sprintf("%s/v1/driver-messages?driver_id=%s&after_id=%d&limit=%d",
		mb.baseURL, mb.driverID, afterID, limit)
	if wait > 0 {
		url += fmt.Sprintf("&wait_ms=%d", int64(wait/time.Millisecond))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := mb.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("poll failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out struct {
		Messages []BusMessage `json:"messages"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}

	return out.Messages, nil
}

func (mb *MessageBus) dispatchMessage(msg BusMessage) {
	// Check if message is expired
	if msg.TTLMs > 0 && msg.TSUnixMs > 0 {
		age := time.Now().UnixMilli() - msg.TSUnixMs
		if age > msg.TTLMs {
			if mb.logger != nil {
				mb.logger.Debug("dropping expired message", "id", msg.ID, "age_ms", age, "ttl_ms", msg.TTLMs)
			}
			return
		}
	}

	// Ignore messages from self
	if strings.EqualFold(strings.TrimSpace(msg.SourceDriver), mb.driverID) {
		return
	}

	// Handle ACK/NACK messages specially - resolve pending ACKs
	if msg.Type == MessageTypeAck || msg.Type == MessageTypeNack {
		mb.handleAckMessage(msg)
		return
	}

	// Get handlers for this message type
	mb.handlersMu.RLock()
	handlers := mb.handlers[msg.Type]
	wildcardHandlers := mb.handlers["*"]
	mb.handlersMu.RUnlock()

	allHandlers := append(handlers, wildcardHandlers...)
	if len(allHandlers) == 0 {
		// No handlers - send NACK if ACK was required
		if msg.RequireAck {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = mb.SendNack(ctx, msg, "no handler registered for message type")
			}()
		}
		return
	}

	ctx := context.Background()
	var lastErr error
	for _, handler := range allHandlers {
		// The handler is responsible for authorization based on the driver's bindings
		if err := handler.Handle(ctx, msg); err != nil {
			lastErr = err
			if mb.logger != nil {
				mb.logger.Debug("message handle failed", "type", msg.Type, "err", err.Error())
			}
		}
	}

	// Send ACK/NACK if required
	if msg.RequireAck {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if lastErr != nil {
				_ = mb.SendNack(ctx, msg, lastErr.Error())
			} else {
				_ = mb.SendAck(ctx, msg)
			}
		}()
	}
}

// handleAckMessage processes incoming ACK/NACK messages and resolves pending ACKs
func (mb *MessageBus) handleAckMessage(msg BusMessage) {
	correlationID := msg.CorrelationID
	if correlationID == "" {
		return
	}

	mb.pendingAcksMu.Lock()
	pending, exists := mb.pendingAcks[correlationID]
	if exists {
		delete(mb.pendingAcks, correlationID)
	}
	mb.pendingAcksMu.Unlock()

	if !exists || pending.ResultCh == nil {
		return
	}

	// Parse ACK payload
	var ackPayload AckPayload
	if err := json.Unmarshal(msg.Payload, &ackPayload); err != nil {
		if mb.logger != nil {
			mb.logger.Debug("failed to parse ACK payload", "err", err.Error())
		}
	}

	status := AckStatusAcked
	if msg.Type == MessageTypeNack {
		status = AckStatusNacked
	}

	result := AckResult{
		Status:        status,
		TargetDriver:  msg.SourceDriver,
		Reason:        ackPayload.Reason,
		CorrelationID: correlationID,
		ReceivedAt:    time.Now(),
	}

	// Non-blocking send to result channel
	select {
	case pending.ResultCh <- result:
	default:
	}
}
