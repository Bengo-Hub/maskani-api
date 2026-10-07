package handlers

import (
	"net"
	"net/http"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/market"
)

// PublicEstates is GET /api/v1/market/estates.
func (h *H) PublicEstates(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Market.Estates(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=120")
	httpx.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// PublicEstate is GET /api/v1/market/estates/{slug}.
func (h *H) PublicEstate(w http.ResponseWriter, r *http.Request) {
	e, err := h.Market.Estate(r.Context(), chiParam(r, "slug"))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=120")
	httpx.JSON(w, http.StatusOK, e)
}

// PublicEnquiry is POST /api/v1/market/enquiries.
func (h *H) PublicEnquiry(w http.ResponseWriter, r *http.Request) {
	var in market.EnquiryInput
	if !httpx.Decode(w, r, &in) {
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if err := h.Market.Enquire(r.Context(), ip, in); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "received"})
}
