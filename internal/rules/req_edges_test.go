package rules

// Edge cases that the main requirement tests leave out: an empty Pages branch,
// scanner set-up and report handling, cat-file output the guard does not
// expect, and diff header paths. See docs/push-guard-testspec.md.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/gitx"
)

// An empty pages_branch switches CONTENT-PAGES-SCRIPT off, even for gh-pages.
func TestREQ_PG_026_EmptyPagesBranchDisablesRule(t *testing.T) {
	f := newReqFixture(t, "pages_branch: ''\n")
	if f.cfg.PagesBranch != "" {
		t.Fatalf("pages_branch = %q, want empty", f.cfg.PagesBranch)
	}
	base := f.repo.Commit("base", map[string]string{"index.html": "<p>hi</p>\n"})
	head := f.repo.Commit("cdn", map[string]string{"index.html": "<script src=\"https://cdn.evil.example/x.js\"></script>\n"})
	expectNone(t, f.eval(map[string]string{"refs/heads/gh-pages": base}, push("refs/heads/gh-pages", head)), "CONTENT-PAGES-SCRIPT")
}

// A .gitleaksignore directory in the scanned directory fails closed too.
func TestREQ_PG_020_DotGitleaksIgnoreDirectoryFailsClosed(t *testing.T) {
	bin := fakeScanner(t, scanned, "[]", 0)
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	if err := os.Mkdir(filepath.Join(f.repo.Dir, ".gitleaksignore"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	_, err = f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head))
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("REQ-PG-020: err = %v, want refusal for a .gitleaksignore directory", err)
	}
}

// Scanner output in an error message is cut to its first line, at most 300 bytes.
func TestREQ_PG_004_ScannerErrorMessageIsShortened(t *testing.T) {
	long := strings.Repeat("x", 1000)
	_, err, _, _ := scanWith(t, fakeScanner(t, long+"\nsecond line\n", "[]", 2))
	if err == nil {
		t.Fatal("REQ-PG-004: scanner exit 2 passed")
	}
	msg := err.Error()
	if !strings.Contains(msg, strings.Repeat("x", 300)) || strings.Contains(msg, strings.Repeat("x", 301)) || strings.Contains(msg, "second line") {
		t.Fatalf("REQ-PG-004: message not cut to 300 bytes of the first line: %d bytes", len(msg))
	}
}

// The same leak reported twice gives one finding; a leak in a commit the push
// does not contain is still reported, on the first pushed ref.
func TestREQ_PG_019_ScannerReportDuplicatesAndUnknownCommit(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report")
	if err := os.WriteFile(report, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	unknown := strings.Repeat("ab", 20)
	leaks := `[{"RuleID":"r","File":"x.txt","StartLine":1,"Commit":"` + head + `"},` +
		`{"RuleID":"r","File":"x.txt","StartLine":1,"Commit":"` + head + `"},` +
		`{"RuleID":"r","File":"y.txt","StartLine":2,"Commit":"` + unknown + `"}]`
	bin := fakeScanner(t, scanned, leaks, 99)
	f = f.withRules("gitleaks: {path: '" + bin + "'}\n")
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	res, err := f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head))
	if err != nil {
		t.Fatal(err)
	}
	fs := findings(res.Findings, "CONTENT-SECRET")
	if len(fs) != 2 {
		t.Fatalf("REQ-PG-019: %d CONTENT-SECRET findings, want 2 (duplicate merged): %+v", len(fs), fs)
	}
	for _, x := range fs {
		if x.Ref != main {
			t.Errorf("REQ-PG-019: finding for %s on ref %q, want %s", x.Path, x.Ref, main)
		}
	}
}

// Without a configured path the scanner binary is gitleaks from PATH; a
// wall-only config never looks in the repos folder for scanner files.
func TestREQ_PG_020_ScannerDefaultsAndWallOnlyConfig(t *testing.T) {
	f := newReqFixture(t, "")
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if sc.Bin != "gitleaks" {
		t.Errorf("default scanner = %q, want gitleaks", sc.Bin)
	}

	wall := *f.cfg
	wall.RepoName = ""
	trap := filepath.Join(wall.Dir, "repos", "gitleaks.toml")
	if err := os.WriteFile(trap, []byte("# not a wall file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sc2, err := NewScanner(&wall, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc2.Close()
	if sc2.ConfigFile == trap {
		t.Errorf("wall-only config picked %s", trap)
	}
	wallToml := filepath.Join(wall.Dir, "gitleaks.toml")
	if err := os.WriteFile(wallToml, []byte("[extend]\nuseDefault = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sc3, err := NewScanner(&wall, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc3.Close()
	if sc3.ConfigFile != wallToml {
		t.Errorf("wall-only config file = %q, want %s", sc3.ConfigFile, wallToml)
	}
}

// Missing objects and cat-file output the guard cannot parse fail closed.
func TestREQ_PG_004_CatFileMissingOrUnexpectedFailsClosed(t *testing.T) {
	f := newReqFixture(t, "")
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	b := newBatch(&gitx.Git{Dir: f.repo.Dir})
	ctx := context.Background()
	if _, err := b.check(ctx, []string{head, strings.Repeat("0", 39) + "1"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("missing object: err = %v, want missing", err)
	}
	if _, err := b.check(ctx, []string{"no such object"}); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Errorf("odd cat-file line: err = %v, want unexpected", err)
	}
	if sizes, err := b.check(ctx, []string{head}); err != nil || sizes[head] == 0 {
		t.Errorf("existing commit: sizes = %v, err = %v", sizes, err)
	}
}

// Paths in "+++ " / "--- " diff headers: tab-terminated, C-quoted, /dev/null,
// and without a/ or b/ prefix.
func TestREQ_PG_021_DiffHeaderPath(t *testing.T) {
	for in, want := range map[string]string{
		"b/x.txt":                   "x.txt",
		"b/with space.txt\t":        "with space.txt",
		"b/x.txt\t2026-10-05 22:00": "x.txt",
		`"b/tab\tx.txt"`:            "tab\tx.txt",
		`"b/\303\244.txt"`:          "ä.txt",
		"a/old.txt":                 "old.txt",
		"b/a/x.txt":                 "a/x.txt",
		"/dev/null":                 "",
		"/dev/null\t":               "",
		"plain.txt":                 "plain.txt",
	} {
		if got := diffHeaderPath(in); got != want {
			t.Errorf("diffHeaderPath(%q) = %q, want %q", in, got, want)
		}
	}
}
