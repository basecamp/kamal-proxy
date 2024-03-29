package server

import (
	"net/http"

	"github.com/google/uuid"
)

type RequestIDMiddleware struct {
	next http.Handler
}

func WithRequestIDMiddleware(next http.Handler) http.Handler {
	return &RequestIDMiddleware{
		next: next,
	}
}

func (h *RequestIDMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Request-ID") == "" {
		r.Header.Set("X-Request-ID", h.generateID())
	}
	h.next.ServeHTTP(w, r)
}

func (h *RequestIDMiddleware) generateID() string {
	return uuid.New().String()
}
