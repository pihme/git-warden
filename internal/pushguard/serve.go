package pushguard

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
