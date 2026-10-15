package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/works"
)

// TreasuryVendorLookup is GET /vendors/treasury-lookup?search=: payable suppliers in treasury to
// link a vendor to (at most 20).
func (h *H) TreasuryVendorLookup(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Works.TreasuryVendors(r.Context(), r.URL.Query().Get("search"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// LinkTreasuryVendor is PUT /vendors/{id}/treasury {treasury_vendor_id, name}.
func (h *H) LinkTreasuryVendor(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in struct {
		TreasuryVendorID uuid.UUID `json:"treasury_vendor_id"`
		Name             string    `json:"name"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if in.TreasuryVendorID == uuid.Nil {
		httpx.Fail(w, httpx.Invalid("pick the supplier from the accounts list"))
		return
	}
	v, err := h.Works.LinkTreasuryVendor(r.Context(), id, in.TreasuryVendorID, in.Name)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}

// CreateVendorContract is POST /vendors/{id}/contracts: a service contract for one property.
func (h *H) CreateVendorContract(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	var in works.ContractInput
	if !httpx.Decode(w, r, &in) || !requireProperty(w, r, in.PropertyID) {
		return
	}
	c, err := h.Works.CreateContract(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}

// contractInScope reads the contract id and checks the caller may act on its property.
func (h *H) contractInScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return id, false
	}
	pid, err := h.Works.ContractProperty(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return id, false
	}
	return id, requireProperty(w, r, pid)
}

// UpdateVendorContract is PUT /vendor-contracts/{id}.
func (h *H) UpdateVendorContract(w http.ResponseWriter, r *http.Request) {
	id, ok := h.contractInScope(w, r)
	if !ok {
		return
	}
	var in works.ContractInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	c, err := h.Works.UpdateContract(r.Context(), id, in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

// TerminateVendorContract is POST /vendor-contracts/{id}/terminate {reason}.
func (h *H) TerminateVendorContract(w http.ResponseWriter, r *http.Request) {
	id, ok := h.contractInScope(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	c, err := h.Works.TerminateContract(r.Context(), id, actor(r), in.Reason)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}
