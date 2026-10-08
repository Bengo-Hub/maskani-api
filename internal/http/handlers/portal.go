package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/collections"
	"github.com/bengobox/maskani-api/internal/modules/gate"
	"github.com/bengobox/maskani-api/internal/modules/works"
	"github.com/bengobox/maskani-api/internal/shared/page"
)

// MyUnits is GET /me/units.
func (h *H) MyUnits(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Portal.Units(r.Context(), access(r).PartyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// MyStatement is GET /me/accounts/{id}/statement.
func (h *H) MyStatement(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Portal.OwnsAccount(r.Context(), access(r).PartyIDs, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	h.statement(w, r, id)
}

// MyPay is POST /me/accounts/{id}/pay.
func (h *H) MyPay(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	if err := h.Portal.OwnsAccount(r.Context(), access(r).PartyIDs, id); err != nil {
		httpx.Fail(w, err)
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

// MyPurchase is GET /me/purchase.
func (h *H) MyPurchase(w http.ResponseWriter, r *http.Request) {
	cs, err := h.Portal.Contracts(r.Context(), access(r).PartyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	out := []any{}
	for _, c := range cs {
		if v, err := h.Sales.GetContract(r.Context(), c.ID); err == nil {
			out = append(out, v)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *H) portalParty(r *http.Request) *uuid.UUID {
	if a := access(r); a != nil && len(a.PartyIDs) > 0 {
		return &a.PartyIDs[0]
	}
	return nil
}

// MyCreatePass is POST /me/passes.
func (h *H) MyCreatePass(w http.ResponseWriter, r *http.Request) {
	var in gate.PassInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.UnitID == nil {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "unit_id is required")
		return
	}
	if err := h.Portal.OwnsUnit(r.Context(), access(r).PartyIDs, *in.UnitID); err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := h.Register.GetUnit(r.Context(), *in.UnitID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	in.PropertyID = u.PropertyID
	p, err := h.Gate.CreatePass(r.Context(), "resident", actor(r), h.portalParty(r), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

// MyPasses is GET /me/passes.
func (h *H) MyPasses(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Gate.ListPasses(r.Context(), nil, access(r).PartyIDs, r.URL.Query().Get("active") == "true", 100)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// MyCancelPass is POST /me/passes/{id}/cancel.
func (h *H) MyCancelPass(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.Gate.ListPasses(r.Context(), nil, access(r).PartyIDs, false, 500)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	for _, p := range rows {
		if p.ID == id {
			if err := h.Gate.CancelPass(r.Context(), id); err != nil {
				httpx.Fail(w, err)
				return
			}
			httpx.JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
			return
		}
	}
	httpx.Error(w, http.StatusNotFound, "not_found", "pass not found")
}

// MyRequests is GET /me/requests.
func (h *H) MyRequests(w http.ResponseWriter, r *http.Request) {
	pid := h.portalParty(r)
	res, err := h.Works.List(r.Context(), works.Filter{PartyID: pid, AllProperties: true}, page.Parse(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}

// MyCreateRequest is POST /me/requests.
func (h *H) MyCreateRequest(w http.ResponseWriter, r *http.Request) {
	var in works.RequestInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.UnitID == nil {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "unit_id is required")
		return
	}
	if err := h.Portal.OwnsUnit(r.Context(), access(r).PartyIDs, *in.UnitID); err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := h.Register.GetUnit(r.Context(), *in.UnitID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	in.PropertyID = u.PropertyID
	if in.Priority == "emergency" {
		in.Priority = "high"
	}
	wo, err := h.Works.Create(r.Context(), works.Actor{UserID: actor(r), PartyID: h.portalParty(r), Kind: "resident"}, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, wo)
}

// MyRequestAction is POST /me/requests/{id}/actions (confirm or reopen).
func (h *H) MyRequestAction(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	wo, err := h.Works.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	owned := false
	for _, p := range access(r).PartyIDs {
		if wo.RequestedByPartyID != nil && *wo.RequestedByPartyID == p {
			owned = true
		}
	}
	if !owned {
		httpx.Error(w, http.StatusForbidden, "forbidden", "this request is not yours")
		return
	}
	var in works.ActionInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	out, err := h.Works.Act(r.Context(), id, works.Actor{UserID: actor(r), PartyID: h.portalParty(r), Kind: "resident"}, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// MyNotices is GET /me/notices.
func (h *H) MyNotices(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Portal.Notices(r.Context(), access(r).PartyIDs)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// MyAcceptTerms is POST /me/terms/accept.
func (h *H) MyAcceptTerms(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version string `json:"version"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if err := h.Portal.AcceptTerms(r.Context(), access(r).PartyIDs, in.Version); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// MyDecideWalkIn is POST /me/walk-ins/{id}/decide {approve}.
func (h *H) MyDecideWalkIn(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		Approve bool `json:"approve"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	cur, err := h.Gate.Event(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if cur.HostUnitID == nil {
		httpx.Error(w, http.StatusForbidden, "forbidden", "this visitor has no host unit")
		return
	}
	if err := h.Portal.OwnsUnit(r.Context(), access(r).PartyIDs, *cur.HostUnitID); err != nil {
		httpx.Fail(w, err)
		return
	}
	ev, err := h.Gate.Decide(r.Context(), id, in.Approve)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ev)
}
