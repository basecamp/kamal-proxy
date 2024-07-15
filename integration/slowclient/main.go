package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	ChunkSize = 1024
	ChunkTime = 100 * time.Millisecond
	Chunks    = 30
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <url>\n", os.Args[0])
		os.Exit(1)
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		chunk := strings.Repeat("a", ChunkSize-1) + "\n"

		for i := 0; i < Chunks; i++ {
			pw.Write([]byte(chunk))
			fmt.Printf("%d of %d\n", i, Chunks)
			time.Sleep(ChunkTime)
		}
	}()

	req, err := http.NewRequest("POST", os.Args[1], pr)
	if err != nil {
		panic(err)
	}
	req.TransferEncoding = []string{"chunked"}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	fmt.Println("Response status:", resp.Status)
}
