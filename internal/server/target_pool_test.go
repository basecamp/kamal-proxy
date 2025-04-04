package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetPool_MultipleTargets(t *testing.T) {
	// Create a pool with multiple targets
	pool := NewTargetPool()
	
	// Create test backends with different responses
	_, target1 := testBackend(t, "target1", http.StatusOK)
	_, target2 := testBackend(t, "target2", http.StatusOK)
	_, target3 := testBackend(t, "target3", http.StatusOK)
	
	// Create targets
	tgt1, err := NewTarget(target1, defaultTargetOptions)
	require.NoError(t, err)
	tgt1.state = TargetStateHealthy
	
	tgt2, err := NewTarget(target2, defaultTargetOptions)
	require.NoError(t, err)
	tgt2.state = TargetStateHealthy
	
	tgt3, err := NewTarget(target3, defaultTargetOptions)
	require.NoError(t, err)
	tgt3.state = TargetStateHealthy
	
	// Add all targets to the pool
	pool.AddTarget(tgt1)
	pool.AddTarget(tgt2)
	pool.AddTarget(tgt3)
	
	// Verify the count
	assert.Equal(t, 3, pool.Count())
	
	t.Run("load balancing", func(t *testing.T) {
		selectedTargets := make(map[string]int)
		
		for i := 0; i < 9; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			target, _, err := pool.StartRequest(req)
			require.NoError(t, err)
			
			targetURL := target.Target()
			selectedTargets[targetURL]++
		}
		
		assert.Equal(t, 3, len(selectedTargets))
		for _, count := range selectedTargets {
			assert.Equal(t, 3, count)
		}
	})
	
	t.Run("replace targets", func(t *testing.T) {
		oldTargets := pool.ReplaceTargets([]*Target{tgt1, tgt2})
		assert.Equal(t, 3, len(oldTargets))
		assert.Equal(t, 2, pool.Count())
		
		pool.AddTarget(tgt3)
		assert.Equal(t, 3, pool.Count())
	})
	
	t.Run("remove target", func(t *testing.T) {
		pool.RemoveTarget(tgt2)
		assert.Equal(t, 2, pool.Count())
		
		targets := pool.GetTargets()
		assert.Equal(t, 2, len(targets))
		
		pool.AddTarget(tgt2)
	})
	
	t.Run("handle target failure", func(t *testing.T) {		
		originalState := tgt1.updateState(TargetStateDraining)
		selectedTargets := make(map[string]int)
		
		for i := 0; i < 10; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			target, reqWithContext, err := pool.StartRequest(req)
			require.NoError(t, err)
			require.NotNil(t, target)
			require.NotNil(t, reqWithContext)
			
			targetURL := target.Target()
			selectedTargets[targetURL]++
			
			// Verify the selected target is not the draining one
			assert.NotEqual(t, tgt1.targetURL.Host, targetURL)
		}
		
		// Verify distribution of requests
		assert.Equal(t, 0, selectedTargets[tgt1.Target()])
		assert.Greater(t, selectedTargets[tgt2.Target()], 0)
		assert.Greater(t, selectedTargets[tgt3.Target()], 0)
		
		tgt1.updateState(originalState)
		tgt1.state = TargetStateHealthy
	})
	
	t.Run("concurrent requests", func(t *testing.T) {
		totalRequests := 100
		var wg sync.WaitGroup
		wg.Add(totalRequests)
		
		// Track hit counts safely with a mutex
		var mu sync.Mutex
		hitCounts := make(map[string]int)
		
		// Send requests concurrently
		for i := 0; i < totalRequests; i++ {
			go func() {
				defer wg.Done()
				
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				target, _, err := pool.StartRequest(req)
				require.NoError(t, err)
				
				mu.Lock()
				hitCounts[target.Target()]++
				mu.Unlock()
			}()
		}
		
		wg.Wait()
		
		// Verify all targets were used and the distribution is approximately even
		assert.Equal(t, 3, len(hitCounts))
		for _, count := range hitCounts {
			// Each target should get approximately 1/3 of the requests
			// Allow for some variance in the distribution
			assert.InDelta(t, totalRequests/3, count, float64(totalRequests)/5)
		}
	})
	
	t.Run("drain all targets", func(t *testing.T) {
		// We need to manually set targets to draining state to test this properly
		// Since the Drain method temporarily sets the state and then defers restoring it
		for _, target := range pool.GetTargets() {
			target.updateState(TargetStateDraining)
		}
		
		// Verify we can't make a request when all targets are draining
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		_, _, err := pool.StartRequest(req)
		
		// We should get an error indicating no healthy targets
		require.Error(t, err, "Pool should reject requests when all targets are draining")
		assert.Equal(t, ErrorNoHealthyTargets.Error(), err.Error())
		
		// Restore targets to healthy state for any subsequent tests
		for _, target := range pool.GetTargets() {
			target.updateState(TargetStateHealthy)
		}
	})
}

