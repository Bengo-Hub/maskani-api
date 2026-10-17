package handlers

import (
	"io"
	"net/http"
	"strings"

	"github.com/Bengo-Hub/httpware/objectstore"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// Media stores uploads on the shared object storage (httpware/objectstore) at
// maskani/<tenant>/<kind>/<yyyy>/<mm>/<uuid>.<ext>. Estate and unit photos are published images on
// the public bucket; every other kind is private and reached only through presigned, short-lived
// links (SRDD 12.4, 17.2). Images are re-encoded, which strips EXIF (location, device) and
// neutralises crafted files.
type Media struct {
	Store *objectstore.Store
	Log   *zap.Logger
}

// maxMediaBytes is the upload limit for every kind.
const maxMediaBytes = 8 << 20

// mediaKinds are the upload kinds; true marks the published (public) ones.
var mediaKinds = map[string]bool{"readings": false, "works": false, "properties": true, "units": true,
	"documents": false, "incidents": false, "vendors": false, "evidence": false}

// portalMediaKinds are the uploads an owner or occupant makes from the portal: request photos and
// their own meter reading photo.
var portalMediaKinds = map[string]bool{"works": true, "readings": true}

// RegisterMediaTypes sets maskani's media rules on the shared registry. Call once at startup.
// "documents" also holds the issued PDFs, which the documents module stores directly.
func RegisterMediaTypes() {
	for kind, public := range mediaKinds {
		objectstore.Register(objectstore.MediaType{Name: kind, Public: public, MaxBytes: maxMediaBytes,
			Allowed: []string{"image/jpeg", "image/png"}, Image: true})
	}
}

// mayUseKind reports whether the caller may upload or sign media of a kind: staff any kind, portal
// users only the portal kinds.
func mayUseKind(r *http.Request, kind string) bool {
	a := access(r)
	if a == nil {
		return false
	}
	if _, ok := mediaKinds[kind]; !ok {
		return false
	}
	if a.IsStaff() {
		return true
	}
	return len(a.PartyIDs) > 0 && portalMediaKinds[kind]
}

// Upload is POST /media/upload (multipart: file, kind).
func (m *Media) Upload(w http.ResponseWriter, r *http.Request) {
	if !m.Store.Enabled() {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "media storage is not set up")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMediaBytes+64<<10)
	if err := r.ParseMultipartForm(maxMediaBytes); err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "file too large (max 8 MB)")
		return
	}
	kind := r.FormValue("kind")
	if _, ok := mediaKinds[kind]; !ok {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "invalid kind")
		return
	}
	if !mayUseKind(r, kind) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "you cannot upload this kind of file")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "file is required")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxMediaBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "could not read the file")
		return
	}
	// Type from the bytes, size limit, dimension check and re-encode (shared rules).
	up, err := objectstore.Prepare(raw, kind)
	if err != nil {
		if objectstore.IsUploadError(err) {
			httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "server_error", "could not store the file")
		return
	}
	key, err := m.Store.NewKey(objectstore.Owner{TenantID: access(r).TenantID.String()}, kind, up.Ext)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "server_error", "could not store the file")
		return
	}
	if err := m.Store.Put(r.Context(), key, up.Data, up.ContentType); err != nil {
		m.Log.Error("media store failed", zap.Error(err))
		httpx.Error(w, http.StatusBadGateway, "server_error", "could not store the file")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"key": key, "url": m.Store.URL(key)})
}

// Sign is POST /media/sign {keys:[...]}: links for keys that belong to the caller's tenant
// (presigned for private kinds, the public link for estate and unit photos). Keys end in a random
// UUID and reach a caller only through reads that already passed their property or unit scope, so
// a key is the capability; portal users are further limited to the kinds the portal shows.
func (m *Media) Sign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys []string `json:"keys"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	prefix := "maskani/" + access(r).TenantID.String() + "/"
	out := map[string]string{}
	for i, k := range in.Keys {
		if i >= 200 {
			break
		}
		if !strings.HasPrefix(k, prefix) || !m.Store.Owned(k) {
			continue
		}
		kind, _, _ := strings.Cut(strings.TrimPrefix(k, prefix), "/")
		if mayUseKind(r, kind) {
			out[k] = m.Store.URL(k)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"urls": out})
}
