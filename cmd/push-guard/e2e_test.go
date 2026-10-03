package main

// End-to-end tests: a bare "remote", the guard's bare repository with the
// pre-receive hook running the compiled push-guard binary, and an agent clone
// that pushes to the guard by file path (and once over HTTP through serve).

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/pushguard"
	"github.com/pihme/git-warden/internal/testutil"
)

var binary string

func TestMain(m *testing.M) {
	cleanup, err := testutil.IsolateGit()
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp("", "push-guard-bin-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "push-guard")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building push-guard:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	cleanup()
	os.Exit(code)
}

const repoName = "hermetarium"

type env struct {
	t        *testing.T
	base     string
	config   string
	state    string
	notify   string
	remote   *testutil.Repo
	agent    *testutil.Repo
	guardDir string
}

// setup builds remote, config, guard repository and agent clone. rules is
// YAML placed under "rules:" in the repo's warden.yaml.
func setup(t *testing.T, rules string) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{t: t, base: base, config: filepath.Join(base, "warden"), state: filepath.Join(base, "state"),
		notify: filepath.Join(base, "notify.jsonl")}

	e.remote = testutil.Open(t, filepath.Join(base, "remote.git"))
	testutil.Run(t, "", "init", "--quiet", "--bare", "-b", "main", e.remote.Dir)
	seed := testutil.Open(t, filepath.Join(base, "seed"))
	testutil.Run(t, "", "clone", "--quiet", e.remote.Dir, seed.Dir)
	seed.Commit("initial", map[string]string{"README.md": "hello\n"})
	seed.Git("push", "--quiet", "origin", "HEAD:main")

	testutil.WriteFiles(t, e.config, map[string]string{
		"defaults.yaml": fmt.Sprintf(`agent:
  name: test-agent
  token_file: agent-token
state_dir: %s
notify:
  command: [sh, -c, 'cat >> "$0"', %s]
rules:
  CONTENT-SECRET: {enabled: false}
`, e.state, e.notify),
		"agent-token":                        "synthetic-test-token\n",
		"repos/" + repoName + "/warden.yaml": "remote: " + e.remote.Dir + "\nrules:\n" + indent(rules) + "\n",
	})
	e.run("init-repo", "--config", e.config, repoName)
	e.guardDir = pushguard.RepoPath(e.state, repoName)
	e.agent = testutil.Open(t, filepath.Join(base, "agent"))
	testutil.Run(t, "", "clone", "--quiet", e.guardDir, e.agent.Dir)
	return e
}

func indent(s string) string {
	if strings.TrimSpace(s) == "" {
		return "  {}"
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, "  "+l)
	}
	return strings.Join(out, "\n")
}

// run runs the push-guard binary and fails the test on error.
func (e *env) run(args ...string) string {
	e.t.Helper()
	out, err := exec.Command(binary, args...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("push-guard %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// push runs git push in the agent clone and returns its output.
func (e *env) push(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"push"}, args...)...)
	cmd.Dir = e.agent.Dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *env) mustPush(args ...string) string {
	e.t.Helper()
	out, err := e.push(args...)
	if err != nil {
		e.t.Fatalf("push failed: %v\n%s", err, out)
	}
	return out
}

func (e *env) mustReject(args ...string) string {
	e.t.Helper()
	out, err := e.push(args...)
	if err == nil {
		e.t.Fatalf("push was accepted:\n%s", out)
	}
	return out
}

func (e *env) remoteRef(ref string) string {
	out, _ := e.remote.Try("rev-parse", "--verify", "--quiet", ref)
	return out
}

func (e *env) entries() []journal.Entry {
	e.t.Helper()
	es, err := journal.Open(e.state).Read()
	if err != nil {
		e.t.Fatal(err)
	}
	return es
}

func (e *env) lastPush() journal.Entry {
	e.t.Helper()
	es := e.entries()
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Event == journal.EventPush {
			return es[i]
		}
	}
	e.t.Fatal("no push in the journal")
	return journal.Entry{}
}

func (e *env) warnings() []pushguard.Warning {
	e.t.Helper()
	data, err := os.ReadFile(e.notify)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var out []pushguard.Warning
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for dec.More() {
		var w pushguard.Warning
		if err := dec.Decode(&w); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, w)
	}
	return out
}

func assertContains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("output does not contain %q:\n%s", want, s)
	}
}

func assertNotContains(t *testing.T, s string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(s, u) {
			t.Fatalf("output contains %q:\n%s", u, s)
		}
	}
}

