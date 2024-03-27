package main

import (
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"time"
)

func newHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)

		time.Sleep(time.Millisecond * time.Duration(rand.Intn(100)))

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, "<body>Hello from %s</body>\n", host)
	}
}

func main() {
	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":3000"
	}

	http.HandleFunc("/", newHandler(host))

	slog.Info("listening", "addr", addr)
	panic(http.ListenAndServe(addr, nil))
}
