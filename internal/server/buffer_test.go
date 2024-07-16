package server

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferReadCloser_WithinMemoryLimits(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	brc, err := NewBufferReadCloser(r, 2048, 1024)

	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", brc.memoryBuffer.String())

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(result))
}

func TestBufferReadCloser_ExceedsMemoryLimits(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	brc, err := NewBufferReadCloser(r, 1024, 5)

	require.NoError(t, err)
	assert.Equal(t, "Hello", brc.memoryBuffer.String())

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(result))
}

func TestBufferReadCloser_ExceedsMemoryAndDiskLimits(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	_, err := NewBufferReadCloser(r, 8, 5)

	require.Equal(t, ErrMaximumSizeExceeded, err)
}

func TestBufferReadCloser_EmptyReader(t *testing.T) {
	r := io.NopCloser(strings.NewReader(""))
	brc, err := NewBufferReadCloser(r, 2048, 1024)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Empty(t, result)
}
