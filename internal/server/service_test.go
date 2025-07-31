package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_ServeRequest(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/", strings.NewReader(""))
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestService_RedirectToHTTPSWhenTLSRequired(t *testing.T) {
	service := testCreateService(t, ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true, TLSRedirect: true}, defaultTargetOptions)

	require.True(t, service.options.TLSEnabled)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusMovedPermanently, w.Result().StatusCode)
	require.Equal(t, "https://example.com/", w.Result().Header.Get("Location"))

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
}

func TestService_DontRedirectToHTTPSWhenTLSAndPlainHTTPAllowed(t *testing.T) {
	var forwardedProto string

	service := testCreateServiceWithHandler(t, ServiceOptions{Hosts: []string{"example.com"}, TLSEnabled: true, TLSRedirect: false}, defaultTargetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwardedProto = r.Header.Get("X-Forwarded-Proto")
		}),
	)

	require.True(t, service.options.TLSEnabled)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Equal(t, "http", forwardedProto)

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)
	assert.Equal(t, "https", forwardedProto)
}

func TestService_UseStaticTLSCertificateWhenConfigured(t *testing.T) {
	certPath, keyPath := prepareTestCertificateFiles(t)

	service := testCreateService(
		t,
		ServiceOptions{
			Hosts:              []string{"example.com"},
			TLSEnabled:         true,
			TLSCertificatePath: certPath,
			TLSPrivateKeyPath:  keyPath,
		},
		defaultTargetOptions,
	)

	require.IsType(t, &StaticCertManager{}, service.certManager)
}

func TestService_RejectTLSRequestsWhenNotConfigured(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)

	require.False(t, service.options.TLSEnabled)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	w := httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Result().StatusCode)

	req = httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	w = httptest.NewRecorder()
	service.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Result().StatusCode)
}

func TestService_ReturnSuccessfulHealthCheckWhilePausedOrStopped(t *testing.T) {
	service := testCreateService(t, defaultServiceOptions, defaultTargetOptions)

	checkRequest := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		return w.Result().StatusCode
	}

	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusOK, checkRequest("/other"))

	service.Pause(time.Second, time.Millisecond)
	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusGatewayTimeout, checkRequest("/other"))

	service.Stop(time.Second, DefaultStopMessage)
	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusServiceUnavailable, checkRequest("/other"))

	service.Resume()
	assert.Equal(t, http.StatusOK, checkRequest("/up"))
	assert.Equal(t, http.StatusOK, checkRequest("/other"))
}

func TestService_EnforceMaxBodySizes(t *testing.T) {
	sendRequest := func(bufferRequests, bufferResponses, dynamicLoadBalancing bool, maxMemorySize, maxBodySize int64, requestBody, responseBody string) *httptest.ResponseRecorder {
		targetOptions := TargetOptions{
			BufferRequests:      bufferRequests,
			BufferResponses:     bufferResponses,
			MaxMemoryBufferSize: maxMemorySize,
			MaxRequestBodySize:  maxBodySize,
			MaxResponseBodySize: maxBodySize,
			HealthCheckConfig:   defaultHealthCheckConfig,
		}

		options := defaultServiceOptions
		options.DynamicLoadBalancing = dynamicLoadBalancing

		service := testCreateServiceWithHandler(t, options, targetOptions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(responseBody))
		}))

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(requestBody))
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)
		return w
	}

	t.Run("without buffering", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(false, false, false, 1, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(false, false, false, 1, 10, "request limits are not enforced when not buffering", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(false, false, false, 1, 10, "hello", "response limits are not enforced when not buffering")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "response limits are not enforced when not buffering", string(w.Body.String()))
		})
	})

	t.Run("with dynamic load balancing", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(false, false, true, 1, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(false, false, true, 1, 10, "request limits are enforced when dynamic load balancing is enabled", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(false, false, true, 1, 10, "hello", "response limits are not enforced by dynamic load balancing")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "response limits are not enforced by dynamic load balancing", string(w.Body.String()))
		})
	})

	t.Run("with buffering but no additional disk limit", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 10, 10, "hello", "this response is too large")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})

	t.Run("with buffering and a separate disk limit", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 5, 20, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for memory but within the limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 5, 20, "hello hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 5, 20, "hello hello hello hello hello", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for memory but within the limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 5, 20, "hello", "hello hello")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "hello hello", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, true, false, 5, 20, "hello", "this is even longer than the disk limit")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})

	t.Run("when buffering requests but not responses", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(true, false, false, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(true, false, false, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusRequestEntityTooLarge, w.Result().StatusCode)
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(true, false, false, 10, 10, "hello", "this response is very large")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "this response is very large", string(w.Body.String()))
		})
	})

	t.Run("when buffering responses but not requests", func(t *testing.T) {
		t.Run("within limit", func(t *testing.T) {
			w := sendRequest(false, true, false, 10, 10, "hello", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("request too large for the limit", func(t *testing.T) {
			w := sendRequest(false, true, false, 10, 10, "this one is too large", "ok")

			require.Equal(t, http.StatusOK, w.Result().StatusCode)
			require.Equal(t, "ok", string(w.Body.String()))
		})

		t.Run("response too large for the limit", func(t *testing.T) {
			w := sendRequest(false, true, false, 10, 10, "hello", "this response is very large")

			require.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		})
	})
}

func TestService_Reproxying(t *testing.T) {
	deployedTargets := TargetList{nil, nil}

	testReproxy := func(h http.HandlerFunc, expectedStatus int, expectedBody string) {
		options := defaultServiceOptions
		options.DynamicLoadBalancing = true

		service, targets := testCreateServiceWithHandlers(t, options, defaultTargetOptions,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)

				fmt.Fprint(w, "Reproxied: "+string(body))
			}),

			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/up" {
					w.WriteHeader(http.StatusOK)
					return
				}

				h(w, r)
			}),
		)
		defer service.Dispose()

		copy(deployedTargets, targets)

		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("something"))
		w := httptest.NewRecorder()

		service.ServeHTTP(w, req)

		assert.Equal(t, expectedStatus, w.Result().StatusCode)
		assert.Equal(t, expectedBody, w.Body.String())
	}

	t.Run("Basic reproxying", func(t *testing.T) {
		testReproxy(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add(reproxyHeaderName, "http://"+deployedTargets[0].targetURL.Host+"/new")
			w.WriteHeader(http.StatusSeeOther)
			w.Write([]byte("This should be discarded!"))
		}, 200, "Reproxied: something")
	})

	t.Run("Successful requests are not reproxied", func(t *testing.T) {
		testReproxy(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add(reproxyHeaderName, "http://"+deployedTargets[0].targetURL.Host+"/new")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("This should not be discarded!"))
		}, 200, "This should not be discarded!")
	})

	t.Run("Reproxying to a host that doesn't exist", func(t *testing.T) {
		testReproxy(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add(reproxyHeaderName, "http://whatever:1234/broken")
			w.WriteHeader(http.StatusSeeOther)
		}, 503, "Service Unavailable\n")
	})
}

