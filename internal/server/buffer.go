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
	ErrNotYetFullyRead     = errors.New("not yet fully read")
)

type Rewindable interface {
	Rewind() error
}

type RewindableReadCloser struct {
	original      io.ReadCloser
	buffer        *Buffer
	readCompleted bool
	isEOF         bool
}

func (r *RewindableReadCloser) Rewind() error {
	if !r.readCompleted {
		return ErrNotYetFullyRead
	}

	r.buffer.Rewind()
	r.isEOF = false
	return nil
}

func (r *RewindableReadCloser) Read(p []byte) (int, error) {
	if r.isEOF {
		return 0, io.EOF
	}

	if !r.readCompleted {
		// Check if buffer has already overflowed
		if r.buffer.Overflowed() {
			return 0, ErrMaximumSizeExceeded
		}

		// First read: read from original and populate buffer
		n, err := r.original.Read(p)
		if n > 0 {
			_, writeErr := r.buffer.Write(p[:n])
			if writeErr == ErrMaximumSizeExceeded {
				// Don't return the bytes that caused overflow
				return 0, writeErr
			}
		}
		if err == io.EOF {
			slog.Info("RewindableReadCloser: read completed", "bytes_read", n)
			r.readCompleted = true
			r.isEOF = true
			// r.buffer.Rewind()
		}
		return n, err
	}

	// Subsequent reads: read from buffer
	slog.Info("RewindableReadCloser: read subseqent")
	return r.buffer.Read(p)
}

func (r *RewindableReadCloser) Close() error {
	var err1, err2 error
	if r.original != nil {
		err1 = r.original.Close()
	}
	if r.buffer != nil {
		err2 = r.buffer.Close()
	}
	if err1 != nil {
		return err1
	}
	return err2
}

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
		buf.Close()
		return nil, err
	}

	return buf, nil
}

func NewRewindableReadCloser(r io.ReadCloser, maxBytes, maxMemBytes int64) (*RewindableReadCloser, error) {
	buf := &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}

	return &RewindableReadCloser{
		original:      r,
		buffer:        buf,
		readCompleted: false,
	}, nil
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

	if b.maxBytes > 0 && totalWritten+length > b.maxBytes {
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

func (b *Buffer) Rewind() {
	b.reader = nil
	b.setReader()
}

func (b *Buffer) Close() error {
	b.closeOnce.Do(func() {
		b.discardSpill()
	})

	return nil
}

// Private

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
