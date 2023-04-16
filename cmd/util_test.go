package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHostURLs_Empty(t *testing.T) {
	urls, err := parseHostURLs([]string{})
	require.NoError(t, err)
	require.Empty(t, urls)
}

func TestParseHostURLs_Valid(t *testing.T) {
	urls, err := parseHostURLs([]string{"localhost", "host1:3000", "host2.local:3000", "127.0.0.1:8080"})
	require.NoError(t, err)
	require.Equal(t, 4, len(urls))
	assert.Equal(t, "localhost", urls[0].Host)
	assert.Equal(t, "host1:3000", urls[1].Host)
	assert.Equal(t, "host2.local:3000", urls[2].Host)
	assert.Equal(t, "127.0.0.1:8080", urls[3].Host)
}

func TestParseHostURLs_NotValid(t *testing.T) {
	problemStrings := []string{
		"_",
		":123",
		"!ok*",
	}

	for _, problemString := range problemStrings {
		_, err := parseHostURLs([]string{problemString})
		require.Error(t, err, "should not be able to parse "+problemString)
	}
}
