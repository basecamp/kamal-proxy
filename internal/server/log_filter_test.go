package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogPathFilter_Filter(t *testing.T) {
	assertFiltered := func(patterns []string, path, expected string) {
		t.Helper()
		filter, err := NewLogPathFilter(patterns)
		require.NoError(t, err)
		assert.Equal(t, expected, filter.Filter(path))
	}

	// Without capture groups, the whole match is replaced
	assertFiltered([]string{`secret-[a-z0-9]+`}, "/files/secret-abc123/download", "/files/[FILTERED]/download")

	// With capture groups, only what each group matches is replaced
	assertFiltered([]string{`/invites/([^/]+)`}, "/invites/abc123", "/invites/[FILTERED]")
	assertFiltered([]string{`/invites/([^/]+)`}, "/42/invites/abc123/accept", "/42/invites/[FILTERED]/accept")
	assertFiltered([]string{`^/shares/([^/]+)/files/([^/]+)`}, "/shares/key1/files/key2/photo.jpg", "/shares/[FILTERED]/files/[FILTERED]/photo.jpg")

	// A group can span several segments
	assertFiltered([]string{`/feeds/(.+)/calendar`}, "/feeds/abc/def==/calendar.ics", "/feeds/[FILTERED]/calendar.ics")

	// Every match is replaced
	assertFiltered([]string{`/t/([^/]+)`}, "/t/one/t/two", "/t/[FILTERED]/t/[FILTERED]")

	// Every pattern applies, each matched against the original path
	assertFiltered([]string{`/invites/([^/]+)`, `/tokens/([^/]+)`}, "/invites/abc/tokens/def", "/invites/[FILTERED]/tokens/[FILTERED]")
	assertFiltered([]string{`^/accounts/([^/]+)`, `^/accounts/[0-9]+/invites/([^/]+)`}, "/accounts/42/invites/abc", "/accounts/[FILTERED]/invites/[FILTERED]")

	// Nested and optional groups
	assertFiltered([]string{`/a/(([^/]+)/b)`}, "/a/x/b/c", "/a/[FILTERED]/c")
	assertFiltered([]string{`/a/([^/]+)(?:/b/([^/]+))?`}, "/a/x", "/a/[FILTERED]")
	assertFiltered([]string{`/a/([^/]+)(?:/b/([^/]+))?`}, "/a/x/b/y", "/a/[FILTERED]/b/[FILTERED]")

	// Empty matches replace nothing
	assertFiltered([]string{`x*`}, "/abc", "/abc")
	assertFiltered([]string{`/a/([^/]*)`}, "/a/", "/a/")

	// Paths that don't match are unchanged
	assertFiltered([]string{`/invites/([^/]+)`}, "/projects/1", "/projects/1")
	assertFiltered(nil, "/invites/abc123", "/invites/abc123")
}

func TestLogPathFilter_InvalidPattern(t *testing.T) {
	_, err := NewLogPathFilter([]string{`/invites/([^/]+`})
	require.ErrorContains(t, err, "invalid log filter path")
}

func TestServiceOptions_ValidateLogFilterPaths(t *testing.T) {
	require.NoError(t, ServiceOptions{LogFilterPaths: []string{`/invites/([^/]+)`}}.Validate())

	err := ServiceOptions{LogFilterPaths: []string{`/invites/([^/]+`}}.Validate()
	require.ErrorContains(t, err, "invalid log filter path")
	require.ErrorIs(t, err, ErrServiceOptionsInvalid)
}

