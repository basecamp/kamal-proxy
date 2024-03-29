package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

func newHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)

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
