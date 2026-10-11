package handlers

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/modules/sales"
)

// StartHandover is POST /sale-contracts/{id}/handover {scheduled_at}: books the handover of a
// fully paid unit (asking again returns the same one).
func (h *H) StartHandover(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordContract, id) {
		return
	}
	var in struct {
		ScheduledAt *time.Time `json:"scheduled_at"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	ho, err := h.Sales.StartHandover(r.Context(), id, in.ScheduledAt)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ho)
}

// handoverInScope reads the handover id and checks the caller may act on its contract.
func (h *H) handoverInScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return id, false
	}
	contractID, err := h.Sales.HandoverContract(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return id, false
	}
	return id, h.scopeOf(w, r, register.RecordContract, contractID)
}

// UpdateHandover is PATCH /handovers/{id} {scheduled_at, keys, checklist, snags}.
func (h *H) UpdateHandover(w http.ResponseWriter, r *http.Request) {
	id, ok := h.handoverInScope(w, r)
	if !ok {
		return
	}
	var in sales.HandoverInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	ho, err := h.Sales.UpdateHandover(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ho)
}

// CompleteHandover is POST /handovers/{id}/complete {handed_over_at, readings: [{meter_id, reading}]}.
func (h *H) CompleteHandover(w http.ResponseWriter, r *http.Request) {
	id, ok := h.handoverInScope(w, r)
	if !ok {
		return
	}
	var in struct {
		HandedOverAt *time.Time              `json:"handed_over_at"`
		Readings     []sales.HandoverReading `json:"readings"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	at := time.Time{}
	if in.HandedOverAt != nil {
		at = *in.HandedOverAt
	}
	ho, err := h.Sales.CompleteHandover(r.Context(), id, actor(r), at, in.Readings)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ho)
}

// ContractTitle is GET /sale-contracts/{id}/title: the estate's title stages with this sale's progress.
func (h *H) ContractTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordContract, id) {
		return
	}
	rows, err := h.Sales.TitleStages(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// SetTitleStage is PUT /sale-contracts/{id}/title/{stage} {status, stage_date, reference, document_id, notes}.
func (h *H) SetTitleStage(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.scopeOf(w, r, register.RecordContract, id) {
		return
	}
	var in sales.TitleStageInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	rows, err := h.Sales.SetTitleStage(r.Context(), id, chiParam(r, "stage"), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}
