// Package treasury is maskani-api's S2S client for treasury-api. maskani never moves money: it asks
// treasury to raise invoices, start payments and route paybill references, and stores the IDs.
package treasury

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Reference types maskani puts on treasury records. Account payments are allocated by treasury
// across the account's open invoices (metadata.account_ref), oldest due first.
const (
	RefBill           = "maskani_bill"       // reference_id = billing_run_line id
	RefInstalment     = "maskani_instalment" // reference_id = instalment id
	RefReservationFee = "maskani_reservation"
	RefAdhoc          = "maskani_adhoc"
	RefAccountPayment = "account_payment" // payment intents and C2B routes: reference_id = unit account id
	SourceService     = "maskani"
)

// Client calls /api/v1/s2s/{tenant}/... with the shared INTERNAL_SERVICE_KEY.
type Client struct {
	apiKey  string
	enabled bool
	sc      *serviceclient.Client
	log     *zap.Logger
}

// NewClient builds the client; with no URL or key it is disabled and every call errors clearly.
func NewClient(baseURL, apiKey string, log *zap.Logger) *Client {
	log = log.Named("treasury.client")
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{apiKey: apiKey, enabled: baseURL != "" && apiKey != "", log: log}
	if c.enabled {
		cfg := serviceclient.DefaultConfig(baseURL, "treasury-api", log)
		cfg.Timeout = 20 * time.Second
		c.sc = serviceclient.New(cfg)
	}
	return c
}

// Enabled reports whether S2S calls are configured.
func (c *Client) Enabled() bool { return c.enabled }

func (c *Client) headers(tenantID uuid.UUID, idem string) map[string]string {
	h := map[string]string{"X-API-Key": c.apiKey, "X-Tenant-ID": tenantID.String()}
	if idem != "" {
		h["Idempotency-Key"] = idem
	}
	return h
}

func path(tenantID uuid.UUID, p string) string { return fmt.Sprintf("/api/v1/s2s/%s%s", tenantID, p) }

func (c *Client) post(ctx context.Context, tenantID uuid.UUID, p, idem string, body, out any) error {
	if !c.enabled {
		return fmt.Errorf("treasury client not configured")
	}
	resp, err := c.sc.Post(ctx, path(tenantID, p), body, c.headers(tenantID, idem))
	if err != nil {
		return fmt.Errorf("treasury POST %s: %w", p, err)
	}
	if !resp.IsSuccess() {
		return &HTTPError{Status: resp.StatusCode, Body: string(resp.Body), Path: p}
	}
	if out != nil {
		return resp.DecodeJSON(out)
	}
	return nil
}

func (c *Client) get(ctx context.Context, tenantID uuid.UUID, p string, out any) error {
	if !c.enabled {
		return fmt.Errorf("treasury client not configured")
	}
	resp, err := c.sc.Get(ctx, path(tenantID, p), c.headers(tenantID, ""))
	if err != nil {
		return fmt.Errorf("treasury GET %s: %w", p, err)
	}
	if !resp.IsSuccess() {
		return &HTTPError{Status: resp.StatusCode, Body: string(resp.Body), Path: p}
	}
	return resp.DecodeJSON(out)
}

// HTTPError is a non-2xx treasury response.
type HTTPError struct {
	Status int
	Body   string
	Path   string
}

func (e *HTTPError) Error() string {
	b := e.Body
	if len(b) > 300 {
		b = b[:300]
	}
	return fmt.Sprintf("treasury %s: status %d: %s", e.Path, e.Status, b)
}

// IsNotFound reports a 404 from treasury.
func IsNotFound(err error) bool {
	he, ok := err.(*HTTPError)
	return ok && he.Status == 404
}

