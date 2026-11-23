package main

// Requirement tests for REQ-PG-054 (docs/push-guard-spec.md): serve reads a
// Git request body only up to a fixed limit. The limit is lowered through
// Server.MaxBody so the bounds can be tested without 10 GiB bodies.

import (
	"bytes"
	"crypto/rand"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/pushguard"
	"github.com/pihme/git-warden/internal/testutil"
)

// countingBody counts the bytes served from r, so a test can see how far
// serve read.
type countingBody struct {
	r io.Reader
	n int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// endless never ends: the body of a stream that never stops.
type endless struct{}

func (endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'P'
	}
	return len(p), nil
}

// spoolFiles lists the temporary pack files serve left in dir.
func spoolFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "push-guard-pack-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// bodyServer is a serve handler with the body limit lowered to limit, its
// log captured and its temporary files in their own directory.
func bodyServer(t *testing.T, limit int64) (*pushguard.Server, *strings.Builder, string) {
	t.Helper()
	e := setup(t, "")
	srv, err := pushguard.NewServer(e.config, binary)
	if err != nil {
		t.Fatal(err)
	}
	srv.MaxBody = limit
	var logs strings.Builder
	srv.Log = log.New(&logs, "", 0)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	return srv, &logs, tmp
}

func receivePack(body io.Reader, length int64, user, pass string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/"+repoName+".git/git-receive-pack", body)
	req.ContentLength = length
	if length < 0 {
		req.TransferEncoding = []string{"chunked"}
	}
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	req.SetBasicAuth(user, pass)
	return req
}

func assertTooLarge(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusRequestEntityTooLarge || strings.TrimSpace(rr.Body.String()) != pushguard.MsgBodyTooLarge {
		t.Fatalf("status %d body %q, want 413 %q", rr.Code, rr.Body.String(), pushguard.MsgBodyTooLarge)
	}
}

func assertNotTooLarge(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code == http.StatusRequestEntityTooLarge || strings.Contains(rr.Body.String(), pushguard.MsgBodyTooLarge) {
		t.Fatalf("body at the limit refused: status %d body %q", rr.Code, rr.Body.String())
	}
}

// REQ-PG-054: an unsized (chunked) body of exactly the limit is spooled and
// handed on; one byte more is refused with 413, read at most one byte past
// the limit, and its temporary file is removed. An endless body ends the
// same way.
func TestREQ_PG_054_UnsizedBodyBounded(t *testing.T) {
	const limit = 64 << 10
	srv, logs, tmp := bodyServer(t, limit)
	const marker = "PACKBYTES-SHOULD-NOT-BE-LOGGED"
	pad := func(n int) []byte { return append([]byte(marker), bytes.Repeat([]byte{'P'}, n-len(marker))...) }

	at := &countingBody{r: bytes.NewReader(pad(limit))}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, receivePack(at, -1, "agent", "synthetic-test-token"))
	assertNotTooLarge(t, rr)
	if at.n != limit {
		t.Fatalf("read %d bytes of a body at the limit, want %d", at.n, limit)
	}

	for _, tc := range []struct {
		name string
		body io.Reader
	}{
		{"limit+1", bytes.NewReader(pad(limit + 1))},
		{"far over", bytes.NewReader(pad(8 * limit))},
		{"endless", io.MultiReader(strings.NewReader(marker), endless{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &countingBody{r: tc.body}
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, receivePack(c, -1, "agent", "synthetic-test-token"))
			assertTooLarge(t, rr)
			if c.n > limit+1+32<<10 {
				// MaxBytesReader stops after limit+1; allow one read buffer.
				t.Fatalf("read %d bytes, limit %d", c.n, limit)
			}
			if left := spoolFiles(t, tmp); len(left) != 0 {
				t.Fatalf("temporary pack left behind: %v", left)
			}
		})
	}
	if strings.Contains(logs.String(), marker) {
		t.Fatalf("pack bytes logged: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "request body over") {
		t.Fatalf("rejection not logged: %q", logs.String())
	}
	if left := spoolFiles(t, tmp); len(left) != 0 {
		t.Fatalf("temporary pack left behind: %v", left)
	}
}

