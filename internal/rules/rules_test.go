package rules

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
)

func TestMain(m *testing.M) {
	cleanup, err := testutil.IsolateGit()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

const main = "refs/heads/main"

type fixture struct {
	t    *testing.T
	repo *testutil.Repo
	cfg  *config.Config
	now  time.Time
}

// newFixture creates a work-tree repo that plays both roles: the remote state
// is passed explicitly as a ref map, the pushed objects are in the repo.
func newFixture(t *testing.T, rulesYAML string) *fixture {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":          "agent: {name: test-agent}\n",
		"repos/demo/warden.yaml": "remote: /nonexistent/remote.git\n" + rulesYAML,
	})
	cfg, err := config.LoadRepo(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, repo: testutil.Init(t, false), cfg: cfg, now: time.Now()}
}

func (f *fixture) eval(remote map[string]string, updates ...Update) []Finding {
	f.t.Helper()
	res, err := f.evalErr(remote, Options{SkipScanner: true}, updates...)
	if err != nil {
		f.t.Fatal(err)
	}
	return res.Findings
}

func (f *fixture) evalErr(remote map[string]string, opt Options, updates ...Update) (*Result, error) {
	ctx := context.Background()
	g := &gitx.Git{Dir: f.repo.Dir}
	def := ""
	if _, ok := remote[main]; ok {
		def = main
	}
	d, err := Normalize(ctx, g, Input{Updates: updates, Remote: remote, DefaultBranch: def, Now: f.now})
	if err != nil {
		return nil, err
	}
	return Evaluate(ctx, g, f.cfg, d, opt)
}

func push(ref, sha string) Update { return Update{Ref: ref, New: sha} }

func find(fs []Finding, rule string) *Finding {
	for i := range fs {
		if fs[i].Rule == rule {
			return &fs[i]
		}
	}
	return nil
}

func expect(t *testing.T, fs []Finding, rule, path string) Finding {
	t.Helper()
	for _, f := range fs {
		if f.Rule == rule && (path == "" || f.Path == path) {
			return f
		}
	}
	t.Fatalf("no %s finding for %q in %v", rule, path, fs)
	return Finding{}
}

func expectNone(t *testing.T, fs []Finding, rules ...string) {
	t.Helper()
	for _, r := range rules {
		if f := find(fs, r); f != nil {
			t.Fatalf("unexpected %s finding: %v", r, *f)
		}
	}
}

func TestGreenFastForward(t *testing.T) {
	f := newFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"README.md": "hello\n"})
	head := f.repo.Commit("docs", map[string]string{"README.md": "hello\nworld\n", "src/main.go": "package main\n"})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	if len(fs) != 0 || Verdict(fs) != config.Green {
		t.Fatalf("expected green, got %v", fs)
	}
}

func TestRefKinds(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-DELETE: {allow: ['refs/heads/agent.*']}\n")
	a := f.repo.Commit("a", map[string]string{"a.txt": "a\n"})
	b := f.repo.Commit("b", map[string]string{"b.txt": "b\n"})
	f.repo.Git("checkout", "--quiet", "-b", "side", a)
	c := f.repo.Commit("c", map[string]string{"c.txt": "c\n"})
	f.repo.Git("tag", "-a", "-m", "release", "v1", b)
	tagObj := f.repo.Git("rev-parse", "v1")
	remote := map[string]string{main: b, "refs/heads/agent-1": a, "refs/heads/old": a, "refs/tags/v0": a}
	fs := f.eval(remote,
		push(main, c), // non-ff
		push("refs/heads/agent-1", gitx.ZeroOID),
		push("refs/heads/old", gitx.ZeroOID),
		push("refs/tags/v0", c),      // tag move
		push("refs/tags/v1", tagObj), // new annotated tag
		push("refs/notes/commits", c),
	)
	expect(t, fs, "REF-NON-FF", "")
	if d := expect(t, fs, "REF-DELETE", ""); d.Ref != "refs/heads/old" {
		t.Fatalf("REF-DELETE on %s", d.Ref)
	}
	if n := countRule(fs, "REF-DELETE"); n != 1 {
		t.Fatalf("allowed delete fired: %v", fs)
	}
	expect(t, fs, "REF-TAG-MOVE", "")
	if tg := expect(t, fs, "REF-TAG-NEW", ""); tg.Color != config.Yellow {
		t.Fatalf("REF-TAG-NEW color %s", tg.Color)
	}
	if ns := expect(t, fs, "REF-NAMESPACE", ""); ns.Ref != "refs/notes/commits" {
		t.Fatalf("REF-NAMESPACE on %s", ns.Ref)
	}
	if Verdict(fs) != config.Red {
		t.Fatalf("verdict %s", Verdict(fs))
	}
}

