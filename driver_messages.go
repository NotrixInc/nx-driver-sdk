package driversdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DriverMessage is an envelope for driver-to-driver messaging (via controller-core HTTP).
// It is identified and routed using driver IDs (string).
//
// Delivery is gated by rules stored in controller-core bindings with:
// - binding_type: DRIVER_TO_DRIVER
// - meta_json: {source_driver_id,target_driver_id,bidirectional}
// - rule_type: ALLOW (default) or DENY

type DriverMessage struct {
	ID           int64           `json:"id"`
	SourceDriver string          `json:"source_driver_id"`
	TargetDriver string          `json:"target_driver_id"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload_json"`
	Correlation  string          `json:"correlation_id,omitempty"`
	TSUnixMs     int64           `json:"ts_unix_ms"`
}

type PublishDriverMessageResponse struct {
	DeliveredTo []string        `json:"delivered_to"`
	Messages    []DriverMessage `json:"messages"`
}

type PollDriverMessagesResponse struct {
	DriverID string          `json:"driver_id"`
	Messages []DriverMessage `json:"messages"`
}

type DriverMessageClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewDriverMessageClient(baseURL string) *DriverMessageClient {
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	return &DriverMessageClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			// Must exceed the maximum long-poll wait (30s) to avoid spurious timeouts.
			// Per-request contexts still bound overall request time.
			Timeout: 40 * time.Second,
		},
	}
}

func NewDriverMessageClientFromEnv() *DriverMessageClient {
	coreHTTP := strings.TrimSpace(os.Getenv("CORE_HTTP_ADDR"))
	if coreHTTP == "" {
		coreHTTP = strings.TrimSpace(os.Getenv("CONTROLLER_CORE_HTTP_ADDR"))
	}
	if coreHTTP == "" {
		coreHTTP = "http://127.0.0.1:8090"
	}
	return NewDriverMessageClient(coreHTTP)
}

type publishDriverMessageReq struct {
	SourceDriverID string          `json:"source_driver_id"`
	TargetDriverID string          `json:"target_driver_id,omitempty"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload_json,omitempty"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
}

func (c *DriverMessageClient) Publish(ctx context.Context, sourceDriverID, targetDriverID, msgType string, payload any, correlationID string) (*PublishDriverMessageResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("driver message client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("BaseURL is empty")
	}
	sourceDriverID = strings.TrimSpace(sourceDriverID)
	msgType = strings.TrimSpace(msgType)
	if sourceDriverID == "" || msgType == "" {
		return nil, fmt.Errorf("sourceDriverID and msgType are required")
	}

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
			return nil, err
		}
		payloadJSON = json.RawMessage(b)
	}
	if len(payloadJSON) == 0 {
		payloadJSON = json.RawMessage("{}")
	}

	reqBody, _ := json.Marshal(publishDriverMessageReq{
		SourceDriverID: sourceDriverID,
		TargetDriverID: strings.TrimSpace(targetDriverID),
		Type:           msgType,
		Payload:        payloadJSON,
		CorrelationID:  strings.TrimSpace(correlationID),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/driver-messages", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("publish driver message failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out PublishDriverMessageResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *DriverMessageClient) Poll(ctx context.Context, driverID string, afterID int64, wait time.Duration, limit int) (*PollDriverMessagesResponse, error) {
	if c == nil {
		return nil, fmt.Errorf("driver message client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("BaseURL is empty")
	}
	driverID = strings.TrimSpace(driverID)
	if driverID == "" {
		return nil, fmt.Errorf("driverID is required")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	if wait < 0 {
		wait = 0
	}
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}

	url := c.BaseURL + "/v1/driver-messages?driver_id=" + strings.TrimSpace(driverID) + "&after_id=" + strconv.FormatInt(afterID, 10) + "&limit=" + strconv.Itoa(limit)
	if wait > 0 {
		url += "&wait_ms=" + strconv.FormatInt(int64(wait/time.Millisecond), 10)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("poll driver messages failed: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out PollDriverMessagesResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
