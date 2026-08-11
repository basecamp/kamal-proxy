package server

import (
	"hash/fnv"
	"net/http"
	"slices"
)

const RolloutCookieName = "kamal-rollout"

type RolloutController struct {
	Percentage           int      `json:"percentage"`
	PercentageSplitPoint float64  `json:"percentage_split_point"`
	Allowlist            []string `json:"allowlist"`

	// Stored inverted so that state written before this existed, which has no such
	// field, restores as enabled rather than silently disabling a live rollout.
	Disabled bool `json:"disabled,omitempty"`
}

func NewRolloutController(percentage int, allowlist []string) *RolloutController {
	maxHashValue := float64(uint32(0xFFFFFFFF))
	percentageSplitPoint := maxHashValue * (float64(percentage) / 100.0)

	return &RolloutController{
		Percentage:           percentage,
		PercentageSplitPoint: percentageSplitPoint,
		Allowlist:            allowlist,
	}
}

func (rc *RolloutController) Enabled() bool {
	return !rc.Disabled
}

func (rc *RolloutController) RequestUsesRolloutGroup(r *http.Request) bool {
	if rc.Disabled {
		return false
	}

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
	return slices.Contains(rc.Allowlist, value)
}

func (rc *RolloutController) valueInRolloutPercentage(value string) bool {
	// Zero has to mean zero, so that setting it is a reliable way to close the
	// split without tearing the rollout down. A value hashing to 0 would
	// otherwise still match, since the split point is 0 too.
	if rc.Percentage <= 0 {
		return false
	}

	hash := rc.hashForValue(value)
	return float64(hash) <= rc.PercentageSplitPoint
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
