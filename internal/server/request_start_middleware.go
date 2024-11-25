package server

import (
	"net/http"
	"strconv"
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
		r.Header.Set(requestStartHeader, strconv.FormatInt(time.Now().UnixMilli(), 10))
	}
	h.next.ServeHTTP(w, r)
}
