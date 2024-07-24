package server

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferedReadCloser(t *testing.T) {
	tests := map[string]struct {
		maxBytes       int64
		maxMemBytes    int64
		expectOverflow bool
	}{
		"unlimited":                      {0, 0, false},
		"unlimited disk":                 {0, 5, false},
		"within memory limits":           {2048, 1024, false},
		"exceeds memory limits":          {2048, 5, false},
		"exceeds memory and disk limits": {8, 5, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := io.NopCloser(strings.NewReader("Hello, World!"))
			brc, err := NewBufferedReadCloser(r, tc.maxBytes, tc.maxMemBytes)

			if tc.expectOverflow {
				require.Equal(t, ErrMaximumSizeExceeded, err)
			} else {
				require.NoError(t, err)

				result, err := io.ReadAll(brc)
				require.NoError(t, err)
				assert.Equal(t, "Hello, World!", string(result))
			}
		})
	}
}

func TestBufferedReadCloser_EmptyReader(t *testing.T) {
	r := io.NopCloser(strings.NewReader(""))
	brc, err := NewBufferedReadCloser(r, 2048, 1024)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestBufferedWriteCloser(t *testing.T) {
	tests := map[string]struct {
		maxBytes       int64
		maxMemBytes    int64
		expectOverflow bool
	}{
		"unlimited":                      {0, 0, false},
		"unlimited disk":                 {0, 5, false},
		"within memory limits":           {2048, 1024, false},
		"exceeds memory limits":          {2048, 5, false},
		"exceeds memory and disk limits": {8, 5, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			bwc := NewBufferedWriteCloser(tc.maxBytes, tc.maxMemBytes)
			_, err := bwc.Write([]byte("Hello, World!"))

			if tc.expectOverflow {
				require.Equal(t, ErrMaximumSizeExceeded, err)
			} else {
				require.NoError(t, err)

				var result strings.Builder
				require.NoError(t, bwc.Send(&result))

				assert.Equal(t, "Hello, World!", result.String())
			}
		})
	}
}

func TestBufferedWriteCloser_NothingWritten(t *testing.T) {
	bwc := NewBufferedWriteCloser(2048, 1024)

	var result strings.Builder
	require.NoError(t, bwc.Send(&result))

	assert.Empty(t, result.String())
}
