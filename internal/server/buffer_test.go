package server

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferedReadCloser_WithinMemoryLimits(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	brc, err := NewBufferedReadCloser(r, 2048, 1024)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(result))
}

func TestBufferedReadCloser_ExceedsMemoryLimits(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	brc, err := NewBufferedReadCloser(r, 1024, 5)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(result))
}

func TestBufferedReadCloser_ExceedsMemoryLimitWhenDiskIsUnlimited(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	brc, err := NewBufferedReadCloser(r, 0, 5)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(result))
}

func TestBufferedReadCloser_Unlimited(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	brc, err := NewBufferedReadCloser(r, 0, 0)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(result))
}

func TestBufferedReadCloser_ExceedsMemoryAndDiskLimits(t *testing.T) {
	r := io.NopCloser(strings.NewReader("Hello, World!"))
	_, err := NewBufferedReadCloser(r, 8, 5)

	require.Equal(t, ErrMaximumSizeExceeded, err)
}

func TestBufferedReadCloser_EmptyReader(t *testing.T) {
	r := io.NopCloser(strings.NewReader(""))
	brc, err := NewBufferedReadCloser(r, 2048, 1024)

	require.NoError(t, err)

	result, err := io.ReadAll(brc)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestBufferedWriteCloser_WithinMemoryLimits(t *testing.T) {
	bwc := NewBufferedWriteCloser(2048, 1024)
	_, err := bwc.Write([]byte("Hello, World!"))
	require.NoError(t, err)

	var result strings.Builder
	require.NoError(t, bwc.Send(&result))

	assert.Equal(t, "Hello, World!", result.String())
}

func TestBufferedWriteCloser_ExceedsMemoryLimits(t *testing.T) {
	bwc := NewBufferedWriteCloser(2048, 2)
	_, err := bwc.Write([]byte("Hello, World!"))
	require.NoError(t, err)

	var result strings.Builder
	require.NoError(t, bwc.Send(&result))

	assert.Equal(t, "Hello, World!", result.String())
}

func TestBufferedWriteCloser_ExceedsMemoryLimitWhenDiskIsUnlimited(t *testing.T) {
	bwc := NewBufferedWriteCloser(0, 2)
	_, err := bwc.Write([]byte("Hello, World!"))
	require.NoError(t, err)

	var result strings.Builder
	require.NoError(t, bwc.Send(&result))

	assert.Equal(t, "Hello, World!", result.String())
}

func TestBufferedWriteCloser_Unlimited(t *testing.T) {
	bwc := NewBufferedWriteCloser(0, 0)
	_, err := bwc.Write([]byte("Hello, World!"))
	require.NoError(t, err)

	var result strings.Builder
	require.NoError(t, bwc.Send(&result))

	assert.Equal(t, "Hello, World!", result.String())
}

func TestBufferedWriteCloser_ExceedsMemoryAndDiskLimits(t *testing.T) {
	bwc := NewBufferedWriteCloser(8, 5)
	_, err := bwc.Write([]byte("Hello, World!"))
	require.Equal(t, ErrMaximumSizeExceeded, err)
}

func TestBufferedWriteCloser_EmptyWriter(t *testing.T) {
	bwc := NewBufferedWriteCloser(2048, 1024)

	var result strings.Builder
	require.NoError(t, bwc.Send(&result))

	assert.Empty(t, result.String())
}
