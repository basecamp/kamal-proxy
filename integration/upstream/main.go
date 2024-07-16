package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func newHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/up" {
			w.WriteHeader(http.StatusOK)
			return
		}

		started := time.Now()
		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)

		io.Copy(io.Discard, r.Body)
		slog.Info("Read body", "duration", time.Since(started))

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, "<body>Hello from %s</body>\n", host)
	}
}

func main() {
	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/", newHandler(host))
	panic(http.ListenAndServe(":3000", nil))
}
