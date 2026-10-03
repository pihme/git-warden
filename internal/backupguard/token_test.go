package backupguard

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gitHTTPServer serves the bare repositories under root over Git smart HTTP
// (git http-backend) behind HTTP basic auth: only user:token gets in. It
// records what every request asked for, so a test can tell ls-remote
// (ls-refs) from a fetch.
type gitHTTPServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []gitRequest
}

type gitRequest struct {
	authorized bool
	user       string
	fetch      bool // an upload-pack request that asks for objects
	query      string
}

func newGitHTTPServer(t *testing.T, root, user, token string) *gitHTTPServer {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{
		Path: git,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	s := &gitHTTPServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		rec := gitRequest{user: u, query: r.URL.RawQuery, authorized: ok && u == user && p == token}
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			plain := body
			if r.Header.Get("Content-Encoding") == "gzip" {
				if zr, err := gzip.NewReader(bytes.NewReader(body)); err == nil {
					plain, _ = io.ReadAll(zr)
				}
			}
			rec.fetch = strings.HasSuffix(r.URL.Path, "/git-upload-pack") &&
				(bytes.Contains(plain, []byte("command=fetch")) || bytes.Contains(plain, []byte("want ")))
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		s.mu.Unlock()
		if !rec.authorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *gitHTTPServer) requests() []gitRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]gitRequest(nil), s.reqs...)
}

// TestTokenFileReachesEverrefFetch backs up an HTTP remote that requires a
// token. The Backup Guard itself only runs ls-remote; the objects come from
// git-everref's own fetch, so a backup can only succeed if the token file's
// credential reaches everref's git as well. The token never shows up in the
// remote URL, the bridge's configuration, the journal or everref's output.
// The server wants a user name other than the default, as a GitLab deploy
// token does, so credential_username is exercised too.
func TestTokenFileReachesEverrefFetch(t *testing.T) {
	realEverref(t)
	ctx := context.Background()
	const token = "synthetic-test-token-4711"
	bare, _ := remoteWith(t, "main", "feature")
	root := filepath.Dir(bare)
	srv := newGitHTTPServer(t, root, "gitlab+deploy-token-1", token)
	remote := srv.URL + "/" + filepath.Base(bare)

	dir := writeConfig(t, "", map[string]string{"r": "remote: " + remote + "\ncredential: token\ncredential_username: gitlab+deploy-token-1\n"})
	tokenFile := filepath.Join(dir, "token") // relative paths are relative to the configuration directory
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RunAll(ctx, repos); err != nil {
		t.Fatalf("run with the token file: %v\n%+v", err, readJournal(t, g.Defaults.StateDir))
	}
	refs := backupRefs(t, BackupPath(g.Defaults.StateDir, "r"))
	if countPrefix(refs, "refs/heads/everref/remotes/origin/main/created_") != 1 ||
		countPrefix(refs, "refs/heads/everref/remotes/origin/feature/created_") != 1 {
		t.Fatalf("backup refs:\n%s", strings.Join(refs, "\n"))
	}
	fetches := 0
	for _, r := range srv.requests() {
		if r.fetch && r.authorized {
			fetches++
		}
		if r.fetch && !r.authorized && r.user != "" {
			t.Errorf("a fetch was sent with a wrong credential (user %q)", r.user)
		}
	}
	t.Logf("%d authenticated fetch request(s) of %d requests", fetches, len(srv.requests()))
	if fetches == 0 {
		t.Fatalf("no authenticated fetch reached the server: %+v", srv.requests())
	}

	// Nothing the guard or everref wrote down contains the token.
	leak := func(what, s string) {
		if strings.Contains(s, token) {
			t.Errorf("token leaked into %s", what)
		}
	}
	for _, f := range []string{
		filepath.Join(g.Defaults.StateDir, "backup.jsonl"),
		filepath.Join(BridgePath(g.Defaults.StateDir, "r"), ".git", "config"),
		filepath.Join(BackupPath(g.Defaults.StateDir, "r"), "config"),
	} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		leak(f, string(data))
	}
	if strings.Contains(remote, "@") {
		t.Fatalf("test remote %s carries a credential", remote)
	}

	// A wrong token fails the run; the backup is not touched and the error
	// does not echo the token.
	if err := os.WriteFile(tokenFile, []byte("wrong-"+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(backupRefs(t, BackupPath(g.Defaults.StateDir, "r")))
	res := g.RunRepo(ctx, "r")
	if res.OK {
		t.Fatal("run with a wrong token succeeded")
	}
	leak("the error", res.Error+res.Output)
	if after := len(backupRefs(t, BackupPath(g.Defaults.StateDir, "r"))); after != before {
		t.Fatalf("failed run changed the backup: %d -> %d refs", before, after)
	}
}
