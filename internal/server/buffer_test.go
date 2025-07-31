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

func TestRewindableReadCloser_BasicRewind(t *testing.T) {
	content := "Hello, World!"
	r := io.NopCloser(strings.NewReader(content))
	rrc, err := NewRewindableReadCloser(r, 0, 0)
	require.NoError(t, err)

	// First read
	result1, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, content, string(result1))

	// Read without rewinding
	result1, err = io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, "", string(result1))

	// Rewind and read again
	err = rrc.Rewind()
	require.NoError(t, err)

	result2, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, content, string(result2))

	// Rewind and read again (third time)
	err = rrc.Rewind()
	require.NoError(t, err)

	result3, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, content, string(result3))

	require.NoError(t, rrc.Close())
}

func TestRewindableReadCloser_MaxBytesRespected(t *testing.T) {
	content := "Hello, World!"
	maxBytes := int64(5) // Smaller than content length
	r := io.NopCloser(strings.NewReader(content))
	rrc, err := NewRewindableReadCloser(r, maxBytes, 0)
	require.NoError(t, err)

	// Read one byte at a time to ensure we hit the limit
	buf := make([]byte, 1)
	totalRead := 0
	maxBytesExceeded := false

	for totalRead < len(content) {
		n, err := rrc.Read(buf)
		totalRead += n
		if err == io.EOF {
			break
		}
		if err == ErrMaximumSizeExceeded {
			maxBytesExceeded = true
			break
		}
		if err != nil {
			require.NoError(t, err)
		}
	}

	// Should have hit the maxBytes limit
	assert.True(t, maxBytesExceeded, "Expected ErrMaximumSizeExceeded")
	assert.Equal(t, maxBytes, int64(totalRead), "Should have read exactly maxBytes")
	require.NoError(t, rrc.Close())
}

func TestRewindableReadCloser_MaxMemBytesRespected(t *testing.T) {
	// Create content larger than memory limit to force disk spill
	content := strings.Repeat("A", 100)
	maxMemBytes := int64(10) // Much smaller than content
	r := io.NopCloser(strings.NewReader(content))
	rrc, err := NewRewindableReadCloser(r, 0, maxMemBytes)
	require.NoError(t, err)

	// First read - should work and spill to disk
	result1, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, content, string(result1))

	// Rewind and read again - should read from memory + disk
	err = rrc.Rewind()
	require.NoError(t, err)

	result2, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, content, string(result2))

	require.NoError(t, rrc.Close())
}

func TestRewindableReadCloser_EmptyReader(t *testing.T) {
	r := io.NopCloser(strings.NewReader(""))
	rrc, err := NewRewindableReadCloser(r, 0, 0)
	require.NoError(t, err)

	// First read
	result1, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Empty(t, result1)

	// Rewind and read again
	err = rrc.Rewind()
	require.NoError(t, err)

	result2, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Empty(t, result2)

	require.NoError(t, rrc.Close())
}

func TestRewindableReadCloser_RewindBeforeFirstRead(t *testing.T) {
	content := "Hello, World!"
	r := io.NopCloser(strings.NewReader(content))
	rrc, err := NewRewindableReadCloser(r, 0, 0)
	require.NoError(t, err)

	// Rewind before any read is not allowed
	err = rrc.Rewind()
	require.ErrorIs(t, err, ErrNotYetFullyRead)

	// Now read normally
	result, err := io.ReadAll(rrc)
	require.NoError(t, err)
	assert.Equal(t, content, string(result))

	require.NoError(t, rrc.Close())
}