// InvoiceLine mirrors treasury's invoicing.LineRequest fields maskani uses.
type InvoiceLine struct {
	Description string  `json:"description"`
	ItemSKU     string  `json:"item_sku,omitempty"`
	ItemType    string  `json:"item_type,omitempty"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	TaxRate     float64 `json:"tax_rate,omitempty"`
}

// CreateInvoiceRequest mirrors treasury's invoicing.CreateInvoiceRequest (subset).
type CreateInvoiceRequest struct {
	CustomerName        string         `json:"customer_name,omitempty"`
	CustomerPhone       string         `json:"customer_phone,omitempty"`
	CustomerEmail       string         `json:"customer_email,omitempty"`
	CRMCustomerID       string         `json:"crm_customer_id,omitempty"`
	InvoiceType         string         `json:"invoice_type,omitempty"`
	InvoiceDate         time.Time      `json:"invoice_date"`
	DueDate             time.Time      `json:"due_date"`
	Currency            string         `json:"currency,omitempty"`
	Notes               string         `json:"notes,omitempty"`
	ReferenceID         *uuid.UUID     `json:"reference_id,omitempty"`
	ReferenceType       string         `json:"reference_type,omitempty"`
	OutletID            *uuid.UUID     `json:"outlet_id,omitempty"`
	SettlementAccountID *uuid.UUID     `json:"settlement_account_id,omitempty"`
	Lines               []InvoiceLine  `json:"lines"`
	Metadata            map[string]any `json:"metadata,omitempty"`
}

// Invoice is the subset of treasury's Invoice maskani reads.
type Invoice struct {
	ID            uuid.UUID       `json:"id"`
	InvoiceNumber string          `json:"invoice_number"`
	PublicToken   uuid.UUID       `json:"public_token"`
	DueDate       time.Time       `json:"due_date"`
	TotalAmount   decimal.Decimal `json:"total_amount"`
	AmountPaid    decimal.Decimal `json:"amount_paid"`
	Currency      string          `json:"currency"`
	Status        string          `json:"status"`
	PaymentStatus string          `json:"payment_status"`
}

// CreateInvoice raises an invoice, first checking by reference so a retry never issues twice.
func (c *Client) CreateInvoice(ctx context.Context, tenantID uuid.UUID, req CreateInvoiceRequest) (*Invoice, error) {
	if req.ReferenceID != nil && req.ReferenceType != "" {
		if inv, err := c.InvoiceByReference(ctx, tenantID, *req.ReferenceID, req.ReferenceType); err == nil {
			return inv, nil
		}
	}
	idem := ""
	if req.ReferenceID != nil {
		idem = "MSK-INV-" + req.ReferenceID.String()
	}
	var out Invoice
	if err := c.post(ctx, tenantID, "/invoices", idem, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InvoiceByReference finds the invoice raised for a maskani record.
func (c *Client) InvoiceByReference(ctx context.Context, tenantID, refID uuid.UUID, refType string) (*Invoice, error) {
	var out Invoice
	q := url.Values{"reference_id": {refID.String()}, "reference_type": {refType}}
	if err := c.get(ctx, tenantID, "/invoices/by-reference?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendInvoice asks treasury to deliver the invoice (email or messaging per tenant config).
func (c *Client) SendInvoice(ctx context.Context, tenantID, invoiceID uuid.UUID) error {
	return c.post(ctx, tenantID, fmt.Sprintf("/invoices/%s/send", invoiceID), "", map[string]any{}, nil)
}

// IntentRequest mirrors treasury's payments.CreateIntentRequest (subset).
type IntentRequest struct {
	ReferenceID    string         `json:"reference_id"`
	ReferenceType  string         `json:"reference_type"`
	PaymentMethod  string         `json:"payment_method"`
	Currency       string         `json:"currency"`
	Amount         decimal.Decimal `json:"amount"`
	PhoneNumber    *string        `json:"phone_number,omitempty"`
	CustomerEmail  *string        `json:"customer_email,omitempty"`
	Description    *string        `json:"description,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	SourceService  string         `json:"source_service"`
	OutletID       *uuid.UUID     `json:"outlet_id,omitempty"`
	Gateway        string         `json:"gateway,omitempty"`
	CallbackURL    string         `json:"callback_url,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// IntentResponse mirrors treasury's CreateIntentResponse (subset).
type IntentResponse struct {
	IntentID          uuid.UUID       `json:"intent_id"`
	Status            string          `json:"status"`
	PaymentMethod     string          `json:"payment_method"`
	Amount            decimal.Decimal `json:"amount"`
	Currency          string          `json:"currency"`
	CheckoutRequestID *string         `json:"checkout_request_id,omitempty"`
	AuthorizationURL  *string         `json:"authorization_url,omitempty"`
	Instructions      map[string]any  `json:"instructions,omitempty"`
}

// CreateIntent starts a payment through the tenant's configured gateway (Daraja, PayHero, Paystack).
func (c *Client) CreateIntent(ctx context.Context, tenantID uuid.UUID, req IntentRequest) (*IntentResponse, error) {
	req.SourceService = SourceService
	var out IntentResponse
	if err := c.post(ctx, tenantID, "/payments/intents", req.IdempotencyKey, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// C2BRoute registers a paybill account reference for a unit account.
type C2BRoute struct {
	AccountRef    string `json:"account_ref"`
	Shortcode     string `json:"shortcode,omitempty"`
	SourceService string `json:"source_service"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	Fund          string `json:"fund,omitempty"`
	Status        string `json:"status,omitempty"`
}

