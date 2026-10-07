package handlers

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/sales"
)

// ListFunds is GET /funds.
func (h *H) ListFunds(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Billing.ListFunds(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// UpdateFund is PATCH /funds/{id}.
func (h *H) UpdateFund(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in billing.FundInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	f, err := h.Billing.UpdateFund(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, f)
}

// ListCharges is GET /charge-types.
func (h *H) ListCharges(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Billing.ListCharges(r.Context(), r.URL.Query().Get("all") == "true")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// CreateCharge is POST /charge-types.
func (h *H) CreateCharge(w http.ResponseWriter, r *http.Request) {
	var in billing.ChargeInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	c, err := h.Billing.CreateCharge(r.Context(), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}

// EnableCharge is POST /charge-types/enable {code}: copy a platform default into the catalogue.
func (h *H) EnableCharge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := h.Settings.EnableCharge(r.Context(), access(r).TenantID, in.Code, 100); err != nil {
		httpx.Fail(w, httpx.Invalid(err.Error()))
		return
	}
	h.ListCharges(w, r)
}

// UpdateCharge is PATCH /charge-types/{id}.
func (h *H) UpdateCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in billing.ChargeInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	c, err := h.Billing.UpdateCharge(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

// AddRate is POST /charge-types/{id}/rates.
func (h *H) AddRate(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in billing.RateInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rate, err := h.Billing.AddRate(r.Context(), id, actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rate)
}

// PreviewRun is POST /billing-runs/preview.
func (h *H) PreviewRun(w http.ResponseWriter, r *http.Request) {
	var in billing.IssueInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.Fund == "" {
		in.Fund = "estate"
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	pv, err := h.Billing.Compute(r.Context(), in.PropertyID, in.Fund, in.Period)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, pv)
}

// IssueRun is POST /billing-runs.
func (h *H) IssueRun(w http.ResponseWriter, r *http.Request) {
	var in billing.IssueInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	run, err := h.Billing.Issue(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	h.Reports.Invalidate(access(r).TenantID)
	httpx.JSON(w, http.StatusAccepted, run)
}

// ListRuns is GET /billing-runs.
func (h *H) ListRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Billing.ListRuns(r.Context(), httpx.QueryUUID(r, "property_id"), intQuery(r, "limit", 24))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// RunLines is GET /billing-runs/{id}/lines.
func (h *H) RunLines(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.Billing.RunLines(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// RetryRun is POST /billing-runs/{id}/retry.
func (h *H) RetryRun(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	run, err := h.Billing.Retry(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, run)
}

// ListAccounts is GET /unit-accounts.
func (h *H) ListAccounts(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	rows, err := h.Collections.ListAccounts(r.Context(), httpx.QueryUUID(r, "property_id"), a.PropertyIDs, a.AllProperties,
		r.URL.Query().Get("owing") == "true", intQuery(r, "limit", 500))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Statement is GET /unit-accounts/{id}/statement.
func (h *H) Statement(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	st, err := h.Collections.Statement(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, st)
}

// StaffPay is POST /unit-accounts/{id}/pay (staff prompts an owner's phone).
func (h *H) StaffPay(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in collections.PayInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	res, err := h.Collections.Pay(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, res)
}

// Suspense is GET /collections/suspense?days=.
func (h *H) Suspense(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Collections.Suspense(r.Context(), time.Now().AddDate(0, 0, -intQuery(r, "days", 60)))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// AssignSuspense is POST /collections/suspense/{trans_id}/assign.
func (h *H) AssignSuspense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID uuid.UUID `json:"unit_account_id"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := h.Collections.Assign(r.Context(), chiParam(r, "trans_id"), in.AccountID); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

// --- Sales ---

// CreatePriceList is POST /price-lists.
func (h *H) CreatePriceList(w http.ResponseWriter, r *http.Request) {
	var in sales.PriceListInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !requireProperty(w, r, in.PropertyID) {
		return
	}
	pl, err := h.Sales.CreatePriceList(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, pl)
}

// ListPriceLists is GET /price-lists?property_id=.
func (h *H) ListPriceLists(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		return
	}
	rows, err := h.Sales.ListPriceLists(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Availability is GET /availability?property_id=.
func (h *H) Availability(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil || !requireProperty(w, r, *pid) {
		if pid == nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		}
		return
	}
	rows, err := h.Sales.Availability(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Reserve is POST /reservations.
func (h *H) Reserve(w http.ResponseWriter, r *http.Request) {
	var in sales.ReserveInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	res, err := h.Sales.Reserve(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, res)
}

// CreateContract is POST /sale-contracts.
func (h *H) CreateContract(w http.ResponseWriter, r *http.Request) {
	var in sales.ContractInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	sc, err := h.Sales.CreateContract(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sc)
}

// ListContracts is GET /sale-contracts.
func (h *H) ListContracts(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Sales.ListContracts(r.Context(), httpx.QueryUUID(r, "property_id"), r.URL.Query().Get("status"), intQuery(r, "limit", 200))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// GetContract is GET /sale-contracts/{id}.
func (h *H) GetContract(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	v, err := h.Sales.GetContract(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// ActivateContract is POST /sale-contracts/{id}/activate.
func (h *H) ActivateContract(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		SignedAt *time.Time `json:"signed_at"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	at := time.Time{}
	if in.SignedAt != nil {
		at = *in.SignedAt
	}
	sc, err := h.Sales.Activate(r.Context(), id, at)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, sc)
}

// ReleaseMilestone is POST /instalments/{id}/release.
func (h *H) ReleaseMilestone(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		EvidenceKey string `json:"evidence_key"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.Sales.ReleaseMilestone(r.Context(), id, actor(r), in.EvidenceKey)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}
