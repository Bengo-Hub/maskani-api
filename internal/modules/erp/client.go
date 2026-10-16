// Package erp is maskani's only door to erp-api. Every call is gated on the tenant's plan: ERP
// (staff records, payroll) comes with the tenant's subscription as the ERP feature set, and a
// tenant whose plan does not include it gets no data from erp and sends none to it until the
// tenant upgrades. Callers check Allowed (or the handlers' requireERP) before any call; the
// client never decides plans itself because a service key passes erp's own plan gate.
package erp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Feature is the subscriptions-api feature that carries ERP staff records. Maskani plans include
// it from the first tier (erpFeatures in the plan seed); a plan without it has no ERP.
const Feature = "hr_management"

// Allowed reports whether the signed-in tenant's plan includes ERP. Exempt tenants (platform
// owner, demo, subscription exempt) are allowed, as everywhere else.
func Allowed(c *authclient.Claims) bool {
	return c != nil && (c.IsGatingExempt() || c.FeatureEnabled(Feature))
}

// Employee is an erp employee as a picker shows it. Maskani keeps the id and name on its own
// records (a work order's assignee); the employee record itself stays in erp.
type Employee struct {
	ID             uuid.UUID `json:"id"`
	EmployeeNumber string    `json:"employee_number,omitempty"`
	Name           string    `json:"name"`
	Email          string    `json:"email,omitempty"`
}

// Client calls erp-api with the shared INTERNAL_SERVICE_KEY and the tenant in X-Tenant-ID.
type Client struct {
	apiKey  string
	enabled bool
	sc      *serviceclient.Client
}

// NewClient builds the client; with no URL or key it is disabled and every call errors clearly.
func NewClient(baseURL, apiKey string, log *zap.Logger) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{apiKey: apiKey, enabled: baseURL != "" && apiKey != ""}
	if c.enabled {
		cfg := serviceclient.DefaultConfig(baseURL, "erp-api", log.Named("erp.client"))
		cfg.Timeout = 10 * time.Second
		c.sc = serviceclient.New(cfg)
	}
	return c
}

// Enabled reports whether S2S calls are configured.
func (c *Client) Enabled() bool { return c != nil && c.enabled }

// Employees looks up the tenant's active employees by name, staff number or email (at most 50).
func (c *Client) Employees(ctx context.Context, tenantID uuid.UUID, q string, limit int) ([]Employee, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("erp client not configured")
	}
	v := url.Values{"q": {strings.TrimSpace(q)}, "limit": {fmt.Sprint(limit)}}
	resp, err := c.sc.Get(ctx, "/api/v1/hrm/employees/lookup?"+v.Encode(),
		map[string]string{"X-API-Key": c.apiKey, "X-Tenant-ID": tenantID.String()})
	if err != nil {
		return nil, fmt.Errorf("erp employees: %w", err)
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("erp employees: status %d", resp.StatusCode)
	}
	var out struct {
		Data []Employee `json:"data"`
	}
	if err := resp.DecodeJSON(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