func TestGreenPushIsForwarded(t *testing.T) {
	e := setup(t, "")
	head := e.agent.Commit("docs", map[string]string{"README.md": "hello\nworld\n"})
	out := e.mustPush("origin", "main")
	assertContains(t, out, "forwarded to the remote")
	if got := e.remoteRef("refs/heads/main"); got != head {
		t.Fatalf("remote main = %s, want %s", got, head)
	}
	p := e.lastPush()
	if p.Verdict != "green" || !p.Forwarded || p.Agent != "test-agent" || p.Updates[0].Kind != "ff" {
		t.Fatalf("journal entry %+v", p)
	}
	// a new branch is forwarded too
	e.agent.Git("checkout", "--quiet", "-b", "agent/feature")
	feat := e.agent.Commit("feature", map[string]string{"src/a.txt": "a\n"})
	e.mustPush("origin", "agent/feature")
	if got := e.remoteRef("refs/heads/agent/feature"); got != feat {
		t.Fatalf("remote feature = %s", got)
	}
}

func TestNonFastForwardIsRedAndSilent(t *testing.T) {
	e := setup(t, "")
	before := e.remoteRef("refs/heads/main")
	e.agent.Write("README.md", "rewritten\n")
	e.agent.Git("commit", "--quiet", "-a", "--amend", "-m", "rewrite history")
	out := e.mustReject("--force", "origin", "main")
	assertContains(t, out, "rejected: waiting for a human (push ")
	assertNotContains(t, out, "REF-NON-FF", "non_ff", "fast-forward of the remote")
	if got := e.remoteRef("refs/heads/main"); got != before {
		t.Fatalf("remote moved to %s", got)
	}
	p := e.lastPush()
	if p.Verdict != "red" || p.Forwarded || p.Findings[0].Rule != "REF-NON-FF" {
		t.Fatalf("journal entry %+v", p)
	}
	if _, err := os.Stat(p.Bundle); err != nil {
		t.Fatalf("pending bundle: %v", err)
	}
	heads := testutil.Run(t, e.base, "bundle", "list-heads", p.Bundle)
	assertContains(t, heads, e.agent.Head()+" refs/heads/main")
	ws := e.warnings()
	if len(ws) != 1 || ws[0].Kind != "red_push" || ws[0].Findings[0].Rule != "REF-NON-FF" || ws[0].Untrusted == nil {
		t.Fatalf("warnings %+v", ws)
	}
	assertContains(t, ws[0].Untrusted.Commits[0].Message, "rewrite history")
}

func TestYellowPushGetsRuleMessage(t *testing.T) {
	e := setup(t, "")
	before := e.remoteRef("refs/heads/main")
	e.agent.Commit("deps", map[string]string{"package.json": "{\"name\": \"x\"}\n"})
	out := e.mustReject("origin", "main")
	assertContains(t, out, "PATH-YELLOW refs/heads/main package.json")
	assertContains(t, out, "leave the change out")
	if got := e.remoteRef("refs/heads/main"); got != before {
		t.Fatal("yellow push reached the remote")
	}
	if len(e.warnings()) != 0 {
		t.Fatal("yellow push warned a human")
	}
}

func TestApproveThenRepushSameSHA(t *testing.T) {
	e := setup(t, "")
	head := e.agent.Commit("ci", map[string]string{".github/workflows/ci.yml": "on: push\n"})
	out := e.mustReject("origin", "main")
	assertContains(t, out, "waiting for a human")
	assertNotContains(t, out, "PATH-RED", "workflows")

	approved := e.run("approve", "--config", e.config, repoName, "main", head[:10])
	assertContains(t, approved, "approved refs/heads/main "+head)
	assertContains(t, approved, "PATH-RED")

	out = e.mustPush("origin", "main")
	assertContains(t, out, "forwarded")
	if got := e.remoteRef("refs/heads/main"); got != head {
		t.Fatalf("remote main = %s, want %s", got, head)
	}
	// the approval is used up and per SHA: another workflow change is red again
	e.agent.Commit("ci again", map[string]string{".github/workflows/ci.yml": "on: [push]\n"})
	assertContains(t, e.mustReject("origin", "main"), "waiting for a human")
	es := e.entries()
	var appr journal.Entry
	for _, x := range es {
		if x.Event == journal.EventApprove {
			appr = x
		}
	}
	if appr.SHA != head || len(appr.Rules) != 1 || appr.Rules[0] != "PATH-RED" || appr.Push == "" {
		t.Fatalf("approval entry %+v", appr)
	}
}

