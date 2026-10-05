package main

// End-to-end requirement tests for the verdict requirements REQ-PG-001,
// REQ-PG-003, REQ-PG-004, REQ-PG-005 and the rate rules REQ-PG-036/037; see
// docs/push-guard-testspec.md. They use the harness from e2e_test.go.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/journal"
)

// remoteLines returns the non-empty "remote:" lines of git push output, i.e.
// everything the guard said to the agent.
func remoteLines(out string) []string {
	var ls []string
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(l), "remote:"); ok && strings.TrimSpace(s) != "" {
			ls = append(ls, strings.TrimSpace(s))
		}
	}
	return ls
}

func (e *env) writeRepoConfig(body string) {
	e.t.Helper()
	cfg := filepath.Join(e.config, "repos", repoName, "warden.yaml")
	if err := os.WriteFile(cfg, []byte("remote: "+e.remote.Dir+"\n"+body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestREQ_PG_001_RedResponseIsOnlyTheWaitingLine(t *testing.T) {
	e := setup(t, "")
	before := e.remoteRef("refs/heads/main")
	// one red (PATH-RED) and one yellow (PATH-YELLOW) finding in the same push
	e.agent.Commit("ci and deps", map[string]string{
		".github/workflows/ci.yml": "on: push\n",
		"package.json":             "{\"name\": \"x\"}\n",
	})
	out := e.mustReject("origin", "main")
	ls := remoteLines(out)
	p := e.lastPush()
	if len(ls) != 1 || ls[0] != "rejected: waiting for a human (push "+p.ID+")" {
		t.Fatalf("REQ-PG-001: the agent saw more than the waiting line: %q", ls)
	}
	assertNotContains(t, out, "PATH-RED", "PATH-YELLOW", "workflows", "package.json")
	if p.Verdict != "red" || p.Forwarded || len(p.Findings) < 2 {
		t.Fatalf("journal entry %+v", p)
	}
	if ws := e.warnings(); len(ws) != 1 || ws[0].Kind != "red_push" || ws[0].Push != p.ID {
		t.Fatalf("REQ-PG-001: owner not warned: %+v", ws)
	}
	if e.remoteRef("refs/heads/main") != before {
		t.Fatal("red push reached the remote")
	}
}

func TestREQ_PG_003_GreenForwardsExactlyTheCheckedSHAs(t *testing.T) {
	e := setup(t, "")
	main := e.agent.Commit("docs", map[string]string{"README.md": "hello\nworld\n"})
	e.agent.Git("checkout", "--quiet", "-b", "agent/feature")
	feat := e.agent.Commit("feature", map[string]string{"src/a.txt": "a\n"})
	e.agent.Git("checkout", "--quiet", "main")
	out := e.mustPush("origin", "main", "agent/feature")
	assertContains(t, out, "forwarded to the remote")
	if e.remoteRef("refs/heads/main") != main || e.remoteRef("refs/heads/agent/feature") != feat {
		t.Fatal("REQ-PG-003: remote does not hold the checked SHAs")
	}
	got := map[string]string{}
	for _, u := range e.lastPush().Updates {
		got[u.Ref] = u.New
	}
	if got["refs/heads/main"] != main || got["refs/heads/agent/feature"] != feat || len(got) != 2 {
		t.Fatalf("REQ-PG-003: journal updates %v", got)
	}
}

func TestREQ_PG_003_MixedPushForwardsNothing(t *testing.T) {
	e := setup(t, "")
	before := e.remoteRef("refs/heads/main")
	e.agent.Commit("docs", map[string]string{"README.md": "harmless\n"})
	e.agent.Git("checkout", "--quiet", "-b", "agent/ci")
	e.agent.Commit("ci", map[string]string{".github/workflows/ci.yml": "on: push\n"})
	e.agent.Git("checkout", "--quiet", "main")
	assertContains(t, e.mustReject("origin", "main", "agent/ci"), "waiting for a human")
	if e.remoteRef("refs/heads/main") != before || e.remoteRef("refs/heads/agent/ci") != "" {
		t.Fatal("REQ-PG-003: a push that is not green forwarded some of its refs")
	}
}

func TestREQ_PG_003_AllowedTagMoveIsForwarded(t *testing.T) {
	e := setup(t, "REF-TAG-NEW: {allow: ['refs/tags/snap-.*']}\nREF-TAG-MOVE: {allow: ['refs/tags/snap-.*']}")
	first := e.agent.Head()
	e.agent.Git("tag", "snap-1")
	e.mustPush("origin", "snap-1")
	second := e.agent.Commit("next", map[string]string{"n.txt": "n\n"})
	e.mustPush("origin", "main")
	e.agent.Git("tag", "-f", "snap-1")
	e.mustPush("--force", "origin", "snap-1")
	if got := e.remoteRef("refs/tags/snap-1"); got != second || got == first {
		t.Fatalf("REQ-PG-003: allowed tag move not forwarded (remote %s)", got)
	}
	// the allow covers only snap-*: another new tag is still yellow
	e.agent.Git("tag", "other")
	assertContains(t, e.mustReject("origin", "other"), "REF-TAG-NEW refs/tags/other")
}

func TestREQ_PG_005_ApprovedRewriteIsForwardedWithoutRecheck(t *testing.T) {
	e := setup(t, "")
	e.agent.Write("README.md", "rewritten\n")
	e.agent.Git("commit", "--quiet", "-a", "--amend", "-m", "rewrite history")
	head := e.agent.Head()
	assertContains(t, e.mustReject("--force", "origin", "main"), "waiting for a human")
	e.run("approve", "--config", e.config, repoName, "main", head[:12])
	assertContains(t, e.mustPush("--force", "origin", "main"), "forwarded")
	if e.remoteRef("refs/heads/main") != head {
		t.Fatal("REQ-PG-005: approved rewrite not forwarded")
	}
	p := e.lastPush()
	if !p.Forwarded || len(p.Approved) != 1 || p.Approved[0].SHA != head {
		t.Fatalf("journal entry %+v", p)
	}
}

func TestREQ_PG_005_ApprovalIsPerRefAndSHA(t *testing.T) {
	e := setup(t, "")
	e.agent.Git("checkout", "--quiet", "-b", "agent/a")
	head := e.agent.Commit("ci", map[string]string{".github/workflows/ci.yml": "on: push\n"})
	assertContains(t, e.mustReject("origin", "agent/a"), "waiting for a human")
	e.run("approve", "--config", e.config, repoName, "agent/a", head[:10])
	// the same SHA on another ref is checked from scratch
	assertContains(t, e.mustReject("origin", "agent/a:agent/b"), "waiting for a human")
	if e.remoteRef("refs/heads/agent/b") != "" {
		t.Fatal("REQ-PG-005: approval leaked to another ref")
	}
	// a descendant of the approved SHA on the approved ref is checked too
	e.agent.Commit("more ci", map[string]string{".github/workflows/ci.yml": "on: [push]\n"})
	assertContains(t, e.mustReject("origin", "agent/a"), "waiting for a human")
	// exactly the approved SHA still goes through
	assertContains(t, e.mustPush("origin", head+":refs/heads/agent/a"), "forwarded")
	if e.remoteRef("refs/heads/agent/a") != head {
		t.Fatal("REQ-PG-005: approved SHA not forwarded")
	}
}

func TestREQ_PG_004_InternalErrorThenFreshCheck(t *testing.T) {
	e := setup(t, "")
	e.writeRepoConfig("gitleaks: {path: /nonexistent/gitleaks}\nrules:\n  CONTENT-SECRET: {enabled: true}\n")
	head := e.agent.Commit("docs", map[string]string{"README.md": "x\n"})
	out := e.mustReject("origin", "main")
	if ls := remoteLines(out); len(ls) != 1 || ls[0] != "internal error, try again later" {
		t.Fatalf("REQ-PG-004: response %q", ls)
	}
	p := e.lastPush()
	if p.Reason != journal.ReasonInternalError || p.Forwarded {
		t.Fatalf("journal entry %+v", p)
	}
	// once the cause is gone, the same push is checked from scratch and goes through
	e.writeRepoConfig("rules: {}\n")
	assertContains(t, e.mustPush("origin", "main"), "forwarded")
	if e.remoteRef("refs/heads/main") != head {
		t.Fatal("REQ-PG-004: push after the internal error not forwarded")
	}
}

func TestREQ_PG_004_ScannerTimeoutIsInternalError(t *testing.T) {
	e := setup(t, "")
	scanner := filepath.Join(e.base, "slow gitleaks")
	if err := os.WriteFile(scanner, []byte("#!/bin/sh\n[ \"$1\" = version ] && { echo 8.30.1; exit 0; }\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.writeRepoConfig(fmt.Sprintf("timeout: 2s\ngitleaks: {path: '%s'}\nrules:\n  CONTENT-SECRET: {enabled: true}\n", scanner))
	e.agent.Commit("docs", map[string]string{"README.md": "x\n"})
	start := time.Now()
	out := e.mustReject("origin", "main")
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("REQ-PG-004: timeout not enforced, push took %s", d)
	}
	if ls := remoteLines(out); len(ls) != 1 || ls[0] != "internal error, try again later" {
		t.Fatalf("REQ-PG-004: response %q", ls)
	}
	if p := e.lastPush(); p.Reason != journal.ReasonInternalError || p.Forwarded {
		t.Fatalf("journal entry %+v", p)
	}
}

func TestREQ_PG_036_RateLimitedPushIsRedWithFixedMessage(t *testing.T) {
	e := setup(t, "RATE-LIMIT: {max_pushes: 1, window: 3s}")
	e.agent.Commit("c1", map[string]string{"n.txt": "1"})
	e.mustPush("origin", "main")
	head := e.agent.Commit("c2", map[string]string{"n.txt": "2"})
	out := e.mustReject("origin", "main")
	if ls := remoteLines(out); len(ls) != 1 || ls[0] != "push-guard: rate limited, try later" {
		t.Fatalf("REQ-PG-036: response %q", ls)
	}
	p := e.lastPush()
	if p.Verdict != "red" || p.Forwarded || p.Reason != journal.ReasonRateLimited ||
		len(p.Findings) != 1 || p.Findings[0].Rule != "RATE-LIMIT" {
		t.Fatalf("REQ-PG-036: journal entry %+v", p)
	}
	// after the window has passed, pushes go through again
	time.Sleep(3500 * time.Millisecond)
	assertContains(t, e.mustPush("origin", "main"), "forwarded")
	if e.remoteRef("refs/heads/main") != head {
		t.Fatal("REQ-PG-036: push after the window not forwarded")
	}
}

func TestREQ_PG_037_GreenPushDoesNotResetCount(t *testing.T) {
	e := setup(t, "RATE-YELLOW-STREAK: {max_yellow: 2}")
	yellow := func(i int) string {
		e.agent.Git("reset", "--quiet", "--hard", "origin/main")
		e.agent.Commit(fmt.Sprintf("deps %d", i), map[string]string{"package.json": fmt.Sprintf("{\"v\": %d}\n", i)})
		out, _ := e.push("origin", "main")
		return out
	}
	assertContains(t, yellow(1), "PATH-YELLOW")
	e.agent.Git("reset", "--quiet", "--hard", "origin/main")
	e.agent.Commit("harmless", map[string]string{"notes.txt": "x\n"})
	assertContains(t, e.mustPush("origin", "main"), "forwarded")
	e.agent.Git("fetch", "--quiet", "origin")
	assertContains(t, yellow(2), "PATH-YELLOW")
	// the third yellow exceeds max_yellow although a green push came in between
	out := yellow(3)
	assertContains(t, out, "waiting for a human")
	assertNotContains(t, out, "PATH-YELLOW", "RATE-YELLOW-STREAK")
	if ws := e.warnings(); len(ws) != 1 || ws[0].Kind != "yellow_streak" {
		t.Fatalf("REQ-PG-037: warnings %+v", ws)
	}
}

func TestREQ_PG_037_ApprovalEndsStreak(t *testing.T) {
	e := setup(t, "RATE-YELLOW-STREAK: {max_yellow: 1}")
	for i := 0; i < 2; i++ {
		e.agent.Git("reset", "--quiet", "--hard", "origin/main")
		e.agent.Commit(fmt.Sprintf("deps %d", i), map[string]string{"package.json": fmt.Sprintf("{\"v\": %d}\n", i)})
		e.mustReject("origin", "main")
	}
	// streak active: a harmless push is red without details
	e.agent.Git("reset", "--quiet", "--hard", "origin/main")
	harmless := e.agent.Commit("harmless", map[string]string{"notes.txt": "x\n"})
	out := e.mustReject("origin", "main")
	if ls := remoteLines(out); len(ls) != 1 || !regexp.MustCompile(`^rejected: waiting for a human \(push \S+\)$`).MatchString(ls[0]) {
		t.Fatalf("REQ-PG-037: response during streak %q", ls)
	}
	// a human approves one of its SHAs: that SHA goes through and the streak is over
	e.run("approve", "--config", e.config, repoName, "main", harmless[:10])
	assertContains(t, e.mustPush("origin", "main"), "forwarded")
	next := e.agent.Commit("next", map[string]string{"notes.txt": "y\n"})
	assertContains(t, e.mustPush("origin", "main"), "forwarded")
	if e.remoteRef("refs/heads/main") != next {
		t.Fatal("REQ-PG-037: streak still active after approve")
	}
}
