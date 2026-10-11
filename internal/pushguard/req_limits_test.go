package pushguard

// Requirement tests for the Push Guard limits REQ-PG-050 to 053
// (docs/push-guard-rules.md); see docs/push-guard-testspec.md.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/rules"
)

const mib = 1 << 20

// script writes an executable shell script named name and returns its path.
func script(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// versionAnswer is a script body printing first as the first line, padded
// with x to exactly n bytes.
func versionAnswer(first string, n int) string {
	return fmt.Sprintf("printf '%s\\n'; head -c %d /dev/zero | tr '\\0' x", first, n-len(first)-1)
}

// REQ-PG-050: `gitleaks version` and `git version` are read up to 1 MiB;
// one byte more fails the preflight.
func TestREQ_PG_050_EvaluatedOutputBounded(t *testing.T) {
	ctx := context.Background()
	if v, err := CheckGitleaks(ctx, script(t, "gitleaks", versionAnswer("8.30.1", mib))); err != nil || !strings.HasPrefix(v, "8.30.1") {
		t.Errorf("gitleaks version of 1 MiB: %q, %v", v, err)
	}
	if _, err := CheckGitleaks(ctx, script(t, "gitleaks", versionAnswer("8.30.1", mib+1))); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Errorf("gitleaks version of 1 MiB + 1: err = %v", err)
	}
	for n, fail := range map[int]bool{mib: false, mib + 1: true} {
		dir := filepath.Dir(script(t, "git", versionAnswer("git version 2.47.3", n)))
		t.Setenv("PATH", dir)
		_, v, err := gitx.CheckGit(ctx)
		if (err != nil) != fail || (fail && !strings.Contains(err.Error(), "1 MiB")) || (!fail && v != "2.47") {
			t.Errorf("git version of %d bytes: %q, %v; want failure %v", n, v, err, fail)
		}
	}
}

// REQ-PG-051: a notify.command that writes 3 MiB and fails is recorded with
// the end of its output.
func TestREQ_PG_051_NotifyOutputKeepsTail(t *testing.T) {
	bin := script(t, "notify", fmt.Sprintf("cat >/dev/null; printf 'FIRST-MARKER\\n'; head -c %d /dev/zero | tr '\\0' n; printf '\\nLAST-MARKER'; exit 4", 3*mib))
	cfg := &config.Config{NotifyCommand: []string{bin}, Timeout: time.Minute}
	err := runNotify(context.Background(), cfg, Warning{Kind: "red_push"})
	if err == nil || !strings.Contains(err.Error(), "LAST-MARKER") || strings.Contains(err.Error(), "FIRST-MARKER") {
		t.Errorf("err = %.200v; want the end of the output", err)
	}
	if err != nil && len(err.Error()) > 400 {
		t.Errorf("error text is %d bytes", len(err.Error()))
	}
	quiet := script(t, "notify", fmt.Sprintf("cat >/dev/null; head -c %d /dev/zero | tr '\\0' n; exit 0", 3*mib))
	if err := runNotify(context.Background(), &config.Config{NotifyCommand: []string{quiet}, Timeout: time.Minute}, Warning{}); err != nil {
		t.Errorf("notify writing 3 MiB and exiting 0: %v", err)
	}
}

// REQ-PG-052: fixed text lengths in the Push Guard.
func TestREQ_PG_052_ShortenedTexts(t *testing.T) {
	if got := shortSHA(strings.Repeat("a", 12)); len(got) != 12 {
		t.Errorf("12-character SHA: %q", got)
	}
	if got := shortSHA(strings.Repeat("a", 13)); len(got) != 12 {
		t.Errorf("13-character SHA: %q", got)
	}
	s72, s73 := strings.Repeat("s", 72), strings.Repeat("s", 73)
	if got := truncate(s72, replaySubject); got != s72 {
		t.Errorf("72-character subject: %q", got)
	}
	if got := truncate(s73, replaySubject); got != s72+"…" {
		t.Errorf("73-character subject: %q", got)
	}
	e300, e301 := strings.Repeat("e", 300), strings.Repeat("e", 301)
	if got := truncate(e300, maxErrorText); got != e300 {
		t.Errorf("300-character error text: %d", len(got))
	}
	if got := truncate(e301, maxErrorText); got != e300+"…" {
		t.Errorf("301-character error text: %d", len(got))
	}
	commits := func(n, msgLen int) []rules.Commit {
		var cs []rules.Commit
		for i := 0; i < n; i++ {
			cs = append(cs, rules.Commit{OID: fmt.Sprint(i), Message: strings.Repeat("m", msgLen)})
		}
		return cs
	}
	if u := quoteCommits(commits(20, 500)); len(u.Commits) != 20 || u.Commits[0].Message != strings.Repeat("m", 500) {
		t.Errorf("20 commits of 500: %d quoted", len(u.Commits))
	}
	if u := quoteCommits(commits(21, 501)); len(u.Commits) != 20 || u.Commits[0].Message != strings.Repeat("m", 500)+"…" {
		t.Errorf("21 commits of 501: %d quoted, message %d bytes", len(u.Commits), len(u.Commits[0].Message))
	}
	if u := quoteCommits(nil); u != nil {
		t.Errorf("no commits: %+v", u)
	}
}
