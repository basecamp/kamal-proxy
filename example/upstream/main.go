package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func upHandler(w http.ResponseWriter, r *http.Request) {
	// slog.Info("Health request", "method", r.Method, "url", r.URL)
	w.WriteHeader(http.StatusOK)
}

func helloHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if host != "web1" {
			slog.Info("Setting X-Reproxy header", "host", host)
			w.Header().Set("X-Reproxy", "web1")
		}

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, "<body>Hello from <strong>%s</strong> at <strong>%s</strong></body>\n",
			host,
			time.Now().Format(time.RFC3339),
		)

		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)
		body, err := io.ReadAll(r.Body)
		slog.Info("Request body", "body", string(body), "error", err)
	}
}

func main() {
	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/up", upHandler)
	http.HandleFunc("/", helloHandler(host))

	panic(http.ListenAndServe(":80", nil))
}
