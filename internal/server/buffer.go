package server

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
)

var (
	ErrMaximumSizeExceeded = errors.New("maximum size exceeded")
)

type BufferReadCloser struct {
	maxBytes    int64
	maxMemBytes int64

	memoryBuffer bytes.Buffer
	diskBuffer   *os.File
	multiReader  io.Reader
}

func NewBufferReadCloser(r io.ReadCloser, maxBytes, maxMemBytes int64) (*BufferReadCloser, error) {
	brc := &BufferReadCloser{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}

	err := brc.populate(r)

	return brc, err
}

func (b *BufferReadCloser) Read(p []byte) (n int, err error) {
	return b.multiReader.Read(p)
}

func (b *BufferReadCloser) Close() error {
	if b.diskBuffer != nil {
		b.diskBuffer.Close()
		os.Remove(b.diskBuffer.Name())
		slog.Debug("Buffer: removing spill", "file", b.diskBuffer.Name())
	}
	return nil
}

func (b *BufferReadCloser) populate(r io.ReadCloser) error {
	defer r.Close()

	moreDataRemaining, err := b.populateMemoryBuffer(r)
	if err != nil {
		return err
	}

	if !moreDataRemaining {
		b.multiReader = &b.memoryBuffer
		return nil
	}

	err = b.populateDiskBuffer(r)
	if err != nil {
		return err
	}

	b.multiReader = io.MultiReader(&b.memoryBuffer, b.diskBuffer)
	return nil
}

func (b *BufferReadCloser) populateMemoryBuffer(r io.ReadCloser) (bool, error) {
	limitReader := io.LimitReader(r, b.maxMemBytes)
	copied, err := b.memoryBuffer.ReadFrom(limitReader)
	if err != nil {
		return false, err
	}

	moreDataRemaining := copied == b.maxMemBytes
	return moreDataRemaining, nil
}

func (b *BufferReadCloser) populateDiskBuffer(r io.ReadCloser) error {
	var err error

	b.diskBuffer, err = os.CreateTemp("", "proxy-buffer")
	if err != nil {
		return err
	}

	slog.Debug("Buffer: spilling request to disk", "file", b.diskBuffer.Name())

	maxDiskBytes := b.maxBytes - b.maxMemBytes
	limitReader := io.LimitReader(r, maxDiskBytes)
	copied, err := io.Copy(b.diskBuffer, limitReader)
	if err != nil {
		return err
	}

	if copied == maxDiskBytes {
		b.Close()
		return ErrMaximumSizeExceeded
	}

	b.diskBuffer.Seek(0, 0)
	return err
}
