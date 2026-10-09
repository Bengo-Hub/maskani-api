package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/sequence"
)

// ListSequences is GET /document-sequences: numbering for every kind with its next number.
func (h *H) ListSequences(w http.ResponseWriter, r *http.Request) {
	v, err := h.Sequences.Configs(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": v})
}

// SaveSequence is PUT /document-sequences/{kind} {prefix, format, pad_width, reset_period, next_value}.
func (h *H) SaveSequence(w http.ResponseWriter, r *http.Request) {
	var in sequence.Update
	if !httpx.Decode(w, r, &in) {
		return
	}
	v, err := h.Sequences.Save(r.Context(), chi.URLParam(r, "kind"), in)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, v)
}
