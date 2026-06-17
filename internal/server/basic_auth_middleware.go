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
	usernameHash [sha256.Size]byte
	passwordHash []byte
	next         http.Handler
}

func WithBasicAuthMiddleware(username, passwordHash string, next http.Handler) http.Handler {
	// If the stored hash is malformed (bad hex or not a SHA-256 digest), fall back
	// to a fixed-length zero hash. No real password hashes to all zeroes, so every
	// request fails closed while comparisons still run over equal-length inputs.
	decoded, err := hex.DecodeString(passwordHash)
	if err != nil || len(decoded) != sha256.Size {
		decoded = make([]byte, sha256.Size)
	}

	return &BasicAuthMiddleware{
		usernameHash: sha256.Sum256([]byte(username)),
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
	givenUser := sha256.Sum256([]byte(username))
	givenPassword := sha256.Sum256([]byte(password))

	userMatch := subtle.ConstantTimeCompare(givenUser[:], h.usernameHash[:]) == 1
	passwordMatch := subtle.ConstantTimeCompare(givenPassword[:], h.passwordHash) == 1

	return userMatch && passwordMatch
}
