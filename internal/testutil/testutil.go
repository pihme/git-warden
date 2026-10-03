// Package testutil has helpers for tests that need real Git repositories.
// Everything stays offline and inside t.TempDir().
package testutil

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// IsolateGit points git at an empty global config and a fixed identity, so a
// developer's own settings (signing, hooks, default branch) can't leak into
// tests. It changes the process environment and must run before any test
// starts git (e.g. from TestMain).
func IsolateGit() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "push-guard-gitconfig-")
	if err != nil {
		return nil, err
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte("[init]\n\tdefaultBranch = main\n[protocol \"file\"]\n\tallow = always\n"), 0o600); err != nil {
		return nil, err
	}
	env := map[string]string{
		"GIT_CONFIG_GLOBAL":   global,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME":     "Test Agent",
		"GIT_AUTHOR_EMAIL":    "agent@example.invalid",
		"GIT_COMMITTER_NAME":  "Test Agent",
		"GIT_COMMITTER_EMAIL": "agent@example.invalid",
		"GIT_TERMINAL_PROMPT": "0",
	}
	for k, v := range env {
		os.Setenv(k, v)
	}
	return func() { os.RemoveAll(dir) }, nil
}

// Repo is a Git repository in a temporary directory.
type Repo struct {
	t   testing.TB
	Dir string
	Env []string // extra environment for every command
}

// Init creates a repository (bare or with a work tree) on branch main.
func Init(t testing.TB, bare bool) *Repo {
	t.Helper()
	dir := t.TempDir()
	if bare {
		dir = filepath.Join(dir, "repo.git")
	}
	args := []string{"init", "--quiet", "-b", "main"}
	if bare {
		args = append(args, "--bare")
	}
	Run(t, "", append(args, dir)...)
	return &Repo{t: t, Dir: dir}
}

// Open wraps an existing repository.
func Open(t testing.TB, dir string) *Repo { return &Repo{t: t, Dir: dir} }

// Run runs git in dir and fails the test on error. It returns trimmed stdout.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := Try(dir, nil, args...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return out
}

// Try runs git in dir and returns trimmed stdout, or an error with stderr.
func Try(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %v\n%s%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Git runs git in the repository and fails the test on error.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	out, err := Try(r.Dir, r.Env, args...)
	if err != nil {
		r.t.Fatalf("%v", err)
	}
	return out
}

// Try runs git in the repository and returns stdout and the error.
func (r *Repo) Try(args ...string) (string, error) { return Try(r.Dir, r.Env, args...) }

// Write writes a file in the work tree, creating directories.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	r.WriteMode(path, content, 0o644)
}

// WriteMode writes a file with the given permissions.
func (r *Repo) WriteMode(path, content string, mode os.FileMode) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Chmod(full, mode); err != nil {
		r.t.Fatal(err)
	}
}

// Commit writes files (path -> content), stages everything and commits.
// It returns the new commit's SHA.
func (r *Repo) Commit(msg string, files map[string]string) string {
	r.t.Helper()
	for p, c := range files {
		r.Write(p, c)
	}
	r.Git("add", "-A")
	r.Git("commit", "--quiet", "--allow-empty", "--no-verify", "-m", msg)
	return r.Head()
}

// Head returns the SHA of HEAD.
func (r *Repo) Head() string {
	r.t.Helper()
	return r.Git("rev-parse", "HEAD")
}

// FakeSigned creates a commit object with a gpgsig header on top of parent
// (empty for a root commit) with tree, without moving any ref. The signature
// is not real: the Push Guard only checks that one is present.
func (r *Repo) FakeSigned(tree, parent, msg string) string {
	r.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "tree %s\n", tree)
	if parent != "" {
		fmt.Fprintf(&b, "parent %s\n", parent)
	}
	b.WriteString("author Test Agent <agent@example.invalid> 1700000000 +0000\n")
	b.WriteString("committer Test Agent <agent@example.invalid> 1700000000 +0000\n")
	b.WriteString("gpgsig -----BEGIN PGP SIGNATURE-----\n \n fake\n -----END PGP SIGNATURE-----\n")
	b.WriteString("\n" + msg + "\n")
	cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
	cmd.Dir = r.Dir
	cmd.Stdin = strings.NewReader(b.String())
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("hash-object: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// WriteFiles writes files below dir (path -> content).
func WriteFiles(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// FakeAWSKey returns a random string in the shape of an AWS access key id, so
// the secret scanner has something to find. It is not a real credential.
func FakeAWSKey() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "AKIA" + string(b)
}
