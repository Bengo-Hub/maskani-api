package handlers

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/billing"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/sales"
	"github.com/bengobox/maskani-api/internal/modules/settings"
	"github.com/bengobox/maskani-api/internal/shared/page"
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

// ChargeCatalogue is GET /charge-types/catalogue: the platform's standard charges this estate has
// not added yet, for "Add from the catalogue".
func (h *H) ChargeCatalogue(w http.ResponseWriter, r *http.Request) {
	have, err := h.Billing.ListCharges(r.Context(), true)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	taken := make(map[string]bool, len(have))
	for _, c := range have {
		taken[c.Code] = true
	}
	out := make([]map[string]string, 0, len(settings.ChargeDefaults))
	for _, d := range settings.ChargeDefaults {
		if taken[d.Code] {
			continue
		}
		out = append(out, map[string]string{"code": d.Code, "name": d.Name, "charge_group": d.Group, "basis": d.Basis,
			"frequency": d.Frequency, "bill_to": d.BillTo, "fund_code": d.Fund})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
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

// ListRuns is GET /billing-runs?property_id= (keyset page).
func (h *H) ListRuns(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	pid := httpx.QueryUUID(r, "property_id")
	if pid != nil && !requireProperty(w, r, *pid) {
		return
	}
	res, err := h.Billing.ListRuns(r.Context(), pid, a.PropertyIDs, a.AllProperties, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// runScope checks the caller may see a run's property.
func (h *H) runScope(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	pid, err := h.Billing.RunPropertyID(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return false
	}
	return requireProperty(w, r, pid)
}

// GetRun is GET /billing-runs/{id}: the run with line counts by status.
func (h *H) GetRun(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	v, err := h.Billing.GetRun(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !requireProperty(w, r, v.PropertyID) {
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// RunLines is GET /billing-runs/{id}/lines.
func (h *H) RunLines(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.runScope(w, r, id) {
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
	if !ok || !h.runScope(w, r, id) {
		return
	}
	run, err := h.Billing.Retry(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, run)
}

// ListAccounts is GET /unit-accounts?property_id=&owing=&fund= (keyset page). Without property_id a
// property-limited user sees only accounts in their properties.
func (h *H) ListAccounts(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	pid := httpx.QueryUUID(r, "property_id")
	if pid != nil && !requireProperty(w, r, *pid) {
		return
	}
	res, err := h.Collections.ListAccounts(r.Context(), pid, a.PropertyIDs, a.AllProperties,
		r.URL.Query().Get("owing") == "true", r.URL.Query().Get("fund"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// Statement is GET /unit-accounts/{id}/statement (staff, property scoped).
func (h *H) Statement(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.accountScope(w, r, id) {
		return
	}
	h.statement(w, r, id)
}

// statement writes the account statement; callers have already authorised the account.
func (h *H) statement(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
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
	if !h.accountScope(w, r, id) {
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
	if !h.accountScope(w, r, in.AccountID) {
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
	if !requireProperty(w, r, *pid) {
		return
	}
	rows, err := h.Sales.ListPriceLists(r.Context(), *pid)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Availability is GET /availability?property_id=&status=: the sales board grouped by block.
func (h *H) Availability(w http.ResponseWriter, r *http.Request) {
	pid := httpx.QueryUUID(r, "property_id")
	if pid == nil || !requireProperty(w, r, *pid) {
		if pid == nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "property_id is required")
		}
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !sales.ValidSaleStatus(status) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unknown sale status")
		return
	}
	groups, err := h.Sales.Availability(r.Context(), *pid, status)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// Reserve is POST /reservations.
func (h *H) Reserve(w http.ResponseWriter, r *http.Request) {
	var in sales.ReserveInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if !h.unitScope(w, r, in.UnitID) {
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
	if !h.unitScope(w, r, in.UnitID) {
		return
	}
	sc, err := h.Sales.CreateContract(r.Context(), actor(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, sc)
}

// ListContracts is GET /sale-contracts?property_id=&status= (keyset page).
func (h *H) ListContracts(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	pid := httpx.QueryUUID(r, "property_id")
	if pid != nil && !requireProperty(w, r, *pid) {
		return
	}
	res, err := h.Sales.ListContracts(r.Context(), pid, a.PropertyIDs, a.AllProperties, r.URL.Query().Get("status"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// ListReservations is GET /reservations?property_id=&status= (keyset page).
func (h *H) ListReservations(w http.ResponseWriter, r *http.Request) {
	a := access(r)
	pid := httpx.QueryUUID(r, "property_id")
	if pid != nil && !requireProperty(w, r, *pid) {
		return
	}
	res, err := h.Sales.ListReservations(r.Context(), pid, a.PropertyIDs, a.AllProperties, r.URL.Query().Get("status"), page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// GetContract is GET /sale-contracts/{id}.
func (h *H) GetContract(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordContract, id) {
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
	if !h.scopeOf(w, r, register.RecordContract, id) {
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
	if !h.scopeOf(w, r, register.RecordInstalment, id) {
		return
	}
	v, err := h.Sales.ReleaseMilestone(r.Context(), id, actor(r), in.EvidenceKey)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}
