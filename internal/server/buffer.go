package server

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
)

var (
	ErrMaximumSizeExceeded = errors.New("maximum size exceeded")
	ErrWriteAfterRead      = errors.New("write after read")
)

type Buffer struct {
	maxBytes    int64
	maxMemBytes int64

	memoryBuffer     bytes.Buffer
	memBytesWritten  int64
	diskBuffer       *os.File
	diskBytesWritten int64
	overflowed       bool
	reader           io.Reader
	closeOnce        sync.Once
}

func NewBufferedReadCloser(r io.ReadCloser, maxBytes, maxMemBytes int64) (io.ReadCloser, error) {
	buf := &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}

	_, err := io.Copy(buf, r)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func NewBufferedWriteCloser(maxBytes, maxMemBytes int64) *Buffer {
	return &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}
}

func (b *Buffer) Write(p []byte) (int, error) {
	if b.reader != nil {
		return 0, ErrWriteAfterRead
	}

	length := int64(len(p))
	totalWritten := b.memBytesWritten + b.diskBytesWritten

	if totalWritten+length > b.maxBytes {
		b.overflowed = true
		return 0, ErrMaximumSizeExceeded
	}

	if b.diskBuffer != nil {
		return b.writeToDisk(p)
	}

	if b.memBytesWritten+length <= b.maxMemBytes {
		return b.writeToMemory(p)
	}

	// We're writing past the memory buffer, so we need to start the spill to disk
	err := b.createSpill()
	if err != nil {
		return 0, err
	}

	memWritten, err := b.writeToMemory(p[:b.maxMemBytes-b.memBytesWritten])
	if err != nil {
		return memWritten, err
	}

	diskWritten, err := b.writeToDisk(p[memWritten:])
	return memWritten + diskWritten, err
}

func (b *Buffer) Read(p []byte) (n int, err error) {
	b.setReader()
	return b.reader.Read(p)
}

func (b *Buffer) Overflowed() bool {
	return b.overflowed
}

func (b *Buffer) Send(w io.Writer) error {
	b.setReader()
	_, err := io.Copy(w, b.reader)
	return err
}

func (b *Buffer) Close() error {
	b.closeOnce.Do(func() {
		b.discardSpill()
	})

	return nil
}

func (b *Buffer) writeToMemory(p []byte) (int, error) {
	n, err := b.memoryBuffer.Write(p)
	b.memBytesWritten += int64(n)
	return n, err
}

func (b *Buffer) writeToDisk(p []byte) (int, error) {
	n, err := b.diskBuffer.Write(p)
	b.diskBytesWritten += int64(n)
	return n, err
}

func (b *Buffer) setReader() {
	if b.reader == nil {
		if b.diskBuffer != nil {
			b.diskBuffer.Seek(0, 0)
			b.reader = io.MultiReader(&b.memoryBuffer, b.diskBuffer)
		} else {
			b.reader = &b.memoryBuffer
		}
	}
}

func (b *Buffer) createSpill() error {
	f, err := os.CreateTemp("", "proxy-buffer-")
	if err != nil {
		slog.Error("Buffer: failed to create spill file", "error", err)
		return err
	}

	b.diskBuffer = f
	slog.Debug("Buffer: spilling to disk", "file", b.diskBuffer.Name())

	return nil
}

func (b *Buffer) discardSpill() {
	if b.diskBuffer != nil {
		b.diskBuffer.Close()

		slog.Debug("Buffer: removing spill", "file", b.diskBuffer.Name())
		err := os.Remove(b.diskBuffer.Name())
		if err != nil {
			slog.Error("Buffer: failed to remove spill", "file", b.diskBuffer.Name(), "error", err)
		}
	}
}