func countRule(fs []Finding, rule string) int {
	n := 0
	for _, f := range fs {
		if f.Rule == rule {
			n++
		}
	}
	return n
}

func TestAllowedRecordedForLease(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-NON-FF: {allow: ['refs/heads/agent/.*']}\n")
	a := f.repo.Commit("a", nil)
	b := f.repo.Commit("b", nil)
	f.repo.Git("checkout", "--quiet", "-b", "x", a)
	c := f.repo.Commit("c", nil)
	res, err := f.evalErr(map[string]string{"refs/heads/agent/x": b}, Options{SkipScanner: true}, push("refs/heads/agent/x", c))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 0 || !res.IsAllowed("REF-NON-FF", "refs/heads/agent/x") {
		t.Fatalf("findings %v allowed %v", res.Findings, res.Allowed)
	}
}

func TestNamespaceDenyProtectsBranch(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-NAMESPACE: {deny: ['refs/heads/main']}\n")
	a := f.repo.Commit("a", nil)
	b := f.repo.Commit("b", map[string]string{"x": "1\n"})
	fs := f.eval(map[string]string{main: a}, push(main, b))
	expect(t, fs, "REF-NAMESPACE", "")
	fs = f.eval(map[string]string{main: a}, push("refs/heads/feature", b))
	expectNone(t, fs, "REF-NAMESPACE")
}

func TestRefCount(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-COUNT: {max_refs: 2}\n")
	a := f.repo.Commit("a", nil)
	var ups []Update
	for i := 0; i < 3; i++ {
		ups = append(ups, push(fmt.Sprintf("refs/heads/b%d", i), a))
	}
	expect(t, f.eval(nil, ups...), "REF-COUNT", "")
}

func TestPathRules(t *testing.T) {
	f := newFixture(t, "rules:\n  PATH-YELLOW: {allow: ['package-lock\\.json']}\n")
	base := f.repo.Commit("base", map[string]string{"AGENTS.md": "rules\n", "README.md": "x\n"})
	f.repo.Git("mv", "AGENTS.md", "notes.md")
	head := f.repo.Commit("change", map[string]string{
		".github/workflows/ci.yml": "on: push\n",
		"web/package.json":         "{}\n",
		"package-lock.json":        "{}\n",
		"docs/claude.md":           "x\n",
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	expect(t, fs, "PATH-RED", ".github/workflows/ci.yml")
	expect(t, fs, "PATH-RED", "AGENTS.md") // rename checked under the old name
	expect(t, fs, "PATH-RED", "docs/claude.md")
	expect(t, fs, "PATH-YELLOW", "web/package.json")
	for _, x := range fs {
		if x.Path == "package-lock.json" || x.Path == "README.md" || x.Path == "notes.md" {
			t.Fatalf("unexpected finding %v", x)
		}
	}
}

func TestModeRules(t *testing.T) {
	f := newFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"run.txt": "x\n"})
	f.repo.WriteMode("run.txt", "x\n", 0o755)
	if err := os.Symlink("run.txt", f.repo.Dir+"/link"); err != nil {
		t.Fatal(err)
	}
	f.repo.Git("add", "-A")
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+base+",vendor/lib")
	f.repo.Git("commit", "--quiet", "-m", "modes")
	fs := f.eval(map[string]string{main: base}, push(main, f.repo.Head()))
	expect(t, fs, "MODE-EXEC", "run.txt")
	expect(t, fs, "MODE-SYMLINK", "link")
	expect(t, fs, "MODE-SUBMODULE", "vendor/lib")
}

