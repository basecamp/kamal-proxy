package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
)

func upHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func maintenaceHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, "<body><strong>%s</strong> undergoing maintenance.</body>\n",
			host,
		)
	}
}

func main() {
	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/up", upHandler)
	http.HandleFunc("/", maintenaceHandler(host))

	panic(http.ListenAndServe(":80", nil))
}