func TestYellowStreakBecomesRed(t *testing.T) {
	e := setup(t, "RATE-YELLOW-STREAK: {max_yellow: 2}")
	for i := 0; i < 2; i++ {
		e.agent.Commit(fmt.Sprintf("deps %d", i), map[string]string{"package.json": fmt.Sprintf("{\"v\": %d}\n", i)})
		assertContains(t, e.mustReject("origin", "main"), "PATH-YELLOW")
	}
	e.agent.Commit("deps 3", map[string]string{"package.json": "{\"v\": 3}\n"})
	out := e.mustReject("origin", "main")
	assertContains(t, out, "waiting for a human")
	assertNotContains(t, out, "PATH-YELLOW", "RATE-YELLOW-STREAK")
	ws := e.warnings()
	if len(ws) != 1 || ws[0].Kind != "yellow_streak" || ws[0].Reset == "" {
		t.Fatalf("warnings %+v", ws)
	}
	// while the streak is active, even a harmless push is red
	e.agent.Git("reset", "--quiet", "--hard", "origin/main")
	e.agent.Commit("harmless", map[string]string{"notes.txt": "x\n"})
	assertContains(t, e.mustReject("origin", "main"), "waiting for a human")

	e.run("reset-streak", "--config", e.config, repoName)
	assertContains(t, e.mustPush("origin", "main"), "forwarded")
}

func TestSecretIsRed(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		if os.Getenv("GITLEAKS_REQUIRED") != "" {
			t.Fatal("gitleaks not on PATH but GITLEAKS_REQUIRED is set")
		}
		t.Skip("gitleaks not on PATH")
	}
	e := setup(t, "CONTENT-SECRET: {enabled: true}")
	e.agent.Commit("config", map[string]string{"deploy.ini": "aws_access_key_id = " + testutil.FakeAWSKey() + "\n"})
	out := e.mustReject("origin", "main")
	assertContains(t, out, "waiting for a human")
	assertNotContains(t, out, "CONTENT-SECRET", "aws")
	p := e.lastPush()
	if p.Verdict != "red" || p.Findings[0].Rule != "CONTENT-SECRET" || p.Findings[0].Path != "deploy.ini" {
		t.Fatalf("journal entry %+v", p)
	}
	data, _ := os.ReadFile(journal.Open(e.state).Path)
	assertNotContains(t, string(data), "AKIA")
}

func TestMissingScannerFailsClosed(t *testing.T) {
	e := setup(t, "")
	// gitleaks disappears (or is misconfigured) after the guard repo was set up
	cfg := filepath.Join(e.config, "repos", repoName, "warden.yaml")
	os.WriteFile(cfg, []byte("remote: "+e.remote.Dir+"\nscanner: {gitleaks: /nonexistent/gitleaks}\nrules:\n  CONTENT-SECRET: {enabled: true}\n"), 0o644)
	e.agent.Commit("docs", map[string]string{"README.md": "x\n"})
	out := e.mustReject("origin", "main")
	assertContains(t, out, "internal error, try again later")
	p := e.lastPush()
	if p.Reason != journal.ReasonInternalError || p.Error == "" {
		t.Fatalf("journal entry %+v", p)
	}
	if len(e.warnings()) != 0 {
		t.Fatal("internal error warned as a violation")
	}
	if !strings.Contains(p.Error, "preflight: gitleaks not found") {
		t.Fatalf("hook did not fail in its preflight: %s", p.Error)
	}
	// init-repo and serve refuse to start at all
	if out, err := exec.Command(binary, "init-repo", "--config", e.config, repoName).CombinedOutput(); err == nil ||
		!strings.Contains(string(out), "gitleaks not found") {
		t.Fatalf("init-repo without gitleaks: %v\n%s", err, out)
	}
	if _, err := pushguard.NewServer(e.config, binary); err == nil || !strings.Contains(err.Error(), "gitleaks not found") {
		t.Fatalf("serve without gitleaks: %v", err)
	}
}

