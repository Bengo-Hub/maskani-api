// Package authapi is maskani-api's client for auth-api: customer and staff membership (S2S) and
// property outlets (forwarding the acting tenant admin's own token, which auth-api requires).
package authapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Client calls auth-api.
type Client struct {
	apiKey  string
	enabled bool
	sc      *serviceclient.Client
	log     *zap.Logger
}

// NewClient builds the client.
func NewClient(baseURL, apiKey string, log *zap.Logger) *Client {
	log = log.Named("authapi.client")
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{apiKey: apiKey, enabled: baseURL != "", log: log}
	if c.enabled {
		cfg := serviceclient.DefaultConfig(baseURL, "auth-api", log)
		cfg.Timeout = 10 * time.Second
		c.sc = serviceclient.New(cfg)
	}
	return c
}

// MemberRequest mirrors auth-api's addTenantMemberRequest. Customers are added by phone; auth-api
// finds the user by verified phone or creates a phone-only account.
type MemberRequest struct {
	Email    string   `json:"email,omitempty"`
	Phone    string   `json:"phone,omitempty"`
	Name     string   `json:"name,omitempty"`
	Roles    []string `json:"roles"`
	OutletID string   `json:"outlet_id,omitempty"`
	Service  string   `json:"service,omitempty"`
}

// MemberResult mirrors auth-api's tenantMemberResponse subset.
type MemberResult struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
}

// AddMember creates or links a tenant member and returns the auth user id.
func (c *Client) AddMember(ctx context.Context, tenantID uuid.UUID, req MemberRequest) (*MemberResult, error) {
	if !c.enabled || c.apiKey == "" {
		return nil, fmt.Errorf("authapi client not configured")
	}
	req.Service = "maskani"
	resp, err := c.sc.Post(ctx, "/api/v1/s2s/tenants/"+tenantID.String()+"/members", req, map[string]string{"X-API-Key": c.apiKey})
	if err != nil {
		return nil, fmt.Errorf("authapi: add member: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("authapi: add member: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out MemberResult
	if err := resp.DecodeJSON(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OutletRequest mirrors auth-api's outletRequest subset.
type OutletRequest struct {
	Code     string         `json:"code"`
	Name     string         `json:"name"`
	UseCase  string         `json:"use_case"`
	Address  string         `json:"address,omitempty"`
	Status   string         `json:"status,omitempty"`
	Timezone string         `json:"timezone,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Outlet is the subset of auth-api's outlet response maskani stores.
type Outlet struct {
	ID   uuid.UUID `json:"id"`
	Code string    `json:"code"`
	Name string    `json:"name"`
}

// CreateOutlet registers a property as an auth-api outlet using the caller's bearer token.
func (c *Client) CreateOutlet(ctx context.Context, tenantSlug, bearer string, req OutletRequest) (*Outlet, error) {
	if !c.enabled {
		return nil, fmt.Errorf("authapi client not configured")
	}
	if req.UseCase == "" {
		req.UseCase = "property"
	}
	resp, err := c.sc.Post(ctx, "/api/v1/tenants/"+tenantSlug+"/outlets", req, map[string]string{"Authorization": bearer})
	if err != nil {
		return nil, fmt.Errorf("authapi: create outlet: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("authapi: create outlet: status %d: %s", resp.StatusCode, string(resp.Body))
	}
	var out Outlet
	if err := resp.DecodeJSON(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