// RegisterC2BRoute upserts a route so paybill payments to account_ref settle that account.
func (c *Client) RegisterC2BRoute(ctx context.Context, tenantID uuid.UUID, r C2BRoute) error {
	r.SourceService = SourceService
	if r.ReferenceType == "" {
		r.ReferenceType = RefAccountPayment
	}
	return c.post(ctx, tenantID, "/c2b/account-routes", "", r, nil)
}

// C2BPayment is an inbox row (unmatched paybill payments form the suspense queue).
type C2BPayment struct {
	TransID          string          `json:"trans_id"`
	BusinessShortcode string         `json:"business_shortcode"`
	Amount           decimal.Decimal `json:"amount"`
	BillRefNumber    string          `json:"bill_ref_number"`
	Msisdn           string          `json:"msisdn"`
	PayerName        string          `json:"payer_name"`
	TransTime        string          `json:"trans_time"`
	Status           string          `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
}

// SuspensePayments lists unreconciled paybill payments to the tenant's own shortcodes.
func (c *Client) SuspensePayments(ctx context.Context, tenantID uuid.UUID, since time.Time) ([]C2BPayment, error) {
	var out struct {
		Candidates []C2BPayment `json:"candidates"`
	}
	q := url.Values{"status": {"unreconciled"}}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	if err := c.get(ctx, tenantID, "/c2b/payments?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out.Candidates, nil
}

// AssignSuspense applies an unmatched paybill payment to a unit account (and remembers the route
// when remember is true, so the payer's next payment with the same reference matches by itself).
func (c *Client) AssignSuspense(ctx context.Context, tenantID uuid.UUID, transID string, accountID uuid.UUID, accountRef string) error {
	body := map[string]any{"reference_type": RefAccountPayment, "reference_id": accountID.String(), "account_ref": accountRef, "source_service": SourceService}
	return c.post(ctx, tenantID, "/c2b/payments/"+url.PathEscape(transID)+"/claim", "MSK-C2B-"+transID, body, nil)
}

// AccountLedger is treasury's view of one account reference: open invoices, payments, balance.
type AccountLedger struct {
	AccountRef   string          `json:"account_ref"`
	Balance      decimal.Decimal `json:"balance"`
	TotalBilled  decimal.Decimal `json:"total_billed"`
	TotalPaid    decimal.Decimal `json:"total_paid"`
	Credit       decimal.Decimal `json:"credit"`
	LastPaidAt   *time.Time      `json:"last_paid_at,omitempty"`
	Invoices     []LedgerInvoice `json:"invoices"`
	Payments     []LedgerPayment `json:"payments"`
}

// LedgerInvoice is one invoice line in the account ledger.
type LedgerInvoice struct {
	ID            uuid.UUID       `json:"id"`
	InvoiceNumber string          `json:"invoice_number"`
	InvoiceDate   time.Time       `json:"invoice_date"`
	DueDate       time.Time       `json:"due_date"`
	TotalAmount   decimal.Decimal `json:"total_amount"`
	AmountPaid    decimal.Decimal `json:"amount_paid"`
	PaymentStatus string          `json:"payment_status"`
	PublicToken   uuid.UUID       `json:"public_token"`
	Description   string          `json:"description"`
}

// LedgerPayment is one payment applied to the account.
type LedgerPayment struct {
	ID        uuid.UUID       `json:"id"`
	Amount    decimal.Decimal `json:"amount"`
	Method    string          `json:"method"`
	Reference string          `json:"reference"`
	PaidAt    time.Time       `json:"paid_at"`
}

// Ledger returns the account's invoices, payments and balance from treasury (authoritative).
func (c *Client) Ledger(ctx context.Context, tenantID uuid.UUID, accountRef string, limit int) (*AccountLedger, error) {
	var out AccountLedger
	q := url.Values{"limit": {fmt.Sprint(limit)}}
	if err := c.get(ctx, tenantID, "/accounts/"+url.PathEscape(accountRef)+"/ledger?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