func TestServiceWithMultipleTargets(t *testing.T) {
	// No longer need random source as we're using a fixed test value
	router := testRouter(t)
	
	// Create three backends with different responses
	_, first := testBackend(t, "first", http.StatusOK)
	_, second := testBackend(t, "second", http.StatusOK)
	_, third := testBackend(t, "third", http.StatusOK)
	
	// Set up the service with multiple targets
	require.NoError(t, router.SetServiceTargets(
		"multi-target-service", 
		[]string{"multi.example.com"}, 
		defaultPaths, 
		[]string{first, second, third}, 
		defaultServiceOptions, 
		defaultTargetOptions, 
		DefaultDeployTimeout, 
		DefaultDrainTimeout,
	))
	
	// Verify the service was created successfully
	service := router.serviceForName("multi-target-service")
	require.NotNil(t, service)
	
	// Check that all three targets were registered
	activeTargets := service.ActiveTargets()
	assert.Equal(t, 3, len(activeTargets))
	
	// Keep track of responses to verify round-robin distribution
	responses := make(map[string]int)
	
	// Send multiple requests to verify all targets are being used
	for i := 0; i < 9; i++ {
		statusCode, body := sendGETRequest(router, "http://multi.example.com/")
		assert.Equal(t, http.StatusOK, statusCode)
		responses[body]++
	}
	
	// Verify all three targets were used
	assert.Equal(t, 3, len(responses))
	assert.Equal(t, 3, responses["first"])
	assert.Equal(t, 3, responses["second"])
	assert.Equal(t, 3, responses["third"])
	
	// Test updating the service with new targets
	t.Run("update service targets", func(t *testing.T) {
		// Create two new backends
		_, fourth := testBackend(t, "fourth", http.StatusOK)
		_, fifth := testBackend(t, "fifth", http.StatusOK)
		
		// Update the service with new targets
		require.NoError(t, router.SetServiceTargets(
			"multi-target-service", 
			[]string{"multi.example.com"}, 
			defaultPaths, 
			[]string{fourth, fifth}, 
			defaultServiceOptions, 
			defaultTargetOptions, 
			DefaultDeployTimeout, 
			DefaultDrainTimeout,
		))
		
		// Check that the targets were updated
		activeTargets = service.ActiveTargets()
		assert.Equal(t, 2, len(activeTargets))
		
		// Reset responses tracking
		responses = make(map[string]int)
		
		// Send multiple requests to verify new targets are being used
		for i := 0; i < 10; i++ {
			statusCode, body := sendGETRequest(router, "http://multi.example.com/")
			assert.Equal(t, http.StatusOK, statusCode)
			responses[body]++
		}
		
		// Verify only the new targets were used
		assert.Equal(t, 2, len(responses))
		assert.Equal(t, 0, responses["first"])
		assert.Equal(t, 0, responses["second"])
		assert.Equal(t, 0, responses["third"])
		assert.Greater(t, responses["fourth"], 0)
		assert.Greater(t, responses["fifth"], 0)
	})
	
	// Test rollout with multiple targets
	t.Run("rollout with multiple targets", func(t *testing.T) {
		// Create fresh active targets to ensure they remain available during this test
		_, freshFirst := testBackend(t, "first", http.StatusOK)
		_, freshSecond := testBackend(t, "second", http.StatusOK)
		_, freshThird := testBackend(t, "third", http.StatusOK)
		
		// Update service with the fresh targets
		require.NoError(t, router.SetServiceTargets(
			"multi-target-service", 
			[]string{"multi.example.com"}, 
			defaultPaths, 
			[]string{freshFirst, freshSecond, freshThird}, 
			defaultServiceOptions, 
			defaultTargetOptions, 
			DefaultDeployTimeout, 
			DefaultDrainTimeout,
		))
		
		// Create two rollout targets
		_, rollout1 := testBackend(t, "rollout1", http.StatusOK)
		_, rollout2 := testBackend(t, "rollout2", http.StatusOK)
		
		// Set rollout targets
		require.NoError(t, router.SetRolloutTargets(
			"multi-target-service",
			[]string{rollout1, rollout2},
			DefaultDeployTimeout,
			DefaultDrainTimeout,
		))
		
		// Check that rollout targets were registered
		rolloutTargets := service.RolloutTargets()
		assert.Equal(t, 2, len(rolloutTargets))
		
		// Use a consistent cookie value for testing
		testCookieValue := "test-rollout-value"
		
		// Set rollout to 50% with our test cookie value in the allowlist
		require.NoError(t, router.SetRolloutSplit("multi-target-service", 50, []string{testCookieValue}))
		
		// Track responses to verify distribution between active and rollout pools
		activeResponses := make(map[string]int)
		rolloutResponses := make(map[string]int)
		
		// Send many requests to verify distribution
		// Half with cookie to go to rollout pool, half without to go to active pool
		for i := 0; i < 100; i++ {
			url := "http://multi.example.com/"
			var statusCode int
			var body string
			
			if i % 2 == 0 {
				// Create a request with cookie to test rollout targets
				req := httptest.NewRequest(http.MethodGet, url, nil)
				cookie := &http.Cookie{
					Name: RolloutCookieName,
					Value: testCookieValue,
				}
				req.AddCookie(cookie)
				log.Printf("DEBUG: Added rollout cookie %s=%s to request %d", RolloutCookieName, testCookieValue, i)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				result := w.Result()
				defer result.Body.Close()
				statusCode = result.StatusCode
				bodyBytes, _ := io.ReadAll(result.Body)
				body = string(bodyBytes)
				log.Printf("DEBUG: Request %d with cookie got response: %s", i, body)
			} else {
				statusCode, body = sendGETRequest(router, url)
				log.Printf("DEBUG: Request %d without cookie got response: %s", i, body)
			}
			assert.Equal(t, http.StatusOK, statusCode)
			
			if body == "first" || body == "second" || body == "third" {
				activeResponses[body]++
			} else if body == "rollout1" || body == "rollout2" {
				rolloutResponses[body]++
			}
		}
		
		// Verify both active and rollout targets were used
		// with approximately 50% distribution
		activeTotalRequests := activeResponses["first"] + activeResponses["second"] + activeResponses["third"]
		rolloutTotalRequests := rolloutResponses["rollout1"] + rolloutResponses["rollout2"]
		
		// Output the actual distribution for debugging
		fmt.Printf("DEBUG: Response distribution: Active=%d (first=%d, second=%d, third=%d), Rollout=%d (rollout1=%d, rollout2=%d)\n",
			activeTotalRequests, activeResponses["first"], activeResponses["second"], activeResponses["third"],
			rolloutTotalRequests, rolloutResponses["rollout1"], rolloutResponses["rollout2"])
		
		// Allow for some variance in the distribution
		assert.InDelta(t, 50, activeTotalRequests, 20)
		assert.InDelta(t, 50, rolloutTotalRequests, 20)
		
		// Verify all targets in each pool received requests
		assert.Greater(t, activeResponses["first"], 0)
		assert.Greater(t, activeResponses["second"], 0)
		assert.Greater(t, activeResponses["third"], 0)
		assert.Greater(t, rolloutResponses["rollout1"], 0)
		assert.Greater(t, rolloutResponses["rollout2"], 0)
	})
}
