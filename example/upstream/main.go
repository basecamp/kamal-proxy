package main

import (
	"cmp"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func upHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func helloHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host = cmp.Or(r.Header.Get("X-Kamal-Target"), host)
		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)

		reproxyTo := r.URL.Query().Get("rt")
		if reproxyTo != "" {
			w.Header().Add("X-Kamal-Reproxy-Location", "http://"+reproxyTo)
			w.WriteHeader(http.StatusSeeOther)
			return
		}

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
