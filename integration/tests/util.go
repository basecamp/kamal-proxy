package integration

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kevinmcconnell/mproxy/pkg/server"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func testProxyServer(t *testing.T, handlers ...http.HandlerFunc) *server.Server {
	configDir, err := os.MkdirTemp("", "")
	require.NoError(t, err)
	t.Cleanup(func() {
		os.RemoveAll(configDir)
	})

	proxyServer := server.NewServer(server.Config{
		ConfigDir:          configDir,
		AddTimeout:         time.Second,
		DrainTimeout:       time.Second,
		MaxRequestBodySize: 1024,
		HealthCheckConfig: server.HealthCheckConfig{
			HealthCheckPath:     server.DefaultHealthCheckPath,
			HealthCheckInterval: server.DefaultHealthCheckInterval,
			HealthCheckTimeout:  server.DefaultHealthCheckTimeout,
		},
	})
	err = proxyServer.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		proxyServer.Stop()
	})

	for _, handler := range handlers {
		upstream := httptest.NewServer(http.HandlerFunc(handler))
		upstreamURL, _ := url.Parse(upstream.URL)
		host, _ := server.NewHost(upstreamURL.Host)

		proxyServer.LoadBalancer().Add(server.Hosts{host}, true)

		t.Cleanup(func() {
			proxyServer.LoadBalancer().Remove(server.Hosts{host})
			upstream.Close()
		})
	}

	return proxyServer
}

type clientConsumer struct {
	wg                           sync.WaitGroup
	done                         chan bool
	minStatusCode, maxStatusCode int
}

func newClientConsumer(t *testing.T, target *url.URL) *clientConsumer {
	clientCount := 8

	consumer := clientConsumer{
		done: make(chan bool),
	}

	consumer.wg.Add(clientCount)
	for i := 0; i < clientCount; i++ {
		go func() {
			for {
				select {
				case <-consumer.done:
					consumer.wg.Done()
					return
				default:
					resp, err := http.Get(target.String())
					statusCode := http.StatusInternalServerError
					if err == nil {
						statusCode = resp.StatusCode

					}

					if consumer.minStatusCode == 0 || consumer.minStatusCode > statusCode {
						consumer.minStatusCode = statusCode
					}

					if consumer.maxStatusCode == 0 || consumer.maxStatusCode < statusCode {
						consumer.maxStatusCode = statusCode
					}
				}
			}
		}()
	}

	return &consumer
}

func (c *clientConsumer) Close() {
	log.Debug().Msg("Closing client")
	close(c.done)
	c.wg.Wait()
}
