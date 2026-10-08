package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/imports"
)

// maxImportBytes bounds an upload; 5000 rows of the template fit well inside it.
const maxImportBytes = 4 << 20

// ImportTemplate is GET /imports/template: the CSV header with one example row.
func (h *H) ImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="maskani-units-owners-template.csv"`)
	_, _ = w.Write([]byte(imports.Template()))
}

// CreateImport is POST /imports (multipart: property_id, file). It validates only; nothing is written
// until the job is committed.
func (h *H) CreateImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		httpx.Fail(w, httpx.Invalid("upload a CSV file of at most 4 MB"))
		return
	}
	pid, err := uuid.Parse(r.FormValue("property_id"))
	if err != nil {
		httpx.Fail(w, httpx.Invalid("property_id is required"))
		return
	}
	if !requireProperty(w, r, pid) {
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Fail(w, httpx.Invalid("file is required"))
		return
	}
	defer f.Close()
	job, err := h.Imports.DryRun(r.Context(), actor(r), pid, hdr.Filename, f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, job)
}

// ListImports is GET /imports: the latest jobs without their row payloads.
func (h *H) ListImports(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Imports.Recent(r.Context(), intQuery(r, "limit", 20))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	a := access(r)
	out := rows[:0]
	for _, j := range rows {
		if j.PropertyID == nil || a.CanSeeProperty(*j.PropertyID) {
			out = append(out, j)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": out})
}

// GetImport is GET /imports/{id}: the job with its plan and errors (the UI polls it while committing).
func (h *H) GetImport(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	job, err := h.Imports.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if job.PropertyID != nil && !requireProperty(w, r, *job.PropertyID) {
		return
	}
	httpx.JSON(w, http.StatusOK, job)
}

// CommitImport is POST /imports/{id}/commit. It returns at once; the rows are applied in the background.
func (h *H) CommitImport(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.UUIDParam(w, r, "id")
	if !ok {
		return
	}
	job, err := h.Imports.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if job.PropertyID != nil && !requireProperty(w, r, *job.PropertyID) {
		return
	}
	job, err = h.Imports.Commit(r.Context(), actor(r), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, job)
}
