package rules

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
)

// fakeScanner writes a shell script standing in for gitleaks. It writes report
// to the -r file, prints log and exits with code.
func fakeScanner(t *testing.T, log, report string, code int) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"log": log, "report": report})
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -r ] && r=$2; shift; done\n" +
		"cat '" + filepath.Join(dir, "report") + "' > \"$r\"\ncat '" + filepath.Join(dir, "log") + "'\nexit " + strconv.Itoa(code) + "\n"
	p := filepath.Join(dir, "gitleaks")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// scanWith evaluates a one-commit push with the given scanner binary.
func scanWith(t *testing.T, bin string) ([]Finding, error, string, string) {
	t.Helper()
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	res, err := f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head), push("refs/heads/second", head))
	if err != nil {
		return nil, err, base, head
	}
	return res.Findings, nil, base, head
}

const scanned = "1:00PM INF 1 commits scanned.\n"

func TestREQ_PG_004_ScannerFailuresFailClosed(t *testing.T) {
	for name, c := range map[string]struct {
		log, report string
		code        int
		want        string
	}{
		"exit 2":             {scanned, "[]", 2, "exit 2"},
		"error in log":       {"1:00PM ERR fatal: bad object\n" + scanned, "[]", 0, "reported an error"},
		"no scan reported":   {"nothing to see\n", "[]", 0, "did not report a scan"},
		"leaks, empty":       {scanned, "", 99, "report is empty"},
		"leaks, empty array": {scanned, "[]", 99, "report is empty"},
		"malformed report":   {scanned, "[{", 99, "report"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err, _, _ := scanWith(t, fakeScanner(t, c.log, c.report, c.code))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("REQ-PG-004: err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestREQ_PG_004_ScannerTimeoutFailsClosed(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gitleaks")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	g := &gitx.Git{Dir: f.repo.Dir}
	d, err := Normalize(ctx, g, Input{Updates: []Update{push(main, head)}, Remote: map[string]string{main: base}, DefaultBranch: main, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := Evaluate(ctx, g, f.cfg, d, Options{Scanner: sc}); err == nil {
		t.Fatal("REQ-PG-004: a hanging scanner did not fail")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("REQ-PG-004: timeout took %s", time.Since(start))
	}
}

func TestREQ_PG_004_UnknownCommitInReportGoesToFirstRef(t *testing.T) {
	report := `[{"RuleID":"x","File":"x.txt","StartLine":1,"Commit":"` + strings.Repeat("f", 40) + `"}]`
	fs, err, _, _ := scanWith(t, fakeScanner(t, scanned, report, 99))
	if err != nil {
		t.Fatal(err)
	}
	s := expect(t, fs, "CONTENT-SECRET", "x.txt")
	if s.Ref != main || s.Color != config.Red {
		t.Fatalf("finding %v", s)
	}
}

func TestREQ_PG_004_NoScannerIsAnError(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	if _, err := f.evalErr(map[string]string{main: base}, Options{}, push(main, head)); err == nil {
		t.Fatal("REQ-PG-004: evaluation without a scanner did not fail")
	}
}

func TestREQ_PG_004_BadLimitsFailClosed(t *testing.T) {
	for name, yaml := range map[string]string{
		"max_refs float":    "rules:\n  REF-COUNT: {max_refs: 1.5}\n",
		"max_refs string":   "rules:\n  REF-COUNT: {max_refs: ten}\n",
		"max_age number":    "rules:\n  META-BACKDATED: {max_age: 5}\n",
		"max_skew garbage":  "rules:\n  META-FUTURE: {max_skew: soon}\n",
		"lookback string":   "rules:\n  META-UNSIGNED: {lookback: many}\n",
		"min_base64 float":  "rules:\n  CONTENT-BLOB: {min_base64: 2.5}\n",
		"max_files string":  "rules:\n  SIZE-LIMIT: {max_files: lots}\n",
		"max_commits float": "rules:\n  SIZE-LARGE: {max_commits: 0.5}\n",
		"min_lines string":  "rules:\n  SIZE-MASS-DELETE: {min_lines: x}\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteFiles(t, dir, map[string]string{
				"defaults.yaml":          "agent: {name: test-agent}\n",
				"repos/demo/warden.yaml": "remote: /nonexistent/remote.git\n" + yaml,
			})
			cfg, err := config.LoadRepo(dir, "demo")
			if err != nil {
				return // rejected at load: fails closed before any push is checked
			}
			f := newReqFixture(t, "")
			f.cfg = cfg
			base := f.repo.Commit("base", map[string]string{"a.txt": lines("a", 3)})
			f.repo.Git("checkout", "--quiet", "-b", "feature")
			head := f.repo.Commit("x", map[string]string{"a.txt": "b\n"})
			if _, err := f.evalErr(map[string]string{main: base}, Options{SkipScanner: true}, push(main, head)); err == nil {
				t.Fatalf("REQ-PG-004: bad limit accepted at load and at evaluation")
			}
		})
	}
}

