package rules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/testutil"
)

func needGitleaks(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gitleaks"); err != nil {
		if os.Getenv("GITLEAKS_REQUIRED") != "" {
			t.Fatal("gitleaks not on PATH but GITLEAKS_REQUIRED is set")
		}
		t.Skip("gitleaks not on PATH")
	}
}

func (f *fixture) scan(remote map[string]string, updates ...Update) []Finding {
	f.t.Helper()
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		f.t.Fatal(err)
	}
	defer sc.Close()
	res, err := f.evalErr(remote, Options{Scanner: sc}, updates...)
	if err != nil {
		f.t.Fatal(err)
	}
	return res.Findings
}

// ---- CONTENT-SECRET ----

func TestREQ_PG_019_SecretOnlyInPushedRangeAndAttributedToRef(t *testing.T) {
	needGitleaks(t)
	f := newReqFixture(t, "")
	// a secret that is already on the remote is not part of the push
	base := f.repo.Commit("old secret", map[string]string{"old.ini": "aws_access_key_id = " + testutil.FakeAWSKey() + "\n"})
	clean := f.repo.Commit("clean", map[string]string{"a.txt": "a\n"})
	fs := f.scan(map[string]string{main: base}, push(main, clean))
	expectNone(t, fs, "CONTENT-SECRET")

	// a secret in a commit only the second ref brings is reported on that ref
	f.repo.Git("checkout", "--quiet", "-b", "feature")
	leak := f.repo.Commit("leak", map[string]string{"deploy.ini": "aws_access_key_id = " + testutil.FakeAWSKey() + "\n"})
	fs = f.scan(map[string]string{main: base}, push(main, clean), push("refs/heads/feature", leak))
	s := expect(t, fs, "CONTENT-SECRET", "deploy.ini")
	if s.Ref != "refs/heads/feature" || s.Commit != leak || s.Color != config.Red || s.Line != 1 {
		t.Fatalf("secret finding %v", s)
	}
	if strings.Contains(s.String(), "AKIA") {
		t.Fatalf("finding leaks the secret: %s", s)
	}
}

func TestREQ_PG_020_ScannerIgnoresRepoConfiguration(t *testing.T) {
	needGitleaks(t)
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	key := testutil.FakeAWSKey()
	head := f.repo.Commit("leak with exceptions", map[string]string{
		// the repo tries every way to silence the scanner
		".gitleaks.toml": "[extend]\nuseDefault = true\n[allowlist]\npaths = ['''.*''']\nregexes = ['''AKIA.*''']\n",
		"cfg.ini":        "aws_access_key_id = " + key + " # gitleaks:allow\n",
	})
	fs := f.scan(map[string]string{main: base}, push(main, head))
	s := expect(t, fs, "CONTENT-SECRET", "cfg.ini")

	// a .gitleaksignore committed in the push with the exact fingerprint is ignored too
	rule := strings.TrimSuffix(strings.TrimPrefix(s.Message, "secret scanner hit ("), ")")
	fp := fmt.Sprintf("%s:%s:%s:%d\n", s.Commit, s.Path, rule, s.Line)
	ign := f.repo.Commit("ignore it", map[string]string{".gitleaksignore": fp})
	if err := os.Remove(filepath.Join(f.repo.Dir, ".gitleaksignore")); err != nil {
		t.Fatal(err)
	}
	expect(t, f.scan(map[string]string{main: base}, push(main, ign)), "CONTENT-SECRET", "cfg.ini")

	// ...while the same fingerprint in the wall's gitleaksignore is honoured
	testutil.WriteFiles(t, f.cfg.RepoDir(), map[string]string{"gitleaksignore": fp})
	expectNone(t, f.scan(map[string]string{main: base}, push(main, ign)), "CONTENT-SECRET")
}

// REQ-PG-020: gitleaks also loads <source>/.gitleaksignore on its own, next to
// --gitleaks-ignore-path. A .gitleaksignore in the scanned directory (in
// production the guard repository's git dir) must fail closed, not be renamed
// aside (that races under concurrent scans).
func TestREQ_PG_020_ScannerIgnoresIgnoreFileInScannedDir(t *testing.T) {
	needGitleaks(t)
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("leak", map[string]string{"cfg.ini": "aws_access_key_id = " + testutil.FakeAWSKey() + "\n"})
	expect(t, f.scan(map[string]string{main: base}, push(main, head)), "CONTENT-SECRET", "cfg.ini")
	f.repo.Write(".gitleaksignore", "anything\n")
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	_, err = f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head))
	if err == nil || !strings.Contains(err.Error(), ".gitleaksignore") {
		t.Fatalf("REQ-PG-020: want fail-closed on scanned-dir .gitleaksignore, got %v", err)
	}
}

