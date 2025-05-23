package server

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostWriterMap_SetAndGet(t *testing.T) {
	t.Run("using domain names", func(t *testing.T) {
		hwm := NewHostWriterMap(0)

		hwm.Set(testURL(t, "https://one.example.com/users/100/profile"), "writer1")
		hwm.Set(testURL(t, "https://two.example.com/users/200/profile"), "writer2")

		assert.Equal(t, "writer1", hwm.Get(testURL(t, "https://one.example.com/users/100/profile")))
		assert.Equal(t, "writer1", hwm.Get(testURL(t, "https://one.example.com/users/200/profile")))
		assert.Equal(t, "writer2", hwm.Get(testURL(t, "https://two.example.com/users/200/profile")))

		assert.Empty(t, hwm.Get(testURL(t, "https://three.example.com/users/200/profile")))
	})

	t.Run("with significant path segments", func(t *testing.T) {
		hwm := NewHostWriterMap(2)

		hwm.Set(testURL(t, "https://example.com/users/100/profile"), "writer1")
		hwm.Set(testURL(t, "https://example.com/users/200/profile"), "writer2")

		assert.Equal(t, "writer1", hwm.Get(testURL(t, "https://example.com/users/100/profile/edit")))
		assert.Equal(t, "writer1", hwm.Get(testURL(t, "https://example.com/users/100/profile")))
		assert.Equal(t, "writer1", hwm.Get(testURL(t, "https://example.com/users/100")))
		assert.Equal(t, "writer2", hwm.Get(testURL(t, "https://example.com/users/200/profile/edit")))
		assert.Equal(t, "writer2", hwm.Get(testURL(t, "https://example.com/users/200/profile")))
		assert.Equal(t, "writer2", hwm.Get(testURL(t, "https://example.com/users/200")))

		assert.Empty(t, hwm.Get(testURL(t, "https://example.com/users/245/profile")))
		assert.Empty(t, hwm.Get(testURL(t, "https://example.com/users/")))
	})
}

// Helpers

func testURL(t *testing.T, u string) *url.URL {
	t.Helper()

	parsedURL, err := url.Parse(u)
	require.NoError(t, err)
	return parsedURL
}
