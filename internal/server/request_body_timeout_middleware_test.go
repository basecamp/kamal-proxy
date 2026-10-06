package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func newDeadlineRecorder() *deadlineRecorder {
	return &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	r.deadlines = append(r.deadlines, deadline)
	return nil
}

func (r *deadlineRecorder) armedCount() int {
	count := 0
	for _, deadline := range r.deadlines {
		if !deadline.IsZero() {
			count++
		}
	}
	return count
}

func (r *deadlineRecorder) clearedCount() int {
	return len(r.deadlines) - r.armedCount()
}

type stallingReader struct {
	err error
}

func (r *stallingReader) Read(p []byte) (int, error) { return 0, r.err }
func (r *stallingReader) Close() error               { return nil }

func TestRequestBodyTimeoutMiddleware_HTTP1ArmsBeforeHandlerAndClearsOnEOF(t *testing.T) {
	recorder := newDeadlineRecorder()
	var deadlinesAtHandlerStart int

	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadlinesAtHandlerStart = len(recorder.deadlines)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(body))
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, 1, deadlinesAtHandlerStart)
	assert.False(t, recorder.deadlines[0].IsZero())
	assert.True(t, recorder.deadlines[len(recorder.deadlines)-1].IsZero())
	assert.Equal(t, 1, recorder.clearedCount())
}

func TestRequestBodyTimeoutMiddleware_HTTP1KeepsDeadlineAfterTimeout(t *testing.T) {
	recorder := newDeadlineRecorder()

	var readErr error
	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", &stallingReader{err: os.ErrDeadlineExceeded})
	req.ContentLength = 100
	handler.ServeHTTP(recorder, req)

	assert.True(t, errors.Is(readErr, ErrRequestBodyTimeout))
	assert.Equal(t, 0, recorder.clearedCount())
}

func TestRequestBodyTimeoutMiddleware_TimedOutWaitsForReadInProgress(t *testing.T) {
	release := make(chan struct{})
	reader := &blockingReader{release: release, err: os.ErrDeadlineExceeded, started: make(chan struct{})}

	var timedOut bool
	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		go func() {
			_, _ = r.Body.Read(make([]byte, 1))
		}()
		<-reader.started

		time.AfterFunc(5*time.Millisecond, func() { close(release) })
		timedOut = requestBodyTimedOut(r)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", reader)
	req.ContentLength = 100
	handler.ServeHTTP(newDeadlineRecorder(), req)

	assert.True(t, timedOut)
}

func TestRequestBodyTimeoutMiddleware_NotTimedOutForOtherErrors(t *testing.T) {
	var timedOut bool
	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		timedOut = requestBodyTimedOut(r)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", &stallingReader{err: errors.New("boom")})
	req.ContentLength = 100
	handler.ServeHTTP(newDeadlineRecorder(), req)

	assert.False(t, timedOut)
}

type blockingReader struct {
	release <-chan struct{}
	err     error
	started chan struct{}
	once    sync.Once
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, r.err
}

func (r *blockingReader) Close() error { return nil }

func TestRequestBodyTimeoutMiddleware_SkipsRequestsWithoutBody(t *testing.T) {
	recorder := newDeadlineRecorder()

	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, req)

	assert.Empty(t, recorder.deadlines)
}

func TestRequestBodyTimeoutMiddleware_HTTP2ArmsOnlyDuringReads(t *testing.T) {
	recorder := newDeadlineRecorder()
	var deadlinesAtHandlerStart int

	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadlinesAtHandlerStart = len(recorder.deadlines)

		buf := make([]byte, 2)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				break
			}
		}
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	req.ProtoMajor = 2
	req.ProtoMinor = 0
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, 0, deadlinesAtHandlerStart)
	assert.Equal(t, recorder.armedCount(), recorder.clearedCount())
	for i, deadline := range recorder.deadlines {
		assert.Equal(t, i%2 == 1, deadline.IsZero(), "deadline %d", i)
	}
}

func TestRequestBodyTimeoutMiddleware_ReachesWriterThroughLoggerResponseWriter(t *testing.T) {
	recorder := newDeadlineRecorder()

	handler := WithRequestBodyTimeoutMiddleware(time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	handler.ServeHTTP(newLoggerResponseWriter(recorder), req)

	assert.NotEmpty(t, recorder.deadlines)
}

func TestRequestBodyTimeoutMiddleware_DisabledWhenTimeoutIsZero(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler := WithRequestBodyTimeoutMiddleware(0, next)

	assert.IsType(t, next, handler)
}
