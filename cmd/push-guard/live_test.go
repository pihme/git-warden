package main

// Live test against a real HTTPS remote (in CI: this repository on GitHub).
// It only runs when WARDEN_LIVE_REMOTE and WARDEN_LIVE_TOKEN_FILE are set and
// only ever touches one branch, testrun-<UTC timestamp>-<run>, which it
// deletes at the end (through the guard, and directly as a fallback).

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/pushguard"
	"github.com/pihme/git-warden/internal/testutil"
)

type liveRemote struct {
	t      *testing.T
	url    string
	header []string // git config via environment, keeps the token out of argv
}

func (r *liveRemote) git(dir string, args ...string) (string, error) {
	return testutil.Try(dir, r.header, args...)
}

// ref returns the remote's SHA for ref, or "" if it doesn't exist.
func (r *liveRemote) ref(ref string) string {
	r.t.Helper()
	out, err := r.git("", "ls-remote", r.url, ref)
	if err != nil {
		r.t.Fatalf("ls-remote: %v", err)
	}
	if f := strings.Fields(out); len(f) > 0 {
		return f[0]
	}
	return ""
}

func TestLiveRemote(t *testing.T) {
	url, tokenFile := os.Getenv("WARDEN_LIVE_REMOTE"), os.Getenv("WARDEN_LIVE_TOKEN_FILE")
	if url == "" || tokenFile == "" {
		t.Skip("WARDEN_LIVE_REMOTE and WARDEN_LIVE_TOKEN_FILE not set")
	}
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + strings.TrimSpace(string(token))))
	remote := &liveRemote{t: t, url: url, header: []string{
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + basic}}

	run := os.Getenv("GITHUB_RUN_ID")
	if run == "" {
		run = fmt.Sprint(os.Getpid())
	}
	branch := fmt.Sprintf("testrun-%s-%s", time.Now().UTC().Format("20060102T150405Z"), run)
	ref := "refs/heads/" + branch
	t.Cleanup(func() {
		if remote.ref(ref) != "" {
			if _, err := remote.git("", "push", "--quiet", url, ":"+ref); err != nil {
				t.Errorf("cleanup of %s failed: %v", branch, err)
			}
		}
	})

	base := t.TempDir()
	e := &env{t: t, base: base, config: filepath.Join(base, "warden"), state: filepath.Join(base, "state"),
		notify: filepath.Join(base, "notify.jsonl")}
	testutil.WriteFiles(t, e.config, map[string]string{
		"defaults.yaml": fmt.Sprintf(`agent:
  name: live-test-agent
state_dir: %s
notify:
  command: [sh, -c, 'cat >> "$0"', %s]
rules:
  CONTENT-SECRET: {enabled: false}
`, e.state, e.notify),
		"repos/live/warden.yaml": fmt.Sprintf(`remote: %s
credential: %s
rules:
  REF-DELETE: {allow: ['refs/heads/testrun-.*']}
`, url, tokenFile),
	})
	e.run("init-repo", "--config", e.config, "live")
	e.guardDir = pushguard.RepoPath(e.state, "live")
	e.agent = testutil.Open(t, filepath.Join(base, "agent"))
	testutil.Run(t, "", "clone", "--quiet", e.guardDir, e.agent.Dir)
	e.agent.Git("checkout", "--quiet", "-b", branch, "origin/main")

	// a green push creates the branch on the real remote
	first := e.agent.Commit("live test: create", map[string]string{"testrun.txt": branch + "\n"})
	assertContains(t, e.mustPush("origin", branch), "forwarded to the remote")
	if got := remote.ref(ref); got != first {
		t.Fatalf("remote %s = %q, want %s", branch, got, first)
	}

	// a fast-forward is green as well
	second := e.agent.Commit("live test: fast-forward", map[string]string{"testrun.txt": branch + "\nff\n"})
	assertContains(t, e.mustPush("origin", branch), "forwarded")
	if got := remote.ref(ref); got != second {
		t.Fatalf("remote %s = %q after ff, want %s", branch, got, second)
	}

	// rewriting history is red and leaves the remote alone
	e.agent.Write("testrun.txt", branch+"\nrewritten\n")
	e.agent.Git("commit", "--quiet", "-a", "--amend", "-m", "live test: rewrite")
	rewritten := e.agent.Head()
	assertContains(t, e.mustReject("--force", "origin", branch), "waiting for a human")
	if got := remote.ref(ref); got != second {
		t.Fatalf("red push moved the remote to %s", got)
	}

	// after a human approves exactly that SHA it goes out (with a lease)
	e.run("approve", "--config", e.config, "live", branch, rewritten[:12])
	assertContains(t, e.mustPush("--force", "origin", branch), "forwarded")
	if got := remote.ref(ref); got != rewritten {
		t.Fatalf("approved push: remote = %s, want %s", got, rewritten)
	}

	// another writer moves the branch on the remote; the agent's stale push is red
	other := testutil.Open(t, filepath.Join(base, "other"))
	other.Env = remote.header
	if _, err := testutil.Try("", remote.header, "clone", "--quiet", "--branch", branch, "--single-branch", url, other.Dir); err != nil {
		t.Fatal(err)
	}
	moved := other.Commit("live test: other writer", map[string]string{"other.txt": "x\n"})
	other.Git("push", "--quiet", "origin", branch)
	e.agent.Commit("live test: stale", map[string]string{"stale.txt": "s\n"})
	assertContains(t, e.mustReject("origin", branch), "waiting for a human")
	if got := remote.ref(ref); got != moved {
		t.Fatalf("stale push moved the remote to %s, want %s", got, moved)
	}

	// an allowed delete goes through the guard and removes the branch
	assertContains(t, e.mustPush("origin", "--delete", branch), "forwarded")
	if got := remote.ref(ref); got != "" {
		t.Fatalf("branch still on the remote at %s", got)
	}
}
