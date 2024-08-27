package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func upHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func helloHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, "<body>Hello from <strong>%s</strong> at <strong>%s</strong></body>\n",
			host,
			time.Now().Format(time.RFC3339),
		)
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
