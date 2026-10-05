package rules

// Helpers for the requirement tests (req_*_test.go). Every requirement test is
// named TestREQ_PG_<nnn>_<what> after the requirement in
// docs/push-guard-rules.md it checks; docs/push-guard-testspec.md maps them.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
)

// newReqFixture is newFixture with a clock on whole seconds, so commit dates
// (which Git stores in seconds) can sit exactly on a rule's boundary.
func newReqFixture(t *testing.T, rulesYAML string) *fixture {
	t.Helper()
	f := newFixture(t, rulesYAML)
	f.now = time.Now().UTC().Truncate(time.Second)
	return f
}

// withRules returns a fixture on the same repository with another config.
func (f *fixture) withRules(rulesYAML string) *fixture {
	f.t.Helper()
	dir := f.t.TempDir()
	testutil.WriteFiles(f.t, dir, map[string]string{
		"defaults.yaml":          "agent: {name: test-agent}\n",
		"repos/demo/warden.yaml": "remote: /nonexistent/remote.git\n" + rulesYAML,
	})
	cfg, err := config.LoadRepo(dir, "demo")
	if err != nil {
		f.t.Fatal(err)
	}
	return &fixture{t: f.t, repo: f.repo, cfg: cfg, now: f.now}
}

// normalize runs Normalize the way evalErr does and returns the delta.
func (f *fixture) normalize(remote map[string]string, updates ...Update) *Delta {
	f.t.Helper()
	def := ""
	if _, ok := remote[main]; ok {
		def = main
	}
	d, err := Normalize(context.Background(), &gitx.Git{Dir: f.repo.Dir},
		Input{Updates: updates, Remote: remote, DefaultBranch: def, Now: f.now})
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

// rawCommit writes a commit object directly (no ref moves). Times are Unix
// seconds; signed adds a gpgsig header (presence is all the guard checks).
func rawCommit(t *testing.T, r *testutil.Repo, tree string, parents []string, author, committer int64, signed bool, msg string) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "tree %s\n", tree)
	for _, p := range parents {
		fmt.Fprintf(&b, "parent %s\n", p)
	}
	fmt.Fprintf(&b, "author Test Agent <agent@example.invalid> %d +0000\n", author)
	fmt.Fprintf(&b, "committer Test Agent <agent@example.invalid> %d +0000\n", committer)
	if signed {
		b.WriteString("gpgsig -----BEGIN PGP SIGNATURE-----\n \n fake\n -----END PGP SIGNATURE-----\n")
	}
	b.WriteString("\n" + msg + "\n")
	return hashObject(t, r, "commit", b.String(), false)
}

func hashObject(t *testing.T, r *testutil.Repo, kind, content string, literally bool) string {
	t.Helper()
	args := []string{"hash-object", "-t", kind, "-w", "--stdin"}
	if literally {
		args = append(args, "--literally")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// findings returns the findings of rule.
func findings(fs []Finding, rule string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

// paths returns the set of paths rule fired on.
func paths(fs []Finding, rule string) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings(fs, rule) {
		out[f.Path] = true
	}
	return out
}

// refs returns the set of refs rule fired on.
func refs(fs []Finding, rule string) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings(fs, rule) {
		out[f.Ref] = true
	}
	return out
}

// wantPaths checks that rule fired on exactly the paths in want among the
// candidates (want ⊆ candidates; every other candidate must stay quiet).
func wantPaths(t *testing.T, fs []Finding, rule string, candidates []string, want ...string) {
	t.Helper()
	got := paths(fs, rule)
	w := map[string]bool{}
	for _, p := range want {
		w[p] = true
	}
	for _, p := range candidates {
		if got[p] != w[p] {
			t.Errorf("%s on %q: fired=%v, want %v (all findings: %v)", rule, p, got[p], w[p], fs)
		}
	}
}

// expectColor checks the color of every finding of rule.
func expectColor(t *testing.T, fs []Finding, rule string, c config.Color) {
	t.Helper()
	for _, f := range findings(fs, rule) {
		if f.Color != c {
			t.Errorf("%s has color %s, want %s: %v", rule, f.Color, c, f)
		}
	}
}

// lines returns n lines "prefix<i>\n".
func lines(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "%s%d\n", prefix, i)
	}
	return b.String()
}
