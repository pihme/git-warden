package pushguard

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pihme/git-warden/internal/config"
)

// Server serves the guard repositories over Git smart HTTP through
// git http-backend, with HTTP basic auth against the agent's token.
type Server struct {
	ConfigDir  string
	HookBinary string // absolute path of push-guard, written into the hooks
	Log        *log.Logger
	Summary    string // what the preflight found, for the start-up log
}

// NewServer runs the Preflight (wall configuration, repos and their rules,
// git, gitleaks) and prepares a server; any problem stops serve from starting.
func NewServer(configDir, hookBinary string) (*Server, error) {
	ready, err := Preflight(context.Background(), configDir, nil)
	if err != nil {
		return nil, err
	}
	wall := ready.Wall
	if wall.AgentTokenFile == "" {
		return nil, errors.New("serve needs agent.token_file in defaults.yaml")
	}
	if _, err := readToken(wall.AgentTokenFile); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(hookBinary) {
		return nil, fmt.Errorf("hook binary must be an absolute path: %s", hookBinary)
	}
	abs, _ := filepath.Abs(configDir)
	return &Server{ConfigDir: abs, HookBinary: hookBinary, Log: log.New(os.Stderr, "push-guard: ", log.LstdFlags),
		Summary: fmt.Sprintf("git %s, %d repo(s)%s", ready.Git, len(ready.Repos), describeGitleaks(ready.Gitleaks))}, nil
}

func readToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("agent token: %w", err)
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", errors.New("agent token file is empty")
	}
	return tok, nil
}