func TestService_MarshallingState(t *testing.T) {
	targetOptions := TargetOptions{
		HealthCheckConfig:   HealthCheckConfig{Path: "/health", Interval: time.Second, Timeout: 2 * time.Second},
		BufferRequests:      true,
		MaxMemoryBufferSize: 123,
	}

	service := testCreateService(t, defaultServiceOptions, targetOptions)
	t.Cleanup(service.Dispose)
	require.NoError(t, service.Stop(time.Second, DefaultStopMessage))
	service.UpdateLoadBalancer(NewLoadBalancer(service.active.Targets(), DefaultWriterAffinityTimeout, false), TargetSlotRollout)

	require.NoError(t, service.SetRolloutSplit(20, []string{"first"}))

	var buf bytes.Buffer
	err := json.NewEncoder(&buf).Encode(service)
	require.NoError(t, err)

	var service2 Service
	err = json.NewDecoder(&buf).Decode(&service2)
	require.NoError(t, err)
	t.Cleanup(service2.Dispose)

	assert.Equal(t, service.name, service2.name)
	assert.Equal(t, service.active.Targets().Names(), service2.active.Targets().Names())
	assert.Equal(t, service.targetOptions, service2.targetOptions)

	assert.Equal(t, PauseStateStopped, service2.pauseController.GetState())
	assert.Equal(t, DefaultStopMessage, service2.pauseController.GetStopMessage())

	assert.Equal(t, 20, service2.rolloutController.Percentage)
	assert.Equal(t, []string{"first"}, service2.rolloutController.Allowlist)
}

func TestService_UnmarshallingStateFromLegacyFormat(t *testing.T) {
	state := `
	  {
		"name": "my-app",
		"hosts": ["app.example.com"],
		"active_target": "localhost:3000",
		"rollout_target": "",
		"options": {
		  "tls_enabled": false,
		  "tls_certificate_path": "",
		  "tls_private_key_path": "",
		  "acme_directory": "",
		  "acme_cache_path": "",
		  "error_page_path": ""
		},
		"target_options": {
		  "health_check_config": {
			"path": "/up",
			"interval": 1000000000,
			"timeout": 5000000000
		  },
		  "response_timeout": 3000000000,
		  "buffer_requests": false,
		  "buffer_responses": false,
		  "max_memory_buffer_size": 1048576,
		  "max_request_body_size": 0,
		  "max_response_body_size": 0,
		  "log_request_headers": null,
		  "log_response_headers": null,
		  "forward_headers": true
		},
		"pause_controller": {
		  "state": 0,
		  "stop_message": "",
		  "fail_after": 0
		},
		"rollout_controller": null
	  }
	`

	var service Service
	err := json.NewDecoder(strings.NewReader(state)).Decode(&service)
	require.NoError(t, err)
	t.Cleanup(service.Dispose)

	assert.Equal(t, "my-app", service.name)
	assert.Equal(t, []string{"localhost:3000"}, service.active.Targets().Names())
	assert.Equal(t, []string{"app.example.com"}, service.options.Hosts)
	assert.Equal(t, []string{"/"}, service.options.PathPrefixes)
	assert.Equal(t, 3*time.Second, service.targetOptions.ResponseTimeout)
}

func testCreateService(t *testing.T, options ServiceOptions, targetOptions TargetOptions) *Service {
	return testCreateServiceWithHandler(t, options, targetOptions,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	)
}

func testCreateServiceWithHandler(t *testing.T, options ServiceOptions, targetOptions TargetOptions, handler http.Handler) *Service {
	service, _ := testCreateServiceWithHandlers(t, options, targetOptions, handler)
	return service
}

func testCreateServiceWithHandlers(t *testing.T, options ServiceOptions, targetOptions TargetOptions, handlers ...http.Handler) (*Service, TargetList) {
	targets := Map(handlers, func(h http.Handler) *Target {
		server := httptest.NewServer(h)
		t.Cleanup(server.Close)

		serverURL, err := url.Parse(server.URL)
		require.NoError(t, err)

		target, err := NewTarget(serverURL.Host, targetOptions)
		require.NoError(t, err)

		return target
	})

	service, err := NewService("test", options, targetOptions)
	require.NoError(t, err)

	targetList := TargetList(targets)

	service.UpdateLoadBalancer(NewLoadBalancer(targetList, DefaultWriterAffinityTimeout, false), TargetSlotActive)
	service.active.WaitUntilHealthy(time.Second)

	return service, targetList
}
