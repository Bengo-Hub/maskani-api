// Package notify sends direct messages through notifications-api (used where a message is not
// already driven by a maskani.* event, such as notices to an audience or portal invitations).
package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Client calls notifications-api.
type Client struct {
	apiKey  string
	enabled bool
	sc      *serviceclient.Client
	log     *zap.Logger
}

// NewClient builds the client.
func NewClient(baseURL, apiKey string, log *zap.Logger) *Client {
	log = log.Named("notify.client")
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{apiKey: apiKey, enabled: baseURL != "" && apiKey != "", log: log}
	if c.enabled {
		cfg := serviceclient.DefaultConfig(baseURL, "notifications-api", log)
		cfg.Timeout = 10 * time.Second
		c.sc = serviceclient.New(cfg)
	}
	return c
}

// Message mirrors notifications-api's send request.
type Message struct {
	Channel  string         `json:"channel"`
	Tenant   string         `json:"tenant"`
	Template string         `json:"template"`
	Data     map[string]any `json:"data"`
	To       []string       `json:"to"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Result carries the provider message id when available.
type Result struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Send delivers one message. idem makes retries safe.
func (c *Client) Send(ctx context.Context, tenantID uuid.UUID, tenantSlug, idem string, m Message) (*Result, error) {
	if !c.enabled {
		return nil, fmt.Errorf("notifications client not configured")
	}
	m.Tenant = tenantSlug
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	m.Metadata["source_service"] = "maskani"
	headers := map[string]string{"X-API-Key": c.apiKey, "X-Tenant-ID": tenantID.String()}
	if idem != "" {
		headers["Idempotency-Key"] = idem
	}
	resp, err := c.sc.Post(ctx, "/api/v1/"+tenantSlug+"/notifications/messages", m, headers)
	if err != nil {
		return nil, fmt.Errorf("notify: send: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("notify: send: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out Result
	_ = resp.DecodeJSON(&out)
	return &out, nil
}

// Broadcast is a notifications-api broadcast another service hands over (POST /api/v1/s2s/broadcasts).
// Notifications resolves the audience page by page from the owning service, applies templates,
// quiet-hour windows, rate limits and suppression, and tracks every recipient.
type Broadcast struct {
	Title    string           `json:"title"`
	Kind     string           `json:"kind"` // service_notice for estate notices (transactional, no consent gate)
	Channels []string         `json:"channels"`
	Content  BroadcastContent `json:"content"`
	Audience map[string]any   `json:"audience"`
}

// BroadcastContent carries the per-channel text. Email is a subject and a plain-text body;
// WhatsApp names an approved template and the tokens filling it ("message" is Message).
type BroadcastContent struct {
	Email    *BroadcastEmail    `json:"email,omitempty"`
	WhatsApp *BroadcastWhatsApp `json:"whatsapp,omitempty"`
}

// BroadcastEmail is an email subject and body.
type BroadcastEmail struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// BroadcastWhatsApp is an approved template with its parameter tokens.
type BroadcastWhatsApp struct {
	Template string   `json:"template"`
	Params   []string `json:"params"`
	Message  string   `json:"message,omitempty"`
}

// BroadcastResult is the created broadcast's id and status.
type BroadcastResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// CreateBroadcast hands a broadcast to notifications-api. sourceRef is this service's record (the
// notice id), echoed back on notifications.broadcast.completed. approve schedules it at once; only
// service notices may be approved this way (the sender already holds notices.manage here).
func (c *Client) CreateBroadcast(ctx context.Context, tenantID uuid.UUID, requestedBy, sourceRef string, approve bool, b Broadcast) (*BroadcastResult, error) {
	if !c.enabled {
		return nil, fmt.Errorf("notifications client not configured")
	}
	body := map[string]any{
		"tenant_id": tenantID.String(), "requested_by": requestedBy, "source": "maskani", "source_ref": sourceRef,
		"approve": approve, "broadcast": b,
	}
	resp, err := c.sc.Post(ctx, "/api/v1/s2s/broadcasts", body, map[string]string{"X-API-Key": c.apiKey, "Idempotency-Key": "MSK-NOTICE-" + sourceRef})
	if err != nil {
		return nil, fmt.Errorf("notify: broadcast: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("notify: broadcast: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out BroadcastResult
	if err := resp.DecodeJSON(&out); err != nil {
		return nil, fmt.Errorf("notify: broadcast: decode: %w", err)
	}
	return &out, nil
}
