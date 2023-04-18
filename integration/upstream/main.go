package main

import (
	"fmt"
	"net/http"
	"os"
)

func newHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("host: %s request: %s\n", host, r.URL)

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, `
<html>
  <head><title>Hello world</title></head>
	<body>Hello from %s</body>
</html>
		`, host)
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
