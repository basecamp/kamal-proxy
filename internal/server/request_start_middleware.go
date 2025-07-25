package server

import (
	"fmt"
	"net/http"
	"time"
)

const (
	requestStartHeader = "X-Request-Start"
)

type RequestStartMiddleware struct {
	next http.Handler
}

func WithRequestStartMiddleware(next http.Handler) http.Handler {
	return &RequestStartMiddleware{
		next: next,
	}
}

func (h *RequestStartMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(requestStartHeader) == "" {
		r.Header.Set(requestStartHeader, fmt.Sprintf("t=%d", time.Now().UnixMilli()))
	}
	h.next.ServeHTTP(w, r)
}
