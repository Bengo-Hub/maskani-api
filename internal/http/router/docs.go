package router

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
)

var routes chi.Routes

// docsHandler serves /v1/docs/: the live route table walked from the router itself, so it can never
// drift from the code. Request and response shapes are in docs/api-spec.md.
func docsHandler(w http.ResponseWriter, r *http.Request) {
	type row struct{ method, path string }
	var rows []row
	if routes != nil {
		_ = chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if !strings.HasPrefix(route, "/v1/docs") {
				rows = append(rows, row{method, route})
			}
			return nil
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].path == rows[j].path {
			return rows[i].method < rows[j].method
		}
		return rows[i].path < rows[j].path
	})
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>Maskani API</title>
<style>body{font-family:system-ui,sans-serif;margin:24px;color:#1f2937}h1{color:#6E1A5A}td{padding:4px 12px;border-bottom:1px solid #eee;font-family:ui-monospace,monospace;font-size:13px}td.m{font-weight:600;color:#4E1240}</style>
</head><body><h1>Maskani API</h1><p>Live route table. Tenant routes need a bearer token; gate routes need X-Device-Key; S2S calls send X-API-Key and X-Tenant-ID. Shapes and rules: docs/api-spec.md in the maskani-api repository.</p><table>`)
	for _, r := range rows {
		fmt.Fprintf(&b, `<tr><td class="m">%s</td><td>%s</td></tr>`, html.EscapeString(r.method), html.EscapeString(r.path))
	}
	b.WriteString(`</table></body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
