package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBasicAuthMiddleware(t *testing.T) {
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	handler := WithBasicAuthMiddleware("admin", HashBasicAuthCredential("secret"), next)

	send := func(setAuth func(*http.Request)) *httptest.ResponseRecorder {
		reached = false
		r := httptest.NewRequest("GET", "/", nil)
		if setAuth != nil {
			setAuth(r)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	t.Run("allows requests with correct credentials", func(t *testing.T) {
		w := send(func(r *http.Request) { r.SetBasicAuth("admin", "secret") })

		assert.True(t, reached)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("rejects requests with no credentials", func(t *testing.T) {
		w := send(nil)

		assert.False(t, reached)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, basicAuthRealm, w.Header().Get("WWW-Authenticate"))
	})

	t.Run("rejects requests with a wrong password", func(t *testing.T) {
		w := send(func(r *http.Request) { r.SetBasicAuth("admin", "wrong") })

		assert.False(t, reached)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("rejects requests with a wrong username", func(t *testing.T) {
		w := send(func(r *http.Request) { r.SetBasicAuth("root", "secret") })

		assert.False(t, reached)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestBasicAuthMiddleware_FailsClosedWithInvalidHash(t *testing.T) {
	for name, passwordHash := range map[string]string{
		"non-hex hash":      "not-a-valid-hash",
		"wrong-length hash": "abcd",
		"empty hash":        "",
	} {
		t.Run(name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
			})
			handler := WithBasicAuthMiddleware("admin", passwordHash, next)

			r := httptest.NewRequest("GET", "/", nil)
			r.SetBasicAuth("admin", "secret")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assert.False(t, reached, "request must not reach the backend")
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, basicAuthRealm, w.Header().Get("WWW-Authenticate"))
		})
	}
}
