package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

const basicAuthRealm = `Basic realm="Restricted", charset="UTF-8"`

// HashBasicAuthCredential returns the hex-encoded SHA-256 digest of a Basic Auth
// credential. The password is hashed before it is stored in the service options,
// so the proxy never persists it in plaintext.
func HashBasicAuthCredential(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type BasicAuthMiddleware struct {
	username     string
	passwordHash []byte
	next         http.Handler
}

func WithBasicAuthMiddleware(username, passwordHash string, next http.Handler) http.Handler {
	decoded, err := hex.DecodeString(passwordHash)
	if err != nil {
		decoded = nil
	}

	return &BasicAuthMiddleware{
		username:     username,
		passwordHash: decoded,
		next:         next,
	}
}

func (h *BasicAuthMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authenticated(r) {
		h.next.ServeHTTP(w, r)
		return
	}

	w.Header().Set("WWW-Authenticate", basicAuthRealm)
	SetErrorResponse(w, r, http.StatusUnauthorized, nil)
}

// Private

func (h *BasicAuthMiddleware) authenticated(r *http.Request) bool {
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}

	// Hash both sides so the comparisons run in constant time over equal-length
	// inputs, regardless of the supplied credential lengths.
	expectedUser := sha256.Sum256([]byte(h.username))
	givenUser := sha256.Sum256([]byte(username))
	givenPassword := sha256.Sum256([]byte(password))

	userMatch := subtle.ConstantTimeCompare(givenUser[:], expectedUser[:]) == 1
	passwordMatch := subtle.ConstantTimeCompare(givenPassword[:], h.passwordHash) == 1

	return userMatch && passwordMatch
}