func TestREQ_PG_004_BadObjectsFailClosed(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	tree := f.repo.Git("rev-parse", "HEAD^{tree}")
	blob := f.repo.Git("rev-parse", "HEAD:a.txt")
	missing := strings.Repeat("1", 40)
	bad := hashObject(t, f.repo, "commit", "tree "+tree+"\nparent "+base+
		"\nauthor A <a@b> notanumber +0000\ncommitter A <a@b> 1 +0000\n\nbad\n", true)
	noTime := hashObject(t, f.repo, "commit", "tree "+tree+"\nparent "+base+
		"\nauthor A <a@b> 1 +0000\ncommitter A <a@b>\n\nbad\n", true)
	for name, c := range map[string]struct {
		remote map[string]string
		u      Update
	}{
		"tree pushed to a branch": {map[string]string{main: base}, push(main, tree)},
		"blob pushed to a branch": {map[string]string{main: base}, push(main, blob)},
		"missing pushed object":   {map[string]string{main: base}, push(main, missing)},
		"missing remote object":   {map[string]string{main: missing}, push(main, base)},
		"malformed author date":   {map[string]string{main: base}, push(main, bad)},
		"missing committer date":  {map[string]string{main: base}, push(main, noTime)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.evalErr(c.remote, Options{SkipScanner: true}, c.u); err == nil {
				t.Fatalf("REQ-PG-004: %s accepted", name)
			}
		})
	}
}

func TestREQ_PG_004_Parsers(t *testing.T) {
	for name, raw := range map[string]string{
		"no colon":         "junk\x00a.txt\x00",
		"too few fields":   ":100644 100644 abc M\x00a.txt\x00",
		"missing path":     ":100644 100644 abc def M",
		"rename, one name": ":100644 100644 abc def R100\x00a.txt",
	} {
		if _, err := parseRaw([]byte(raw)); err == nil {
			t.Errorf("parseRaw %s: no error", name)
		}
	}
	fs, err := parseRaw([]byte(":100644 100644 abc def C075\x00a.txt\x00b.txt\x00"))
	if err != nil || len(fs) != 1 || fs[0].Status != 'A' || fs[0].Path != "b.txt" || fs[0].OldPath != "" {
		t.Errorf("parseRaw copy: %+v, %v (a copy counts as an added file)", fs, err)
	}

	for name, raw := range map[string]string{
		"two fields":     "1\ta.txt\x00",
		"not a number":   "x\t1\ta.txt\x00",
		"rename, no new": "1\t1\t\x00a.txt",
	} {
		if _, err := parseNumstat([]byte(raw)); err == nil {
			t.Errorf("parseNumstat %s: no error", name)
		}
	}
	st, err := parseNumstat([]byte("-\t-\tbin\x002\t3\t\x00old\x00new\x00"))
	if err != nil || !st["bin"].binary || st["new"].added != 2 || st["new"].deleted != 3 {
		t.Errorf("parseNumstat: %+v, %v", st, err)
	}

	for h, want := range map[string]int{
		"@@ -1,2 +3,4 @@": 3, "@@ -1 +7 @@": 7, "@@ -0,0 +1 @@ ctx": 1, "@@ nonsense": 0,
	} {
		if got := hunkStart(h); got != want {
			t.Errorf("hunkStart(%q) = %d, want %d", h, got, want)
		}
	}

	for _, ident := range []string{"no brackets 12 +0000", "A <a@b>", "A <a@b> soon +0000"} {
		if _, err := identTime(ident); err == nil {
			t.Errorf("identTime(%q): no error", ident)
		}
	}
	if ts, err := identTime("A <a> b> 1700000000 +0100"); err != nil || ts.Unix() != 1700000000 {
		t.Errorf("identTime with '>' in the name: %v, %v", ts, err)
	}

	c, err := ParseCommit("x", []byte("tree t\nparent p1\nparent p2\nauthor A <a> 1 +0000\ncommitter C <c> 2 +0000\ngpgsig-sha256 -----BEGIN\n sig\n\nmsg\n"))
	if err != nil || len(c.Parents) != 2 || !c.Signed || c.Author.Unix() != 1 || c.Committer.Unix() != 2 || c.Message != "msg\n" {
		t.Errorf("ParseCommit: %+v, %v", c, err)
	}
	for _, raw := range []string{"author A <a> x +0000\n\nm", "committer C <c> x +0000\n\nm"} {
		if _, err := ParseCommit("x", []byte(raw)); err == nil {
			t.Errorf("ParseCommit(%q): no error", raw)
		}
	}
	// the signature only counts as a header, not in the message
	if c, _ := ParseCommit("x", []byte("tree t\nauthor A <a> 1 +0000\ncommitter C <c> 2 +0000\n\ngpgsig fake\n")); c.Signed {
		t.Error("ParseCommit: gpgsig in the message counted as a signature")
	}

	if got := firstLine("  one\ntwo\n"); got != "one" {
		t.Errorf("firstLine = %q", got)
	}
}