// REQ-PG-054: a body that declares a length over the limit is refused with
// 413 before a byte of it is read, and after authentication: without the
// password the answer stays 401. A declared length of exactly the limit is
// handed on.
func TestREQ_PG_054_SizedBodyOverLimit(t *testing.T) {
	const limit = 64 << 10
	srv, _, tmp := bodyServer(t, limit)

	over := &countingBody{r: endless{}}
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, receivePack(over, limit+1, "agent", "synthetic-test-token"))
	assertTooLarge(t, rr)
	if over.n != 0 {
		t.Fatalf("read %d bytes of a body declared over the limit", over.n)
	}
	if rr.Header().Get("Connection") != "close" {
		t.Fatalf("connection kept open after 413: %q", rr.Header().Get("Connection"))
	}

	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, receivePack(&countingBody{r: endless{}}, limit+1, "agent", "wrong"))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated oversized body: status %d, want 401", rr.Code)
	}

	at := &countingBody{r: bytes.NewReader(bytes.Repeat([]byte{'P'}, limit))}
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, receivePack(at, limit, "agent", "synthetic-test-token"))
	assertNotTooLarge(t, rr)
	if left := spoolFiles(t, tmp); len(left) != 0 {
		t.Fatalf("sized body spooled: %v", left)
	}
}

// REQ-PG-054: a real chunked Git push whose pack is over the limit fails
// over HTTP with 413, nothing reaches the remote or the journal, and no
// temporary pack stays on disk. The same push goes through with the
// limit above the pack.
func TestREQ_PG_054_ChunkedPushOverLimit(t *testing.T) {
	e := setup(t, "CONTENT-BINARY: {allow: ['blob\\.bin']}\nSIZE-LARGE: {max_file_bytes: 10000000}")
	srv, err := pushguard.NewServer(e.config, binary)
	if err != nil {
		t.Fatal(err)
	}
	srv.MaxBody = 2 << 20
	srv.Log = log.New(io.Discard, "", 0)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := strings.Replace(hs.URL, "http://", "http://agent:synthetic-test-token@", 1) + "/" + repoName + ".git"
	clone := testutil.Open(t, filepath.Join(e.base, "http-agent"))
	testutil.Run(t, "", "clone", "--quiet", url, clone.Dir)
	payload := make([]byte, 3<<20) // random, so the pack stays over 2 MiB
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	head := clone.Commit("too big", map[string]string{"blob.bin": string(payload)})
	before := len(e.entries())
	out, err := clone.Try("-c", "http.postBuffer=1048576", "push", "origin", "HEAD:refs/heads/big")
	if err == nil {
		t.Fatalf("push over the limit accepted:\n%s", out)
	}
	if !strings.Contains(err.Error(), "HTTP 413") {
		t.Fatalf("push over the limit: want HTTP 413 in %q (%v)", out, err)
	}
	if got := e.remoteRef("refs/heads/big"); got != "" {
		t.Fatalf("push over the limit forwarded: %s", got)
	}
	if after := len(e.entries()); after != before {
		t.Fatalf("journal grew from %d to %d entries", before, after)
	}
	if left := spoolFiles(t, tmp); len(left) != 0 {
		t.Fatalf("temporary pack left behind: %v", left)
	}

	srv.MaxBody = 8 << 20
	if out, err := clone.Try("-c", "http.postBuffer=1048576", "push", "origin", "HEAD:refs/heads/big"); err != nil {
		t.Fatalf("push under the raised limit: %v\n%s", err, out)
	}
	if e.remoteRef("refs/heads/big") != head {
		t.Fatal("push under the raised limit not forwarded")
	}
}
