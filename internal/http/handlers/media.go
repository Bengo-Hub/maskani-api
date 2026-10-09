package handlers

import (
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
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

// portalMediaKinds are the uploads an owner or occupant makes from the portal: request photos and
// their own meter reading photo.
var portalMediaKinds = map[string]bool{"works": true, "readings": true}

// maxImagePixels and maxImageSide stop decompression bombs: a small file that declares huge
// dimensions is refused from its header, before any pixel is decoded.
const (
	maxImagePixels = 40_000_000
	maxImageSide   = 12_000
)

// mayUseKind reports whether the caller may upload or sign media of a kind: staff any kind, portal
// users only the portal kinds.
func mayUseKind(r *http.Request, kind string) bool {
	a := access(r)
	if a == nil {
		return false
	}
	if a.IsStaff() {
		return mediaKinds[kind]
	}
	return len(a.PartyIDs) > 0 && portalMediaKinds[kind]
}

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
	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "only JPEG and PNG images are accepted")
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageSide || cfg.Height > maxImageSide ||
		int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		httpx.Error(w, http.StatusUnprocessableEntity, "validation_failed", "the image dimensions are too large")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "server_error", "could not read the file")
		return
	}
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
// Keys end in a random UUID and reach a caller only through reads that already passed their
// property or unit scope, so a key is the capability; portal users are further limited to the
// kinds the portal shows.
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
		if !strings.HasPrefix(k, prefix) || strings.Contains(k, "..") {
			continue
		}
		kind, _, _ := strings.Cut(strings.TrimPrefix(k, prefix), "/")
		if mayUseKind(r, kind) {
			out[k] = m.sign(k)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"urls": out})
}
