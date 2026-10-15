package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Bearer protects an HTTP handler with a shared token.
// Comparison hashes both sides so unequal lengths do not short-circuit.
func Bearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearerToken(r.Header.Get("Authorization"))
		sum := sha256.Sum256([]byte(got))
		if subtle.ConstantTimeCompare(want[:], sum[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}
