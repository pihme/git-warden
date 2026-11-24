package pushguard

// Requirement test for the Push Guard request-body cap (REQ-PG-054,
// docs/push-guard-spec.md); see docs/push-guard-testspec.md.

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// unsizedRequest is a request whose body hides its length, like a chunked Git
// push. The body keeps its own Close, as a real request body does.
func unsizedRequest(body io.ReadCloser) *http.Request {
	return &http.Request{ContentLength: -1, Body: body, TransferEncoding: []string{"chunked"}}
}

// bytesBody is exactly n bytes of 'x'.
func bytesBody(n int64) io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Repeat("x", int(n))))
}

// REQ-PG-054: an unsized (chunked) request body is spooled up to the cap and
// one byte more is refused.
func TestREQ_PG_054_UnsizedBodyCapped(t *testing.T) {
	const limit = int64(64 << 10)

	for _, n := range []int64{0, 1, limit - 1, limit} {
		r := unsizedRequest(bytesBody(n))
		cleanup, err := spoolUnsizedBody(r, limit)
		if err != nil {
			cleanup()
			t.Fatalf("body of %d bytes: err = %v; want accepted", n, err)
		}
		assertSpooled(t, r, n)
		cleanup()
	}

	r := unsizedRequest(bytesBody(limit + 1))
	cleanup, err := spoolUnsizedBody(r, limit)
	defer cleanup()
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("body of %d bytes: err = %v; want 'too large'", limit+1, err)
	}
	if code := spoolStatus(err); code != http.StatusRequestEntityTooLarge {
		t.Errorf("body over the cap: status = %d; want %d", code, http.StatusRequestEntityTooLarge)
	}
}

// REQ-PG-054: the cap holds for a body that never ends, not only for one
// that is merely large.
func TestREQ_PG_054_EndlessBodyRefused(t *testing.T) {
	const limit = int64(8 << 10)
	r := unsizedRequest(zeroReader{})
	cleanup, err := spoolUnsizedBody(r, limit)
	defer cleanup()
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("endless body: err = %v; want 'too large'", err)
	}
}

// REQ-PG-054: a body that stalls mid-stream does not hold the handler open.
// The read is bounded in time, not only in bytes.
func TestREQ_PG_054_StalledBodyTimesOut(t *testing.T) {
	const limit = int64(1 << 20)
	const within = 100 * time.Millisecond

	body := &stalledReader{closed: make(chan struct{})}
	r := unsizedRequest(body)

	type result struct {
		err error
		dur time.Duration
	}
	got := make(chan result, 1)
	go func() {
		start := time.Now()
		cleanup, err := spoolUnsizedBodyWithin(r, limit, within)
		cleanup()
		got <- result{err, time.Since(start)}
	}()

	select {
	case res := <-got:
		if res.err == nil || !strings.Contains(res.err.Error(), "timed out") {
			t.Fatalf("stalled body: err = %v; want a timeout", res.err)
		}
		if res.dur > 3*time.Second {
			t.Errorf("stalled body took %v; want about %v", res.dur, within)
		}
		if code := spoolStatus(res.err); code != http.StatusRequestTimeout {
			t.Errorf("stalled body: status = %d; want %d", code, http.StatusRequestTimeout)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled body held the handler open")
	}
}

// REQ-PG-054: a body that already carries a length is left untouched, so git
// http-backend still sees the client's length and the hook still runs.
func TestREQ_PG_054_KnownLengthUntouched(t *testing.T) {
	r := &http.Request{ContentLength: 12, Body: io.NopCloser(strings.NewReader("hello world!"))}
	cleanup, err := spoolUnsizedBody(r, 64<<10)
	defer cleanup()
	if err != nil || r.ContentLength != 12 {
		t.Fatalf("known-length body: ContentLength = %d, err = %v", r.ContentLength, err)
	}
}

func assertSpooled(t *testing.T, r *http.Request, n int64) {
	t.Helper()
	if r.ContentLength != n {
		t.Errorf("ContentLength = %d; want %d", r.ContentLength, n)
	}
	if len(r.TransferEncoding) != 0 {
		t.Errorf("TransferEncoding = %v; want none", r.TransferEncoding)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil || int64(len(got)) != n {
		t.Errorf("spooled %d bytes, err = %v; want %d", len(got), err, n)
	}
}

// zeroReader streams 'x' without end.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func (zeroReader) Close() error { return nil }

// stalledReader yields one byte and then blocks until it is closed, like a
// client that stops sending in the middle of a chunked push. Closing it
// releases the blocked Read, as a real request body does.
type stalledReader struct {
	sent   bool
	closed chan struct{}
	once   sync.Once
}

func (s *stalledReader) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true
		return copy(p, "x"), nil
	}
	<-s.closed
	return 0, io.ErrClosedPipe
}

func (s *stalledReader) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