func TestREQ_PG_020_ScannerFilesComeFromTheWall(t *testing.T) {
	f := newReqFixture(t, "")
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	cfgFile, ignFile := sc.ConfigFile, sc.IgnoreFile
	if strings.HasPrefix(cfgFile, f.repo.Dir) || strings.HasPrefix(ignFile, f.repo.Dir) {
		t.Fatalf("scanner files inside the repo: %s %s", cfgFile, ignFile)
	}
	if data, _ := os.ReadFile(cfgFile); !strings.Contains(string(data), "useDefault = true") {
		t.Fatalf("generated config %q", data)
	}
	if data, _ := os.ReadFile(ignFile); len(data) != 0 {
		t.Fatalf("generated ignore file not empty: %q", data)
	}
	sc.Close()
	for _, p := range []string{cfgFile, ignFile} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("generated %s not removed: %v", p, err)
		}
	}

	// the wall's files are used, the repo folder's win over the wall's
	testutil.WriteFiles(t, f.cfg.Dir, map[string]string{"gitleaks.toml": "# wall\n", "gitleaksignore": "# wall\n"})
	sc, err = NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if sc.ConfigFile != filepath.Join(f.cfg.Dir, "gitleaks.toml") || sc.IgnoreFile != filepath.Join(f.cfg.Dir, "gitleaksignore") {
		t.Fatalf("wall files not used: %s %s", sc.ConfigFile, sc.IgnoreFile)
	}
	testutil.WriteFiles(t, f.cfg.RepoDir(), map[string]string{"gitleaks.toml": "# repo folder\n"})
	sc, err = NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if sc.ConfigFile != filepath.Join(f.cfg.RepoDir(), "gitleaks.toml") || sc.IgnoreFile != filepath.Join(f.cfg.Dir, "gitleaksignore") {
		t.Fatalf("repo folder files not preferred: %s %s", sc.ConfigFile, sc.IgnoreFile)
	}
	if sc.Bin != "gitleaks" {
		t.Fatalf("default binary %q", sc.Bin)
	}
}

// ---- CONTENT-SCANNER-ALLOW ----

