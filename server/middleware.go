package server

import (
	"crypto/subtle"
	"net/http"

	"github.com/madeddie/opds-aggregator/config"
)

// BasicAuth returns middleware that enforces HTTP Basic Auth.
// If auth is nil, the middleware is a no-op passthrough.
func BasicAuth(auth *config.AuthConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if auth == nil || auth.Username == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(auth.Username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(auth.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="OPDS Aggregator"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
