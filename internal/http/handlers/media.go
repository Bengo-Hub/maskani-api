package handlers

import (
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Bengo-Hub/httpware"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/http/httpx"
)

// Media stores uploads under {root}/tenants/{tenant_id}/{kind}/{yyyy-mm}/ and serves them only
// through signed, short-lived links (SRDD 12.4, 17.2). Images are re-encoded to JPEG, which strips
// EXIF (location, device) and neutralises crafted files.
type Media struct {
	Root    string
	URLBase string
	MaxMB   int
	Signer  *httpware.MediaSigner
	Log     *zap.Logger
}

var mediaKinds = map[string]bool{"readings": true, "works": true, "properties": true, "units": true,
	"documents": true, "incidents": true, "vendors": true, "evidence": true}

// Upload is POST /media/upload (multipart: file, kind).
func (m *Media) Upload(w http.ResponseWriter, r *http.Request) {
	max := int64(m.MaxMB) << 20
	if max <= 0 {
		max = 8 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	if err := r.ParseMultipartForm(max); err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("file too large (max %d MB)", max>>20))
		return
	}
	kind := r.FormValue("kind")
	if !mediaKinds[kind] {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "invalid kind")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "file is required")
		return
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "only JPEG and PNG images are accepted")
		return
	}
	a := access(r)
	rel := filepath.ToSlash(filepath.Join("tenants", a.TenantID.String(), kind, time.Now().Format("2006-01"),
		uuid.New().String()+".jpg"))
	dst := filepath.Join(m.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		m.Log.Error("media mkdir failed", zap.Error(err))
		httpx.Error(w, http.StatusInternalServerError, "server_error", "could not store the file")
		return
	}
	f, err := os.Create(dst)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "server_error", "could not store the file")
		return
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 82}); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "server_error", "could not store the file")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"key": rel, "url": m.sign(rel)})
}

func (m *Media) sign(key string) string {
	u := "/media/" + key
	if m.URLBase != "" {
		u = strings.TrimRight(m.URLBase, "/") + u
	}
	return m.Signer.Sign(u)
}

// Sign is POST /media/sign {keys:[...]}: signed links for keys that belong to the caller's tenant.
func (m *Media) Sign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Keys []string `json:"keys"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	prefix := "tenants/" + access(r).TenantID.String() + "/"
	out := map[string]string{}
	for i, k := range in.Keys {
		if i >= 200 {
			break
		}
		if strings.HasPrefix(k, prefix) && !strings.Contains(k, "..") {
			out[k] = m.sign(k)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"urls": out})
}