func TestContentRules(t *testing.T) {
	f := newFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	long64 := strings.Repeat("QUJD", 60) // 240 base64 characters
	head := f.repo.Commit("content", map[string]string{
		"allow.go":  "x := 1 // gitleaks:allow\n",
		"bidi.go":   "ok\nif admin \u202e{ }\n",
		"bom.txt":   "\ufeffstarts with a BOM\n",
		"bom2.txt":  "line\nmid\ufeffdle\n",
		"zw.txt":    "a\u200bb\n",
		"blob.txt":  "data = " + long64 + "\n",
		"hex.txt":   strings.Repeat("ab", 60) + "\n",
		"bin.dat":   "\x00\x01\x02binary",
		"latin.txt": "caf\xe9\n",
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	expect(t, fs, "CONTENT-SCANNER-ALLOW", "allow.go")
	if b := expect(t, fs, "CONTENT-INVISIBLE", "bidi.go"); b.Line != 2 || b.Color != config.Red {
		t.Fatalf("bidi finding %v", b)
	}
	expect(t, fs, "CONTENT-INVISIBLE", "bom2.txt")
	expect(t, fs, "CONTENT-INVISIBLE", "zw.txt")
	expect(t, fs, "CONTENT-BLOB", "blob.txt")
	if h := expect(t, fs, "CONTENT-BLOB", "hex.txt"); !strings.Contains(h.Message, "hex") {
		t.Fatalf("hex finding %v", h)
	}
	expect(t, fs, "CONTENT-BINARY", "bin.dat")
	expect(t, fs, "CONTENT-BINARY", "latin.txt")
	for _, x := range fs {
		if x.Path == "bom.txt" {
			t.Fatalf("BOM at line 1 col 0 flagged: %v", x)
		}
	}
}

func TestContentBinaryAllow(t *testing.T) {
	f := newFixture(t, "rules:\n  CONTENT-BINARY: {allow: ['docs/.*\\.png']}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("img", map[string]string{"docs/a.png": "\x89PNG\x00\x00", "b.png": "\x89PNG\x00\x00"})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	expect(t, fs, "CONTENT-BINARY", "b.png")
	if countRule(fs, "CONTENT-BINARY") != 1 {
		t.Fatalf("allow ignored: %v", fs)
	}
}

func TestPagesScript(t *testing.T) {
	f := newFixture(t, "")
	f.repo.Git("checkout", "--quiet", "-b", "gh-pages")
	base := f.repo.Commit("site", map[string]string{"index.html": `<script src="https://cdn.known.example/a.js"></script>` + "\n"})
	head := f.repo.Commit("more", map[string]string{"page.html": `<script src="https://cdn.known.example/b.js"></script>
<script async src="https://evil.example/x.js"></script>
<link rel="stylesheet" href="//fonts.new.example/css">
<script src="/local.js"></script>
`})
	fs := f.eval(map[string]string{"refs/heads/gh-pages": base}, push("refs/heads/gh-pages", head))
	if n := countRule(fs, "CONTENT-PAGES-SCRIPT"); n != 2 {
		t.Fatalf("expected 2 hits, got %v", fs)
	}
	// the same change on another branch is not checked
	fs = f.eval(map[string]string{main: base}, push(main, head))
	expectNone(t, fs, "CONTENT-PAGES-SCRIPT")
}

func TestMetaUnsigned(t *testing.T) {
	f := newFixture(t, "rules:\n  META-UNSIGNED: {lookback: 3}\n  META-BACKDATED: {enabled: false}\n")
	f.repo.Commit("root", map[string]string{"a": "1\n"})
	tree := f.repo.Git("rev-parse", "HEAD^{tree}")
	s := ""
	for i := 0; i < 3; i++ {
		s = f.repo.FakeSigned(tree, s, fmt.Sprintf("signed %d", i))
	}
	f.repo.Git("reset", "--quiet", "--hard", s)
	head := f.repo.Commit("unsigned", map[string]string{"b": "2\n"})
	fs := f.eval(map[string]string{main: s}, push(main, head))
	if u := expect(t, fs, "META-UNSIGNED", ""); u.Commit != head {
		t.Fatalf("finding on %s", u.Commit)
	}
	// with fewer signed commits than lookback, the rule stays quiet
	f2 := newFixture(t, "rules:\n  META-UNSIGNED: {lookback: 5}\n  META-BACKDATED: {enabled: false}\n")
	f2.repo.Commit("root", nil)
	tree2 := f2.repo.Git("rev-parse", "HEAD^{tree}")
	s2 := f2.repo.FakeSigned(tree2, "", "signed")
	f2.repo.Git("reset", "--quiet", "--hard", s2)
	head2 := f2.repo.Commit("unsigned", map[string]string{"b": "2\n"})
	expectNone(t, f2.eval(map[string]string{main: s2}, push(main, head2)), "META-UNSIGNED")
}

func commitAt(t *testing.T, r *testutil.Repo, msg string, author, committer time.Time) string {
	t.Helper()
	r.Env = []string{"GIT_AUTHOR_DATE=" + author.Format(time.RFC3339), "GIT_COMMITTER_DATE=" + committer.Format(time.RFC3339)}
	defer func() { r.Env = nil }()
	return r.Commit(msg, map[string]string{msg + ".txt": msg + "\n"})
}

func TestMetaDates(t *testing.T) {
	f := newFixture(t, "")
	now := f.now
	base := commitAt(t, f.repo, "base", now.Add(-time.Hour), now.Add(-time.Hour))
	old := commitAt(t, f.repo, "old", now.Add(-72*time.Hour), now.Add(-48*time.Hour)) // backdated, and before its parent
	fut := commitAt(t, f.repo, "future", now.Add(time.Hour), now)
	rebased := commitAt(t, f.repo, "rebased", now.Add(-90*24*time.Hour), now) // old author date is fine
	fs := f.eval(map[string]string{main: base}, push(main, rebased))
	var backdated, future []string
	for _, x := range fs {
		switch x.Rule {
		case "META-BACKDATED":
			backdated = append(backdated, x.Commit)
		case "META-FUTURE":
			future = append(future, x.Commit)
		}
	}
	if len(backdated) != 2 || backdated[0] != old || backdated[1] != old {
		t.Fatalf("META-BACKDATED: %v (want twice %s)", backdated, old)
	}
	if len(future) != 1 || future[0] != fut {
		t.Fatalf("META-FUTURE: %v", future)
	}
}

func TestSizeRules(t *testing.T) {
	f := newFixture(t, `rules:
  SIZE-LIMIT: {max_files: 1000, max_lines: 1000, max_bytes: 100000000, max_file_bytes: 5000}
  SIZE-LARGE: {max_files: 3, max_lines: 50, max_commits: 2, max_file_bytes: 1000}
  SIZE-MASS-DELETE: {max_deleted_files: 2, max_deleted_share: 50, min_lines: 10}
`)
	files := map[string]string{"long.txt": strings.Repeat("line\n", 20)}
	for i := 0; i < 3; i++ {
		files[fmt.Sprintf("gone%d.txt", i)] = "x\n"
	}
	base := f.repo.Commit("base", files)
	for i := 0; i < 3; i++ {
		f.repo.Git("rm", "--quiet", fmt.Sprintf("gone%d.txt", i))
	}
	f.repo.Commit("one", map[string]string{"long.txt": strings.Repeat("line\n", 5), "big.txt": strings.Repeat("y", 2000)})
	f.repo.Commit("two", map[string]string{"huge.txt": strings.Repeat("z", 6000)})
	head := f.repo.Commit("three", map[string]string{"n.txt": strings.Repeat("n\n", 60)})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	expect(t, fs, "SIZE-LIMIT", "huge.txt")
	expect(t, fs, "SIZE-LARGE", "big.txt")
	var large []string
	for _, x := range fs {
		if x.Rule == "SIZE-LARGE" && x.Path == "" {
			large = append(large, x.Message)
		}
	}
	if len(large) != 3 { // files, lines, commits
		t.Fatalf("SIZE-LARGE totals: %v", large)
	}
	expect(t, fs, "SIZE-MASS-DELETE", "long.txt")
	if d := expect(t, fs, "SIZE-MASS-DELETE", ""); !strings.Contains(d.Message, "3 files deleted") {
		t.Fatalf("mass delete: %v", d)
	}
}

func TestCreateDiffsAgainstDefaultBranch(t *testing.T) {
	f := newFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"package.json": "{}\n"})
	head := f.repo.Commit("feature", map[string]string{"src/a.go": "package a\n"})
	fs := f.eval(map[string]string{main: base}, push("refs/heads/feature", head))
	if len(fs) != 0 {
		t.Fatalf("new branch diffed against more than its merge base: %v", fs)
	}
	// without a known default branch, the whole tree counts
	fs = f.eval(nil, push("refs/heads/feature", head))
	expect(t, fs, "PATH-YELLOW", "package.json")
}

func TestMissingObjectFailsClosed(t *testing.T) {
	f := newFixture(t, "")
	f.repo.Commit("base", nil)
	_, err := f.evalErr(nil, Options{SkipScanner: true}, push(main, strings.Repeat("1", 40)))
	if err == nil {
		t.Fatal("missing object did not fail")
	}
}

func TestMissingScannerFailsClosed(t *testing.T) {
	f := newFixture(t, "gitleaks: {path: /nonexistent/gitleaks}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if _, err := f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head)); err == nil {
		t.Fatal("missing scanner did not fail")
	}
}

func TestContentSecret(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		if os.Getenv("GITLEAKS_REQUIRED") != "" {
			t.Fatal("gitleaks not on PATH but GITLEAKS_REQUIRED is set")
		}
		t.Skip("gitleaks not on PATH")
	}
	f := newFixture(t, "")
	base := f.repo.Commit("base", nil)
	f.repo.Commit("add key", map[string]string{"cfg.ini": "aws_access_key_id = " + testutil.FakeAWSKey() + "\n"})
	head := f.repo.Commit("remove key", map[string]string{"cfg.ini": "aws_access_key_id = from-env\n"})
	f.repo.Write(".gitleaksignore", "") // the repo's own scanner files are ignored
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	res, err := f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head))
	if err != nil {
		t.Fatal(err)
	}
	if s := expect(t, res.Findings, "CONTENT-SECRET", "cfg.ini"); s.Color != config.Red || s.Ref != main {
		t.Fatalf("secret finding %v", s)
	}
}
