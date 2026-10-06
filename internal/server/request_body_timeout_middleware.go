package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrRequestBodyTimeout = errors.New("timed out waiting for request body")

	warnDeadlinesUnsupported sync.Once
)

const timedOutSettleDuration = 50 * time.Millisecond

// RequestBodyTimeoutMiddleware bounds the time a client may stall while
// sending a request body. Each read of the body is given a fresh deadline, so
// an upload that keeps making progress is never cut off, but a client that
// stops sending is.
//
// For HTTP/1 requests that carry a body, the deadline is also armed before the
// handler runs and left in place between reads. That bounds the body drain
// that net/http performs after a handler that never reads the body (such as a
// 404 or a redirect) returns. That drain bypasses this wrapper, so it runs
// under a fixed deadline rather than a per-read one: a client still sending
// its body after an early response loses the connection once the deadline
// passes, instead of having it kept alive.
//
// HTTP/2 and HTTP/3 reset the stream when the handler returns, and an expired
// stream deadline closes the body for good, so on those protocols the
// deadline exists only while a read is in progress.
type RequestBodyTimeoutMiddleware struct {
	timeout time.Duration
	next    http.Handler
}

func WithRequestBodyTimeoutMiddleware(timeout time.Duration, next http.Handler) http.Handler {
	if timeout <= 0 {
		return next
	}

	return &RequestBodyTimeoutMiddleware{
		timeout: timeout,
		next:    next,
	}
}

func (h *RequestBodyTimeoutMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength == 0 || r.Body == nil || r.Body == http.NoBody {
		h.next.ServeHTTP(w, r)
		return
	}

	body := &deadlineBody{
		ReadCloser:        r.Body,
		controller:        http.NewResponseController(w),
		timeout:           h.timeout,
		armedBetweenReads: r.ProtoMajor == 1,
	}
	if body.armedBetweenReads {
		body.arm()
	}

	r.Body = body
	h.next.ServeHTTP(w, r)
}

type deadlineBody struct {
	io.ReadCloser
	controller        *http.ResponseController
	timeout           time.Duration
	armedBetweenReads bool

	reading  atomic.Bool
	timedOut atomic.Bool
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	b.reading.Store(true)
	defer b.reading.Store(false)

	b.arm()
	n, err := b.ReadCloser.Read(p)

	if err == io.EOF || !b.armedBetweenReads {
		b.clear()
	}

	if err != nil && err != io.EOF && isDeadlineError(err) {
		b.timedOut.Store(true)
		err = fmt.Errorf("%w: %w", ErrRequestBodyTimeout, err)
	}

	return n, err
}

// TimedOut reports whether a body read has failed because the client stalled.
//
// On HTTP/1, net/http cancels the request context as soon as a body read
// fails, so a handler can observe the cancellation a moment before the read
// itself returns. If a read is in progress, wait briefly for it to finish so
// that its result is known.
func (b *deadlineBody) TimedOut() bool {
	deadline := time.Now().Add(timedOutSettleDuration)
	for b.reading.Load() && !b.timedOut.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	return b.timedOut.Load()
}

func (b *deadlineBody) arm() {
	err := b.controller.SetReadDeadline(time.Now().Add(b.timeout))
	if err != nil {
		warnDeadlinesUnsupported.Do(func() {
			slog.Warn("Request body timeout cannot be enforced", "error", err)
		})
	}
}

func (b *deadlineBody) clear() {
	_ = b.controller.SetReadDeadline(time.Time{})
}

func requestBodyTimedOut(r *http.Request) bool {
	body, ok := r.Body.(*deadlineBody)
	return ok && body.TimedOut()
}

func isDeadlineError(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}

	if netErr, ok := errors.AsType[net.Error](err); ok {
		return netErr.Timeout()
	}

	return false
}