func TestREQ_PG_021_ScannerAllowInAddedLinesAnyCase(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"old.go": "x := 1 // gitleaks:allow\n"})
	head := f.repo.Commit("allow", map[string]string{
		"old.go":   "x := 1 // gitleaks:allow\ny := 2\n", // the exception was already there
		"upper.go": "k := 1 // GITLEAKS:ALLOW\n",
		"tr.py":    "ok = 1\nk = 1  # trufflehog:ignore\n",
		"tr2.py":   "k = 1  # TruffleHog:Ignore\n",
		"plain.go": "gitleaks allow\n",
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-SCANNER-ALLOW", []string{"old.go", "upper.go", "tr.py", "tr2.py", "plain.go"}, "upper.go", "tr.py", "tr2.py")
	expectColor(t, fs, "CONTENT-SCANNER-ALLOW", config.Red)
	for _, x := range findings(fs, "CONTENT-SCANNER-ALLOW") {
		if x.Path == "tr.py" && x.Line != 2 {
			t.Fatalf("line of %v", x)
		}
	}
}

// ---- CONTENT-INVISIBLE ----

func TestREQ_PG_022_InvisibleCharacterRanges(t *testing.T) {
	fire := []rune{0x202A, 0x202B, 0x202C, 0x202D, 0x202E, 0x2066, 0x2067, 0x2068, 0x2069,
		0x200B, 0x200C, 0x200D, 0x2060, 0xE0000, 0xE0001, 0xE0041, 0xE007F}
	quiet := []rune{0x2029, 0x202F, 0x2065, 0x206A, 0x200A, 0x200E, 0x205F, 0x2061, 0xE0080, 0xDFFFF, 0x00E9, 0x2713}
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	files := map[string]string{}
	name := func(r rune) string { return fmt.Sprintf("u%04X.txt", r) }
	for _, r := range append(append([]rune{}, fire...), quiet...) {
		files[name(r)] = "ok\na" + string(r) + "b\n"
	}
	head := f.repo.Commit("chars", files)
	fs := f.eval(map[string]string{main: base}, push(main, head))
	got := paths(fs, "CONTENT-INVISIBLE")
	for _, r := range fire {
		if !got[name(r)] {
			t.Errorf("U+%04X not flagged", r)
		}
	}
	for _, r := range quiet {
		if got[name(r)] {
			t.Errorf("U+%04X flagged", r)
		}
	}
	expectColor(t, fs, "CONTENT-INVISIBLE", config.Red)
	for _, x := range findings(fs, "CONTENT-INVISIBLE") {
		if x.Line != 2 || !strings.Contains(x.Message, "U+") {
			t.Fatalf("finding %v", x)
		}
	}
}

func TestREQ_PG_022_ByteOrderMarkOnlyAllowedAtTheStart(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"mod.txt": "\ufefffirst\nsecond\n"})
	head := f.repo.Commit("bom", map[string]string{
		"l1c0.txt": "\ufeffstart\n",
		"l1c1.txt": "x\ufeff\n",
		"l2c0.txt": "x\n\ufeffy\n",
		"mod.txt":  "\ufefffirst\nsecond\n\ufeffthird\n", // BOM at the start of an added line 3
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-INVISIBLE", []string{"l1c0.txt", "l1c1.txt", "l2c0.txt", "mod.txt"}, "l1c1.txt", "l2c0.txt", "mod.txt")
}

// ---- CONTENT-BINARY ----

func TestREQ_PG_023_BinaryAddedOrChangedAlsoInTestDirs(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"tests/old.bin": "\x00old", "gone.bin": "\x00gone"})
	f.repo.Git("rm", "--quiet", "gone.bin")
	head := f.repo.Commit("binaries", map[string]string{
		"testdata/payload.xz":    "\xfd7zXZ\x00\x00\x04",
		"tests/fixtures/x.bin":   "\x00\x01\x02",
		"tests/old.bin":          "\x00new",
		"src/test/resources/a.o": "\x7fELF\x00",
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	cands := []string{"testdata/payload.xz", "tests/fixtures/x.bin", "tests/old.bin", "src/test/resources/a.o", "gone.bin"}
	wantPaths(t, fs, "CONTENT-BINARY", cands, cands[:4]...)
	expectColor(t, fs, "CONTENT-BINARY", config.Yellow)
}

func TestREQ_PG_024_InvalidUTF8InAddedLinesIsBinary(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"notes.txt": "ok\n", "old-latin.txt": "caf\xe9\n"})
	head := f.repo.Commit("text", map[string]string{
		"notes.txt":     "ok\ncaf\xe9\n",          // invalid byte in an added line
		"utf8.txt":      "café ✓ 日本\n",            // valid UTF-8 is text
		"old-latin.txt": "caf\xe9\nplain ascii\n", // the invalid line was already there
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-BINARY", []string{"notes.txt", "utf8.txt", "old-latin.txt"}, "notes.txt")
	if b := expect(t, fs, "CONTENT-BINARY", "notes.txt"); !strings.Contains(b.Message, "UTF-8") {
		t.Fatalf("message %q", b.Message)
	}
}

// ---- CONTENT-BLOB ----

func TestREQ_PG_025_BlobThresholds(t *testing.T) {
	f := newReqFixture(t, "")
	if b, _ := f.cfg.Rule("CONTENT-BLOB").Int("min_base64"); b != 200 {
		t.Fatalf("default min_base64 %d", b)
	}
	if h, _ := f.cfg.Rule("CONTENT-BLOB").Int("min_hex"); h != 100 {
		t.Fatalf("default min_hex %d", h)
	}
	b64 := func(n int) string { return strings.Repeat("QUJD", n/4+1)[:n] } // no run of hex digits
	hex := func(n int) string { return strings.Repeat("ab", n/2+1)[:n] }
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("blobs", map[string]string{
		"b64-199.txt": "k = " + b64(199) + "\n",
		"b64-200.txt": "k = " + b64(200) + "\n",
		"b64-sym.txt": "k = " + b64(196) + "+/=_\n", // 200 with base64 punctuation
		"hex-99.txt":  "k = " + hex(99) + "\n",
		"hex-100.txt": "k = " + hex(100) + "\n",
		"two.txt":     b64(150) + "\n" + b64(150) + "\n", // per line, not per file
		"gap.txt":     b64(150) + " " + b64(150) + "\n",
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	cands := []string{"b64-199.txt", "b64-200.txt", "b64-sym.txt", "hex-99.txt", "hex-100.txt", "two.txt", "gap.txt"}
	wantPaths(t, fs, "CONTENT-BLOB", cands, "b64-200.txt", "b64-sym.txt", "hex-100.txt")
	expectColor(t, fs, "CONTENT-BLOB", config.Yellow)
	for _, x := range findings(fs, "CONTENT-BLOB") {
		if x.Path == "hex-100.txt" && !strings.Contains(x.Message, "hex literal of 100 characters (limit 100)") {
			t.Errorf("hex message %q", x.Message)
		}
		if x.Path == "b64-200.txt" && !strings.Contains(x.Message, "base64 literal of 200 characters (limit 200)") {
			t.Errorf("base64 message %q", x.Message)
		}
	}
}

// ---- CONTENT-PAGES-SCRIPT ----

const newHostPage = `<script src="https://evil.example/x.js"></script>` + "\n"

func TestREQ_PG_026_PagesScriptOnConfiguredBranchOnly(t *testing.T) {
	f := newReqFixture(t, "pages_branch: site\n")
	base := f.repo.Commit("base", map[string]string{"index.html": "<p>hi</p>\n"})
	head := f.repo.Commit("page", map[string]string{"page.html": newHostPage + `<LINK REL=stylesheet HREF=HTTPS://Fonts.New.Example/css>` + "\n"})
	fs := f.eval(map[string]string{"refs/heads/site": base}, push("refs/heads/site", head))
	if n := countRule(fs, "CONTENT-PAGES-SCRIPT"); n != 2 {
		t.Fatalf("configured Pages branch: want 2 hits, got %v", fs)
	}
	expectColor(t, fs, "CONTENT-PAGES-SCRIPT", config.Yellow)
	if p := expect(t, fs, "CONTENT-PAGES-SCRIPT", "page.html"); p.Line == 0 {
		t.Fatalf("no line: %v", p)
	}
	// gh-pages is not the Pages branch any more
	expectNone(t, f.eval(map[string]string{"refs/heads/gh-pages": base}, push("refs/heads/gh-pages", head)), "CONTENT-PAGES-SCRIPT")
	// a new Pages branch has no old tree: every external host is new
	expect(t, f.eval(map[string]string{main: base}, push("refs/heads/site", head)), "CONTENT-PAGES-SCRIPT", "page.html")
}

func TestREQ_PG_027_PagesHostKnownFromOldTreeCaseInsensitive(t *testing.T) {
	f := newReqFixture(t, "")
	f.repo.Git("checkout", "--quiet", "-b", "gh-pages")
	base := f.repo.Commit("site", map[string]string{"README.md": "Assets come from CDN.Known.Example.\n"})
	f.repo.Git("checkout", "--quiet", "-b", "other")
	elsewhere := f.repo.Commit("other branch", map[string]string{"x.html": `<script src="https://elsewhere.example/a.js"></script>` + "\n"})
	f.repo.Git("checkout", "--quiet", "gh-pages")
	head := f.repo.Commit("more", map[string]string{"page.html": `<script src="https://cdn.known.example/a.js"></script>
<script src="https://elsewhere.example/a.js"></script>
`})
	remote := map[string]string{"refs/heads/gh-pages": base, "refs/heads/other": elsewhere}
	fs := f.eval(remote, push("refs/heads/gh-pages", head))
	hits := findings(fs, "CONTENT-PAGES-SCRIPT")
	if len(hits) != 1 || !strings.Contains(hits[0].Message, "elsewhere.example") {
		t.Fatalf("want only elsewhere.example (known only on another branch), got %v", fs)
	}
	// deleting the Pages branch is not checked by this rule
	expectNone(t, f.eval(remote, push("refs/heads/gh-pages", "0000000000000000000000000000000000000000")), "CONTENT-PAGES-SCRIPT")
}