func TestService_LogFilterPaths(t *testing.T) {
	serviceOptions := defaultServiceOptions
	serviceOptions.LogFilterPaths = []string{`/invites/([^/]+)`}

	t.Run("request log", func(t *testing.T) {
		service := testCreateService(t, serviceOptions, defaultTargetOptions)
		logs := testServeLogged(t, service, "/invites/secret-value/accept?page=2")

		line := testLogLine(t, logs, "Request")
		assert.Equal(t, "/invites/[FILTERED]/accept", line["path"])
		assert.Equal(t, "page=2", line["query"])
		assert.NotContains(t, logs, "secret-value")
	})

	t.Run("error log when the target can't be reached", func(t *testing.T) {
		service := testCreateServiceWithHandler(t, serviceOptions, defaultTargetOptions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/invites/") {
				conn, _, err := w.(http.Hijacker).Hijack()
				require.NoError(t, err)
				conn.Close()
			}
		}))
		logs := testServeLogged(t, service, "/invites/secret-value")

		assert.Equal(t, "/invites/[FILTERED]", testLogLine(t, logs, "Error while proxying")["path"])
		assert.Equal(t, "/invites/[FILTERED]", testLogLine(t, logs, "Request")["path"])
		assert.NotContains(t, logs, "secret-value")
	})

	t.Run("error log when the request body can't be buffered", func(t *testing.T) {
		targetOptions := defaultTargetOptions
		targetOptions.BufferRequests = true
		service := testCreateService(t, serviceOptions, targetOptions)

		req := httptest.NewRequest(http.MethodPost, "http://example.com/invites/secret-value", failingReader{})
		logs := testServeRequestLogged(t, service, req)

		assert.Equal(t, "/invites/[FILTERED]", testLogLine(t, logs, "Error buffering request")["path"])
		assert.NotContains(t, logs, "secret-value")
	})

	t.Run("request log when the service is stopped", func(t *testing.T) {
		service := testCreateService(t, serviceOptions, defaultTargetOptions)
		require.NoError(t, service.Stop(time.Second, DefaultStopMessage))
		logs := testServeLogged(t, service, "/invites/secret-value")

		assert.Equal(t, "/invites/[FILTERED]", testLogLine(t, logs, "Request")["path"])
		assert.NotContains(t, logs, "secret-value")
	})

	t.Run("rollout debug log", func(t *testing.T) {
		service := testCreateService(t, serviceOptions, defaultTargetOptions)
		service.UpdateLoadBalancer(NewLoadBalancer(service.active.Targets(), DefaultWriterAffinityTimeout, false), TargetSlotRollout)
		require.NoError(t, service.SetRolloutSplit(100, nil))
		require.NoError(t, service.EnableRollout())

		req := httptest.NewRequest(http.MethodGet, "http://example.com/invites/secret-value", nil)
		req.AddCookie(&http.Cookie{Name: RolloutCookieName, Value: "first"})
		logs := testServeRequestLogged(t, service, req)

		assert.Equal(t, "/invites/[FILTERED]", testLogLine(t, logs, "Using rollout for request")["path"])
		assert.NotContains(t, logs, "secret-value")
	})

	t.Run("error log for a service under a stripped path prefix", func(t *testing.T) {
		_, target := testBackendWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/invites/") {
				conn, _, err := w.(http.Hijacker).Hijack()
				require.NoError(t, err)
				conn.Close()
			}
		})

		prefixedOptions := defaultServiceOptions
		prefixedOptions.PathPrefixes = []string{"/app"}
		prefixedOptions.StripPrefix = true
		prefixedOptions.LogFilterPaths = []string{`^/app/invites/([^/]+)`}

		router := NewRouter("")
		require.NoError(t, router.DeployService("app", []string{target}, defaultEmptyReaders, prefixedOptions, defaultTargetOptions, defaultDeploymentOptions))

		req := httptest.NewRequest(http.MethodGet, "http://example.com/app/invites/secret-value", nil)
		logs := testServeHandlerLogged(t, router, req)

		assert.Equal(t, "/app/invites/[FILTERED]", testLogLine(t, logs, "Error while proxying")["path"])
		assert.Equal(t, "/app/invites/[FILTERED]", testLogLine(t, logs, "Request")["path"])
		assert.NotContains(t, logs, "secret-value")
	})

	t.Run("filter survives saving and restoring state", func(t *testing.T) {
		service := testCreateService(t, serviceOptions, defaultTargetOptions)

		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(service))
		var restored Service
		require.NoError(t, json.NewDecoder(&buf).Decode(&restored))
		t.Cleanup(restored.Dispose)

		logs := testServeLogged(t, &restored, "/invites/secret-value")
		assert.Equal(t, "/invites/[FILTERED]", testLogLine(t, logs, "Request")["path"])
	})

	t.Run("services without a filter log the path as is", func(t *testing.T) {
		service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)
		logs := testServeLogged(t, service, "/invites/plain-value")

		assert.Equal(t, "/invites/plain-value", testLogLine(t, logs, "Request")["path"])
	})
}

// Helpers

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, assert.AnError }

func testServeLogged(t *testing.T, service *Service, path string) string {
	t.Helper()
	return testServeRequestLogged(t, service, httptest.NewRequest(http.MethodGet, "http://example.com"+path, nil))
}

func testServeRequestLogged(t *testing.T, service *Service, req *http.Request) string {
	t.Helper()
	return testServeHandlerLogged(t, service, req)
}

// testServeHandlerLogged serves the request through the logging middleware,
// capturing the request log line and anything logged along the way, debug
// lines included.
func testServeHandlerLogged(t *testing.T, handler http.Handler, req *http.Request) string {
	t.Helper()

	out := &strings.Builder{}
	logger := slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	WithLoggingMiddleware(logger, 80, 443, handler).ServeHTTP(httptest.NewRecorder(), req)
	return out.String()
}

func testLogLine(t *testing.T, logs, message string) map[string]any {
	t.Helper()

	for raw := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		line := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(raw), &line))
		if line["msg"] == message {
			return line
		}
	}

	require.Failf(t, "log line not found", "no %q line in:\n%s", message, logs)
	return nil
}
