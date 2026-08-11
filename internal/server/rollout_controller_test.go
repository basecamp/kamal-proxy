package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRolloutController_MatchesAllowlistItems(t *testing.T) {
	rc := NewRolloutController(0, []string{"1", "2"})

	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=1"}}}))
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=2"}}}))

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=3"}}}))
	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}

func TestRolloutController_PercentageSplit(t *testing.T) {
	rc := NewRolloutController(60, []string{})

	usedRolloutGroup := 0
	for i := range 1000 {
		req := &http.Request{Header: http.Header{"Cookie": []string{fmt.Sprintf("kamal-rollout=%05d", i)}}}
		if rc.RequestUsesRolloutGroup(req) {
			usedRolloutGroup++
		}
	}

	assert.InDelta(t, 600, usedRolloutGroup, 20)

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}

func TestRolloutController_AllowListAndPercentageTogether(t *testing.T) {
	rc := NewRolloutController(10, []string{"00001", "00002"})

	usedRolloutGroup := 0
	for i := range 1000 {
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

func TestRolloutController_ZeroPercentageRoutesNothing(t *testing.T) {
	rc := NewRolloutController(0, []string{})

	// A value hashing to exactly 0 would match a split point of 0 without the guard
	assert.False(t, rc.valueInRolloutPercentage("anything"))

	for i := range 1000 {
		req := &http.Request{Header: http.Header{"Cookie": []string{fmt.Sprintf("kamal-rollout=%05d", i)}}}
		assert.False(t, rc.RequestUsesRolloutGroup(req))
	}
}

func TestRolloutController_ZeroPercentageStillHonoursTheAllowlist(t *testing.T) {
	rc := NewRolloutController(0, []string{"00001"})

	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00001"}}}))
	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00002"}}}))
}

func TestRolloutController_DisabledRoutesNothing(t *testing.T) {
	rc := NewRolloutController(100, []string{"00001"})
	assert.True(t, rc.Enabled())
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00001"}}}))

	rc.Disabled = true

	assert.False(t, rc.Enabled())
	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00001"}}}))
	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00002"}}}))

	// Re-enabling restores the split it was already carrying
	rc.Disabled = false
	assert.Equal(t, 100, rc.Percentage)
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00002"}}}))
}

func TestRolloutController_StateWrittenBeforeDisabledExistedRestoresEnabled(t *testing.T) {
	var rc RolloutController
	require.NoError(t, json.Unmarshal([]byte(`{"percentage":100,"percentage_split_point":4294967295,"allowlist":[]}`), &rc))

	assert.True(t, rc.Enabled())
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"kamal-rollout=00001"}}}))
}
