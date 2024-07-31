package server

import (
	"hash/fnv"
	"net/http"
	"slices"
)

const RolloutCookieName = "kamal-rollout"

type RolloutController struct {
	percentage           int
	percentageSplitPoint float64
	allowlist            []string
}

func NewRolloutController(cookie string, percentage int, allowlist []string) *RolloutController {
	maxHashValue := float64(uint32(0xFFFFFFFF))
	percentageSplitPoint := maxHashValue * (float64(percentage) / 100.0)

	return &RolloutController{
		percentage:           percentage,
		percentageSplitPoint: percentageSplitPoint,
		allowlist:            allowlist,
	}
}

func (rc *RolloutController) RequestUsesRolloutGroup(r *http.Request) bool {
	splitValue := rc.splitValue(r)
	if splitValue == "" {
		return false
	}

	if rc.valueInAllowlist(splitValue) {
		return true
	}

	return rc.valueInRolloutPercentage(splitValue)
}

func (rc *RolloutController) valueInAllowlist(value string) bool {
	return slices.Contains(rc.allowlist, value)
}

func (rc *RolloutController) valueInRolloutPercentage(value string) bool {
	hash := rc.hashForValue(value)
	return float64(hash) <= rc.percentageSplitPoint
}

func (rc *RolloutController) hashForValue(value string) uint32 {
	hasher := fnv.New32a()
	hasher.Write([]byte(value))
	return hasher.Sum32()
}

func (rc *RolloutController) splitValue(r *http.Request) string {
	cookie, err := r.Cookie(RolloutCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