// smartRoute splits /<name>.git/<rest> and accepts only the smart HTTP routes.
func smartRoute(r *http.Request) (name, service string, ok bool) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	repo, rest, found := strings.Cut(p, "/")
	if !found || !strings.HasSuffix(repo, ".git") {
		return "", "", false
	}
	name = strings.TrimSuffix(repo, ".git")
	if !config.ValidRepoName(name) {
		return "", "", false
	}
	switch {
	case rest == "info/refs" && r.Method == http.MethodGet:
		service = r.URL.Query().Get("service")
	case (rest == "git-upload-pack" || rest == "git-receive-pack") && r.Method == http.MethodPost:
		service = rest
	}
	if service != "git-upload-pack" && service != "git-receive-pack" {
		return "", "", false
	}
	return name, service, true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wall, err := config.Load(s.ConfigDir)
	if err != nil {
		s.Log.Printf("config: %v", err)
		http.Error(w, MsgInternal, http.StatusServiceUnavailable)
		return
	}
	token, err := readToken(wall.AgentTokenFile)
	if err != nil {
		s.Log.Printf("%v", err)
		http.Error(w, MsgInternal, http.StatusServiceUnavailable)
		return
	}
	_, pass, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(pass), []byte(token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="push-guard"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	name, _, ok := smartRoute(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	cfg, err := config.LoadRepo(s.ConfigDir, name)
	if errors.Is(err, config.ErrUnknownRepo) {
		http.Error(w, "unknown repo", http.StatusNotFound)
		return
	}
	if err != nil {
		s.Log.Printf("config %s: %v", name, err)
		http.Error(w, MsgInternal, http.StatusServiceUnavailable)
		return
	}
	path, err := EnsureRepo(r.Context(), cfg, s.HookBinary)
	if err != nil {
		s.Log.Printf("repo %s: %v", name, err)
		http.Error(w, MsgInternal, http.StatusServiceUnavailable)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/info/refs") {
		// Sync under the repo lock, so a running hook never sees refs move.
		unlock, err := LockRepo(cfg.StateDir, name)
		if err == nil {
			err = Sync(r.Context(), cfg, path)
			unlock()
		}
		if err != nil {
			s.Log.Printf("%v", err)
			http.Error(w, MsgInternal, http.StatusServiceUnavailable)
			return
		}
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		s.Log.Printf("git: %v", err)
		http.Error(w, MsgInternal, http.StatusServiceUnavailable)
		return
	}
	// A chunked Git push has no length. cgi answers 400 while
	// TransferEncoding is still "chunked", and omits CONTENT_LENGTH when
	// ContentLength < 0, so git http-backend never runs the hook.
	cleanup, err := spoolUnsizedBody(r, ServeBodyLimit)
	defer cleanup()
	if err != nil {
		s.Log.Printf("request body: %v", err)
		// A refusal the agent can act on is named; everything else keeps the
		// fixed internal error (AGENTS.md).
		msg := MsgInternal
		code := spoolStatus(err)
		if code != http.StatusServiceUnavailable {
			msg = http.StatusText(code)
		}
		http.Error(w, msg, code)
		return
	}
	h := &cgi.Handler{
		Path: gitBin,
		Args: []string{"http-backend"},
		Root: "/",
		Env: []string{
			"GIT_PROJECT_ROOT=" + filepath.Join(cfg.StateDir, "repos"),
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER=" + wall.AgentName,
		},
		InheritEnv: []string{"HOME", "TMPDIR", "LANG", "LC_ALL", "GIT_EXEC_PATH", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"},
		Logger:     s.Log,
	}
	h.ServeHTTP(w, r)
}

// ServeBodyLimit caps a request body with no Content-Length (REQ-PG-054).
// It sits well above the 50 MB the SIZE-LIMIT rule allows for new objects, so
// an oversized pack is still refused by the hook with a readable reason and
// only an endless stream is cut at the HTTP layer.
const ServeBodyLimit int64 = 1 << 30

// ServeBodyTimeout bounds how long an unsized body may take to arrive
// (REQ-PG-054). Without it a client that sends one byte per minute holds the
// handler and its spool file open indefinitely; the byte cap alone does not
// stop that.
const ServeBodyTimeout = 10 * time.Minute

// errBodyTooLarge is returned when an unsized request body exceeds the cap
// (REQ-PG-054). A distinct value lets the caller answer 413 rather than 503.
var errBodyTooLarge = errors.New("request body too large")

// errBodyStalled is returned when an unsized body stops arriving before
// ServeBodyTimeout (REQ-PG-054).
var errBodyStalled = errors.New("request body timed out")

// spoolStatus maps a spool error to the HTTP status the client sees. Every
// other error stays the generic internal error (AGENTS.md: reject with
// MsgInternal).
func spoolStatus(err error) int {
	switch {
	case errors.Is(err, errBodyTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, errBodyStalled):
		return http.StatusRequestTimeout
	}
	return http.StatusServiceUnavailable
}

// spoolUnsizedBody copies a body with no Content-Length into a temporary
// file and records that length on r. The pack is not held in memory. The
// caller removes the file. At most limit bytes are accepted; one byte more
// returns errBodyTooLarge (REQ-PG-054). A body that already carries a length
// is left untouched, so git http-backend sees the client's length and the
// hook still runs. A read error is returned unchanged.
func spoolUnsizedBody(r *http.Request, limit int64) (func(), error) {
	return spoolUnsizedBodyWithin(r, limit, ServeBodyTimeout)
}

// spoolUnsizedBodyWithin is spoolUnsizedBody with the read bound as a
// parameter, so tests do not wait for ServeBodyTimeout.
func spoolUnsizedBodyWithin(r *http.Request, limit int64, within time.Duration) (func(), error) {
	noop := func() {}
	if r.ContentLength >= 0 {
		return noop, nil
	}
	if r.Body == nil {
		return noop, errors.New("request body is missing")
	}
	f, err := os.CreateTemp("", "push-guard-pack-*")
	if err != nil {
		return noop, err
	}
	cleanup := func() {
		f.Close()
		os.Remove(f.Name())
	}
	// Timer that closes the body when the bound is reached, which makes the
	// blocked Read return and io.Copy stop.
	timer := time.AfterFunc(within, func() { r.Body.Close() })
	// Read one byte past the cap: reaching it proves the body is larger.
	n, err := io.Copy(f, io.LimitReader(r.Body, limit+1))
	if !timer.Stop() {
		cleanup()
		return cleanup, errBodyStalled
	}
	if err != nil {
		return cleanup, err
	}
	if n > limit {
		return cleanup, errBodyTooLarge
	}
	if err := r.Body.Close(); err != nil {
		return cleanup, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return cleanup, err
	}
	r.Body = f
	r.ContentLength = n
	r.TransferEncoding = nil
	return cleanup, nil
}
