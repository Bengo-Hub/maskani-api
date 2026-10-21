package middleware

import (
	"net/http"

	httpware "github.com/Bengo-Hub/httpware"
)

// RequireInternalKey admits only sibling services presenting the fleet INTERNAL_SERVICE_KEY in
// X-API-Key (compared in constant time by httpware). Used for internal read endpoints such as the resident
// reach list notifications-api pages through; an empty configured key refuses everything.
func RequireInternalKey(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !httpware.ValidServiceKey(r, key) {
				WriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
