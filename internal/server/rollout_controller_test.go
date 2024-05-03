package server

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRolloutController_MatchesAllowlistItems(t *testing.T) {
	rc := NewRolloutController("identity", 0, []string{"1", "2"})

	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"identity=1"}}}))
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"identity=2"}}}))

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"identity=3"}}}))
	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}

func TestRolloutController_PercentageSplit(t *testing.T) {
	rc := NewRolloutController("identity", 60, []string{})

	usedRolloutGroup := 0
	for i := 0; i < 1000; i++ {
		req := &http.Request{Header: http.Header{"Cookie": []string{fmt.Sprintf("identity=%05d", i)}}}
		if rc.RequestUsesRolloutGroup(req) {
			usedRolloutGroup++
		}
	}

	assert.InDelta(t, 600, usedRolloutGroup, 20)

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}

func TestRolloutController_AllowListAndPercentageTogether(t *testing.T) {
	rc := NewRolloutController("identity", 10, []string{"00001", "00002"})

	usedRolloutGroup := 0
	for i := 0; i < 1000; i++ {
		req := &http.Request{Header: http.Header{"Cookie": []string{fmt.Sprintf("identity=%05d", i)}}}
		if rc.RequestUsesRolloutGroup(req) {
			usedRolloutGroup++
		}
	}

	assert.InDelta(t, 100, usedRolloutGroup, 20)

	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"identity=00001"}}}))
	assert.True(t, rc.RequestUsesRolloutGroup(&http.Request{Header: http.Header{"Cookie": []string{"identity=00002"}}}))

	assert.False(t, rc.RequestUsesRolloutGroup(&http.Request{}))
}
