package middleware

import (
	"net/http"
	"strings"
)

// StreamPathSuffix is the only route that accepts a token in the query string.
const StreamPathSuffix = "/maskani/stream"

// StreamQueryToken lets the browser EventSource (which cannot send headers) authenticate the SSE
// route with ?token=. It copies the token into the Authorization header when none is present and
// removes it from the URL so nothing downstream logs or echoes it. The token then goes through the
// same JWKS validation as any bearer token (the logistics-api AuthenticateWS pattern). Every other
// route ignores the query parameter.
func StreamQueryToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), StreamPathSuffix) {
			q := r.URL.Query()
			if tok := strings.TrimSpace(q.Get("token")); tok != "" {
				if r.Header.Get("Authorization") == "" {
					r.Header.Set("Authorization", "Bearer "+tok)
				}
				q.Del("token")
				r.URL.RawQuery = q.Encode()
			}
		}
		next.ServeHTTP(w, r)
	})
}
