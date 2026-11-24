package main

// Requirement test for the served request-body cap (REQ-PG-054,
// docs/push-guard-spec.md); see docs/push-guard-testspec.md.

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/pushguard"
)

// endlessBody streams 'x' until the reader stops reading.
type endlessBody struct{}

func (endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}
func (endlessBody) Close() error { return nil }

// TestREQ_PG_054_ServedBodyOverCap drives the served handler the way a chunked push
// does (ContentLength < 0). A body that never ends is refused with 413 once it
// passes the cap, and no spool file is left behind.
func TestREQ_PG_054_ServedBodyOverCap(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	e := setup(t, "")
	srv, err := pushguard.NewServer(e.config, binary)
	if err != nil {
		t.Fatal(err)
	}
	srv.Log = log.New(io.Discard, "", 0)
	pattern := filepath.Join(os.TempDir(), "push-guard-pack-*")
	before, _ := filepath.Glob(pattern)

	req := httptest.NewRequest(http.MethodPost, "/"+repoName+".git/git-receive-pack", strings.NewReader(""))
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Body = endlessBody{}
	req.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	req.SetBasicAuth("agent", "synthetic-test-token")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body %q; want 413", rr.Code, rr.Body.String())
	}
	after, _ := filepath.Glob(pattern)
	if len(after) > len(before) {
		t.Errorf("spool file left behind: %v", after)
	}
	if !strings.Contains(rr.Body.String(), http.StatusText(http.StatusRequestEntityTooLarge)) {
		t.Errorf("body %q does not name the refusal", rr.Body.String())
	}
}