func TestRemoteRejectionIsPassedOn(t *testing.T) {
	e := setup(t, "")
	hook := filepath.Join(e.remote.Dir, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'protected by the remote policy' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.agent.Commit("docs", map[string]string{"README.md": "x\n"})
	out := e.mustReject("origin", "main")
	assertContains(t, out, "the remote rejected the push")
	assertContains(t, out, "protected by the remote policy")
	assertNotContains(t, out, e.remote.Dir)
	if p := e.lastPush(); p.Forwarded || p.RemoteMessage == "" {
		t.Fatalf("journal entry %+v", p)
	}
}

func TestRemoteMovedByOtherWriter(t *testing.T) {
	e := setup(t, "")
	// another writer pushes straight to the remote; the guard has not seen it
	other := testutil.Open(t, filepath.Join(e.base, "other"))
	testutil.Run(t, "", "clone", "--quiet", e.remote.Dir, other.Dir)
	moved := other.Commit("other writer", map[string]string{"other.txt": "x\n"})
	other.Git("push", "--quiet", "origin", "HEAD:main")

	e.agent.Git("checkout", "--quiet", "-b", "feature")
	feat := e.agent.Commit("feature", map[string]string{"f.txt": "f\n"})
	assertContains(t, e.mustPush("origin", "feature"), "forwarded")
	if e.remoteRef("refs/heads/feature") != feat || e.remoteRef("refs/heads/main") != moved {
		t.Fatal("remote state wrong")
	}
	// a push to main that doesn't contain the other writer's commit is non-ff
	e.agent.Git("checkout", "--quiet", "main")
	e.agent.Commit("stale", map[string]string{"s.txt": "s\n"})
	assertContains(t, e.mustReject("origin", "main"), "waiting for a human")
	if e.remoteRef("refs/heads/main") != moved {
		t.Fatal("stale push reached the remote")
	}
}

func TestAllowedDeleteUsesLease(t *testing.T) {
	e := setup(t, "REF-DELETE: {allow: ['refs/heads/agent/.*']}")
	e.agent.Git("checkout", "--quiet", "-b", "agent/tmp")
	e.agent.Commit("tmp", map[string]string{"t.txt": "t\n"})
	e.mustPush("origin", "agent/tmp")
	e.mustPush("origin", "--delete", "agent/tmp")
	if e.remoteRef("refs/heads/agent/tmp") != "" {
		t.Fatal("allowed delete not forwarded")
	}
	assertContains(t, e.mustReject("origin", "--delete", "main"), "waiting for a human")
}

func TestUnknownRepo(t *testing.T) {
	e := setup(t, "")
	cmd := exec.Command(binary, "pre-receive", "--config", e.config, "--repo", "nope")
	cmd.Dir = e.guardDir
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("unknown repo accepted")
	}
	assertContains(t, string(out), "rejected: unknown repo")
}

func TestRateLimit(t *testing.T) {
	e := setup(t, "RATE-LIMIT: {max_pushes: 2}")
	for i := 0; i < 2; i++ {
		e.agent.Commit(fmt.Sprintf("c%d", i), map[string]string{"n.txt": fmt.Sprint(i)})
		e.mustPush("origin", "main")
	}
	e.agent.Commit("c3", map[string]string{"n.txt": "3"})
	assertContains(t, e.mustReject("origin", "main"), "rate limited, try later")
	assertContains(t, e.mustReject("origin", "main"), "rate limited, try later")
	if ws := e.warnings(); len(ws) != 1 || ws[0].Kind != "rate_limited" {
		t.Fatalf("warnings %+v", ws)
	}
}

func TestServeOverHTTP(t *testing.T) {
	e := setup(t, "")
	srv, err := pushguard.NewServer(e.config, binary)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := strings.Replace(hs.URL, "http://", "http://agent:synthetic-test-token@", 1) + "/" + repoName + ".git"
	clone := testutil.Open(t, filepath.Join(e.base, "http-agent"))
	testutil.Run(t, "", "clone", "--quiet", url, clone.Dir)
	head := clone.Commit("over http", map[string]string{"http.txt": "x\n"})
	out, err := clone.Try("push", "origin", "main")
	if err != nil {
		t.Fatalf("push over HTTP: %v\n%s", err, out)
	}
	if e.remoteRef("refs/heads/main") != head {
		t.Fatal("HTTP push not forwarded")
	}
	bad := strings.Replace(hs.URL, "http://", "http://agent:wrong@", 1) + "/" + repoName + ".git"
	if _, err := testutil.Try(e.base, nil, "ls-remote", bad); err == nil {
		t.Fatal("wrong token accepted")
	}
	unknown := strings.Replace(hs.URL, "http://", "http://agent:synthetic-test-token@", 1) + "/nope.git"
	if _, err := testutil.Try(e.base, nil, "ls-remote", unknown); err == nil {
		t.Fatal("unknown repo served")
	}
}
