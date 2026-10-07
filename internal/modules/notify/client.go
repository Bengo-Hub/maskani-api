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
