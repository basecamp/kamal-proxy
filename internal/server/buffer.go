package server

import (
	"bytes"
	"cmp"
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

type Rewindable interface {
	Rewind()
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

type RewindableReadCloser struct {
	rc                   io.ReadCloser
	buffer               *Buffer
	initialReadCompleted bool
	atEnd                bool
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

func NewBufferedWriteCloser(maxBytes, maxMemBytes int64) *Buffer {
	return &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}
}

func NewRewindableReadCloser(rc io.ReadCloser, maxBytes, maxMemBytes int64) *RewindableReadCloser {
	buffer := &Buffer{
		maxBytes:    maxBytes,
		maxMemBytes: maxMemBytes,
	}

	return &RewindableReadCloser{
		rc:     rc,
		buffer: buffer,
	}
}

func (r *RewindableReadCloser) Read(p []byte) (n int, err error) {
	if r.initialReadCompleted {
		if r.atEnd {
			return 0, io.EOF
		}
		return r.buffer.Read(p)
	} else {
		n, err := r.rc.Read(p)
		if n > 0 {
			_, writeErr := r.buffer.Write(p[:n])
			if writeErr != nil {
				return 0, writeErr
			}
		}
		if err == io.EOF {
			r.initialReadCompleted = true
			r.atEnd = true
		}

		return n, err
	}
}

func (r *RewindableReadCloser) Close() error {
	// Don't close underlying buffers, as we may still be rewound to read again
	return nil
}

func (r *RewindableReadCloser) Dispose() error {
	return cmp.Or(r.buffer.Close(), r.rc.Close())
}

func (r *RewindableReadCloser) Rewind() {
	r.atEnd = false
	r.buffer.Rewind()
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
		readers := []io.Reader{bytes.NewReader(b.memoryBuffer.Bytes())}
		if b.diskBuffer != nil {
			b.diskBuffer.Seek(0, 0)
			readers = append(readers, b.diskBuffer)
		}

		b.reader = io.MultiReader(readers...)
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
