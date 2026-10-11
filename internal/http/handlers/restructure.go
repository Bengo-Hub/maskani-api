package handlers

import (
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/sales"
)

// ProposeRestructure is POST /sale-contracts/{id}/restructure {reason, option, frequency,
// term_months, first_due}: a new plan for what is not yet billed, sent for approval.
func (h *H) ProposeRestructure(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordContract, id) {
		return
	}
	var in sales.RestructureInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.Sales.ProposeRestructure(r.Context(), id, actor(r), displayName(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, v)
}

// ContractSchedules is GET /sale-contracts/{id}/schedules: every plan version, newest first.
func (h *H) ContractSchedules(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordContract, id) {
		return
	}
	rows, err := h.Sales.Schedules(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// scheduleInScope reads the schedule id and checks the caller may act on its contract.
func (h *H) scheduleInScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return id, false
	}
	sc, err := h.Sales.ScheduleContract(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return id, false
	}
	return id, h.scopeOf(w, r, register.RecordContract, sc.ID)
}

// SendPlanCode is POST /schedules/{id}/send-code: a code to the buyer's phone and email on record,
// for accepting the plan at the office.
func (h *H) SendPlanCode(w http.ResponseWriter, r *http.Request) {
	id, ok := h.scheduleInScope(w, r)
	if !ok {
		return
	}
	if err := h.Sales.SendRestructureCode(r.Context(), id); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// AcceptPlanWithCode is POST /schedules/{id}/accept {code}: the buyer reads the code back at the office.
func (h *H) AcceptPlanWithCode(w http.ResponseWriter, r *http.Request) {
	id, ok := h.scheduleInScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.Sales.AcceptRestructure(r.Context(), id, sales.Acceptance{Via: "code", Code: in.Code, Person: actor(r)})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// MyAcceptPlan is POST /me/plans/{id}/accept: the signed-in buyer accepts an approved new plan
// on their own contract.
func (h *H) MyAcceptPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	sc, err := h.Sales.ScheduleContract(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !slices.Contains(access(r).PartyIDs, sc.PrimaryBuyerID) {
		httpx.Fail(w, httpx.Forbidden("only the buyer can accept this plan"))
		return
	}
	v, err := h.Sales.AcceptRestructure(r.Context(), id, sales.Acceptance{Via: "portal", Person: actor(r)})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}
