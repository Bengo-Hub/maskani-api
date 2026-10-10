package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/reminders"
)

// CallList is GET /collections/call-list?property_id=: accounts the collections ladder put on
// the call list that still owe, largest first, with the last note and any promise to pay.
func (h *H) CallList(w http.ResponseWriter, r *http.Request) {
	f, ok := scopeFilter(w, r)
	if !ok {
		return
	}
	ids, all := f.Scope, f.AllProperties
	if f.PropertyID != nil {
		ids, all = []uuid.UUID{*f.PropertyID}, false
	}
	rows, err := h.Reminders.CallList(r.Context(), ids, all || access(r).Bypass)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// AccountCollections is GET /unit-accounts/{id}/collections: the account's place on the ladder
// (steps done, call list, promise to pay) and its notes.
func (h *H) AccountCollections(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.accountScope(w, r, id) {
		return
	}
	l, err := h.Reminders.AccountLadder(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}

// AddCollectionNote is POST /unit-accounts/{id}/collection-notes {outcome, promise_date, note}.
func (h *H) AddCollectionNote(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok || !h.accountScope(w, r, id) {
		return
	}
	var in reminders.NoteInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	by := access(r).Email
	if u := access(r).LocalUser; u != nil && u.Name != "" {
		by = u.Name
	}
	l, err := h.Reminders.AddNote(r.Context(), id, by, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, l)
}
