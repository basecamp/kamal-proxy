package integration

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/kevinmcconnell/mproxy/pkg/server"
	"github.com/stretchr/testify/require"
)

func TestZeroDowntimeDeployment(t *testing.T) {
	upstreamResponseTime := time.Millisecond * 20

	newUpstream := func() (*httptest.Server, server.Host) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(upstreamResponseTime)
		}))
		t.Cleanup(upstream.Close)

		upstreamURL, _ := url.Parse(upstream.URL)
		host, _ := server.NewHost(upstreamURL.Host)
		return upstream, host
	}

	_, host1 := newUpstream()
	_, host2 := newUpstream()

	proxy := testProxyServer(t)
	proxyURL, _ := url.Parse("http://" + proxy.Addr())

	proxy.LoadBalancer().Add(server.Hosts{host1}, true)

	clients := newClientConsumer(t, proxyURL)

	time.Sleep(time.Second * 2)

	proxy.LoadBalancer().Add(server.Hosts{host2}, true)
	proxy.LoadBalancer().Remove(server.Hosts{host1})

	time.Sleep(time.Second * 2)

	defer clients.Close()

	require.Equal(t, http.StatusOK, clients.minStatusCode)
	require.Equal(t, http.StatusOK, clients.maxStatusCode)
}
