package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRolloutController_MatchesAllowlistItems(t *testing.T) {
	t.Parallel()

	rc := NewRolloutController(0, []string{"1", "2"})

	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=1"}}}))
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=2"}}}))

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=3"}}}))
	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}

func TestRolloutController_PercentageSplit(t *testing.T) {
	t.Parallel()

	rc := NewRolloutController(60, []string{})

	usedRolloutGroup := 0
	for i := 0; i < 1000; i++ {
		req := &http.Request{Header: http.Header{"Cookie": []string{fmt.Sprintf("kamal-rollout=%05d", i)}}}
		if rc.RequestUsesRolloutGroup(req) {
			usedRolloutGroup++
		}
	}

	assert.InDelta(t, 600, usedRolloutGroup, 20)

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}

func TestRolloutController_AllowListAndPercentageTogether(t *testing.T) {
	t.Parallel()

	rc := NewRolloutController(10, []string{"00001", "00002"})

	usedRolloutGroup := 0
	for i := 0; i < 1000; i++ {
		req := &http.Request{Header: http.Header{"Cookie": []string{fmt.Sprintf("kamal-rollout=%05d", i)}}}
		if rc.RequestUsesRolloutGroup(req) {
			usedRolloutGroup++
		}
	}

	assert.InDelta(t, 100, usedRolloutGroup, 20)

	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00001"}}}))
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00002"}}}))

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}
