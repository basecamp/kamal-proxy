package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeMethod(t *testing.T) {
	assert.Equal(t, "GET", normalizeMethod("GET"))
	assert.Equal(t, "POST", normalizeMethod("POST"))
	assert.Equal(t, "PATCH", normalizeMethod("PATCH"))

	assert.Equal(t, "OTHER", normalizeMethod("CUSTOM"))
	assert.Equal(t, "OTHER", normalizeMethod("OTHER"))
	assert.Equal(t, "OTHER", normalizeMethod(""))
}
