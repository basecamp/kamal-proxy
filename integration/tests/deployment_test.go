package integration

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func TestZeroDowntimeDeployment(t *testing.T) {
	upstreamResponseTime := time.Millisecond * 20

	newUpstream := func() (*httptest.Server, *url.URL) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(upstreamResponseTime)
		}))
		t.Cleanup(upstream.Close)

		upstreamURL, _ := url.Parse(upstream.URL)
		return upstream, upstreamURL
	}

	_, upstream1URL := newUpstream()
	_, upstream2URL := newUpstream()

	proxy := testProxyServer(t)
	proxyURL, _ := url.Parse("http://" + proxy.Addr())
	proxy.LoadBalancer().Add([]*url.URL{upstream1URL}, true)

	clients := newClientConsumer(t, proxyURL)

	time.Sleep(time.Second * 2)

	proxy.LoadBalancer().Add([]*url.URL{upstream2URL}, true)
	proxy.LoadBalancer().Remove([]*url.URL{upstream1URL})

	time.Sleep(time.Second * 2)

	defer clients.Close()

	require.Equal(t, http.StatusOK, clients.minStatusCode)
	require.Equal(t, http.StatusOK, clients.maxStatusCode)
}

// Private helpers

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
