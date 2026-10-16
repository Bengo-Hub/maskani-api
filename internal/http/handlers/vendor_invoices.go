package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/approvals"
	"github.com/bengobox/maskani-api/internal/modules/works"
)

// invoiceInScope reads the {id} invoice and checks its property is the caller's.
func (h *H) invoiceInScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return id, false
	}
	pid, err := h.Works.InvoiceProperty(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return id, false
	}
	return id, requireProperty(w, r, pid)
}

// CreateVendorInvoice is POST /vendor-invoices: an invoice as the vendor issued it, sent for approval.
func (h *H) CreateVendorInvoice(w http.ResponseWriter, r *http.Request) {
	var in works.InvoiceInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	pid, err := h.Works.ContractProperty(r.Context(), in.ContractID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, pid) {
		return
	}
	inv, err := h.Works.CreateInvoice(r.Context(), in, actor(r), displayName(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, inv)
}

// ListVendorInvoices is GET /vendor-invoices?property_id=&vendor_id=&status= (newest first, 200).
func (h *H) ListVendorInvoices(w http.ResponseWriter, r *http.Request) {
	ids, all, ok := scopeIDs(w, r)
	if !ok {
		return
	}
	rows, err := h.Works.Invoices(r.Context(), works.InvoiceFilter{PropertyID: httpx.QueryUUID(r, "property_id"),
		VendorID: httpx.QueryUUID(r, "vendor_id"), Status: r.URL.Query().Get("status"), Scope: ids, All: all})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CheckVendorInvoice is GET /vendor-invoices/{id}/check: the invoice beside its contract fee, the
// visits done and missed in its period and the service credit due.
func (h *H) CheckVendorInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := h.invoiceInScope(w, r)
	if !ok {
		return
	}
	c, err := h.Works.CheckInvoice(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

// DecideVendorInvoice is POST /vendor-invoices/{id}/decide {decision: approve|reject, note}.
func (h *H) DecideVendorInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := h.invoiceInScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	d := approvals.Approve
	switch in.Decision {
	case "approve":
	case "reject":
		d = approvals.Reject
	default:
		httpx.Fail(w, httpx.Invalid("decision must be approve or reject"))
		return
	}
	inv, err := h.Works.DecideInvoice(r.Context(), id, approver(r), d, in.Note)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}

// CancelVendorInvoice is POST /vendor-invoices/{id}/cancel {reason}: withdraws an unpaid invoice;
// an approved one also has treasury void its bill.
func (h *H) CancelVendorInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := h.invoiceInScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	inv, err := h.Works.CancelInvoice(r.Context(), id, actor(r), in.Reason)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}
