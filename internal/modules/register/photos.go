package register

import (
	"context"
	"strings"

	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// maxPhotos bounds a property's or unit's gallery; the public page shows the first five.
const maxPhotos = 12

// cleanPhotos checks a gallery before it is saved: each entry is a media key this tenant uploaded
// under the given kind ("properties" or "units"), listed once, in the order given (the first is the
// cover). A signed link sent back by an edit form is stored as its plain key.
func cleanPhotos(ctx context.Context, kind string, keys []string) ([]string, error) {
	tid, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	prefix := "tenants/" + tid.String() + "/" + kind + "/"
	out := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if i := strings.Index(k, "/media/"); i >= 0 {
			k = k[i+len("/media/"):]
		}
		k, _, _ = strings.Cut(k, "?")
		if !strings.HasPrefix(k, prefix) || strings.Contains(k, "..") {
			return nil, httpx.Invalid("photos must be uploaded here first (kind " + kind + ")")
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	if len(out) > maxPhotos {
		return nil, httpx.Invalid("keep to 12 photos")
	}
	return out, nil
}
