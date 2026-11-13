package middleware

import (
	"net/http"
	"strings"
)

// NoStore sets Cache-Control: no-store on every response under the given path prefixes, errors included.
func NoStore(prefixes []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range prefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				w.Header().Set("Cache-Control", "no-store")
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}
