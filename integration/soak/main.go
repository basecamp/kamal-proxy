package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func main() {
	runDuration := flag.Duration("run-duration", time.Second*10, "How long to run the test")
	concurrency := flag.Int("c", 4, "How many concurrent workers to run")
	rpm := flag.Int("rpm", 60, "Rate of requests per minute per worker")
	deployHostString := flag.String("deploy", "localhost:8000", "Targets to deploy to, comma separated")
	deployInterval := flag.Duration("deploy-interval", time.Minute, "How often to deploy")
	proxyURLString := flag.String("url", "http://localhost:8000/", "Proxy URL to use for requests")

	flag.Parse()

	deployHosts := strings.Split(*deployHostString, ",")
	proxyURL, err := url.Parse(*proxyURLString)
	if err != nil {
		panic(err)
	}

	runTest(*runDuration, *concurrency, *rpm, *deployInterval, deployHosts, proxyURL)
}

func runTest(runDuration time.Duration, concurrency int, rpm int, deployInterval time.Duration, deployHosts []string, proxyURL *url.URL) {
	var wg sync.WaitGroup
	wg.Add(concurrency + 1) // +1 for the deployer

	done := make(chan struct{})

	resultChannel := make(chan int, 1024)
	results := map[int]int{}
	resultsDone := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		go runWorker(done, resultChannel, &wg, rpm, proxyURL)
	}

	go func() {
		displayInterval := time.Minute
		lastDisplay := time.Now()

		for status := range resultChannel {
			results[status]++
			if time.Since(lastDisplay) > displayInterval {
				displayResults(results)
				lastDisplay = time.Now()
			}
		}

		close(resultsDone)
	}()

	go func() {
		<-time.After(runDuration)
		close(done)
	}()

	go runDeployer(done, &wg, deployInterval, deployHosts)

	wg.Wait()
	close(resultChannel)
	<-resultsDone

	displayResults(results)
}

func displayResults(results map[int]int) {
	fmt.Println("Results at", time.Now())
	for status, count := range results {
		fmt.Printf("- HTTP %d: %d\n", status, count)
	}
	fmt.Println()
}

func runWorker(done <-chan struct{}, resultChannel chan<- int, wg *sync.WaitGroup, rpm int, proxyURL *url.URL) {
	defer wg.Done()

	ticker := time.NewTicker(time.Minute / time.Duration(rpm))
	defer ticker.Stop()

	req, err := http.NewRequest("GET", proxyURL.String(), nil)
	if err != nil {
		panic(err)
	}

	for {
		select {
		case <-done:
			return

		case <-ticker.C:
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				panic(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			resultChannel <- resp.StatusCode
		}
	}
}

func runDeployer(done <-chan struct{}, wg *sync.WaitGroup, deployInterval time.Duration, targets []string) {
	defer wg.Done()

	ticker := time.NewTicker(deployInterval)
	defer ticker.Stop()

	idx := 0

	for {
		select {
		case <-done:
			return

		case <-ticker.C:
			target := targets[idx%len(targets)]
			idx++

			fmt.Println("Deploying to", target)
			cmd := exec.Command("docker", "compose", "exec", "proxy", "kamal-proxy", "deploy", "main", "--target", target)
			err := cmd.Run()
			fmt.Println("Deployed to", target, "err:", err)
		}
	}
}
