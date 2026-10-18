package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	// rejected inputs found by mutation testing
	if _, err := parseRaw([]byte("X" + ":000000 100644 0000000000000000000000000000000000000000 45b983be36b73c0788dc9cbcb76cbb80fc7bb057 "[1:] + "A\x00p\x00")); err == nil {
		t.Error("parseRaw: entry without ':' accepted")
	}
	for _, bad := range []string{"1\t2\x00", "5\t-\tp\x00", "-\t5\tp\x00"} {
		if _, err := parseNumstat([]byte(bad)); err == nil {
			t.Errorf("parseNumstat(%q) accepted", bad)
		}
	}
	if _, err := identTime("1700000000 +0000"); err == nil {
		t.Error("identTime: ident without '>' accepted")
	}
	for _, key := range []string{"author", "committer"} {
		_, err := ParseCommit("c", []byte(key+" a <b> notanumber +0000\n\nmsg"))
		var ne *strconv.NumError
		if !errors.As(err, &ne) {
			t.Errorf("ParseCommit %s: err = %v, want the parse error wrapped", key, err)
		}
	}
}

func TestREQ_PG_004_NormalizeFailsClosedAtEveryGitStep(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	const x = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	argsHave := func(s string) func(error) bool {
		return func(err error) bool { return strings.Contains(gitErrArgs(err), s) }
	}
	msgHas := func(subs ...string) func(error) bool {
		return func(err error) bool {
			for _, s := range subs {
				if err == nil || !strings.Contains(err.Error(), s) {
					return false
				}
			}
			return true
		}
	}
	numErr := func(err error) bool { var ne *strconv.NumError; return errors.As(err, &ne) }
	for _, c := range []struct {
		name, match string
		nth         int
		out         string
		fail        bool
		code        int
		check       func(error) bool
	}{
		{"empty tree", " hash-object ", 1, "", true, 128, argsHave(" hash-object ")},
		{"peel new", " rev-parse ", 1, "", true, 128, func(err error) bool {
			return strings.HasPrefix(err.Error(), main+": git rev-parse") && gitx.ExitCode(err) == 128
		}},
		{"peel old", " rev-parse ", 2, "", true, 128, func(err error) bool {
			return strings.HasPrefix(err.Error(), main+" (remote): git rev-parse") && gitx.ExitCode(err) == 128
		}},
		{"new missing", " rev-parse ", 1, "", true, 1, msgHas(main+": object ", "is missing or does not point to a commit")},
		{"old missing", " rev-parse ", 2, "", true, 1, msgHas(main+" (remote): object ", "is missing or does not point to a commit")},
		{"ancestry", "--is-ancestor", 1, "", true, 128, argsHave("--is-ancestor")},
		{"raw diff", "--raw", 1, "", true, 128, argsHave("--raw")},
		{"raw garbage", "--raw", 1, "garbage\x00", false, 0, msgHas("unexpected raw entry")},
		{"numstat", "--numstat", 1, "", true, 128, argsHave("--numstat")},
		{"numstat garbage", "--numstat", 1, "x\x00", false, 0, msgHas("malformed numstat")},
		{"file sizes", "--batch-check", 1, "", true, 128, argsHave(" cat-file ")},
		{"object sizes", "--batch-check", 2, "", true, 128, argsHave(" cat-file ")},
		{"sizes one field", "--batch-check", 1, "abc\n", false, 0, msgHas("unexpected")},
		{"sizes missing", "--batch-check", 1, x + " missing\n", false, 0, func(err error) bool {
			return err != nil && err.Error() == "object "+x+" missing"
		}},
		{"sizes not a number", "--batch-check", 1, x + " abc\n", false, 0, numErr},
		{"patch", " -p ", 1, "", true, 128, argsHave(" -p ")},
		{"commit list", " rev-list --stdin ", 1, "", true, 128, argsHave(" rev-list --stdin ")},
		{"object list", "--objects", 1, "", true, 128, argsHave("--objects")},
		{"commit batch", " cat-file --batch ", 1, "", true, 128, argsHave(" cat-file --batch ")},
		{"commit batch empty", " cat-file --batch ", 1, "", false, 0, func(err error) bool { return errors.Is(err, io.EOF) }},
		{"commit header short", " cat-file --batch ", 1, x + " commit\n", false, 0, msgHas("unexpected")},
		{"commit header tree", " cat-file --batch ", 1, x + " tree 5\nabcde\n", false, 0, msgHas("unexpected")},
		{"commit size", " cat-file --batch ", 1, x + " commit x\n\n", false, 0, numErr},
		{"commit body short", " cat-file --batch ", 1, x + " commit 100\nshort\n", false, 0, func(err error) bool {
			return errors.Is(err, io.ErrUnexpectedEOF)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := wrapGit(t, f.repo.Dir, c.match, c.nth, c.out, c.fail, c.code)
			_, err := Normalize(context.Background(), g, Input{Updates: []Update{push(main, head)},
				Remote: map[string]string{main: base}, DefaultBranch: main, Now: f.now})
			if err == nil || !c.check(err) {
				t.Fatalf("REQ-PG-004: err = %v", err)
			}
		})
	}
	// object sizes are summed as int64
	g := wrapGit(t, f.repo.Dir, "--batch-check", 2, x+" 5000000000000000000\n", false, 0)
	d, err := Normalize(context.Background(), g, Input{Updates: []Update{push(main, head)},
		Remote: map[string]string{main: base}, DefaultBranch: main, Now: f.now})
	if err != nil || d.Refs[0].NewBytes != 5000000000000000000 {
		t.Fatalf("NewBytes: %v, %v", d, err)
	}
}

// REQ-PG-004: when the diff base cannot be found (a default branch that does
// not peel, a failing merge-base), the push fails closed.
func TestREQ_PG_004_DiffBaseFailsClosed(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	run := func(g *gitx.Git, remote map[string]string, def string, u Update) (*Delta, error) {
		return Normalize(context.Background(), g, Input{Updates: []Update{u}, Remote: remote, DefaultBranch: def, Now: f.now})
	}
	plain := &gitx.Git{Dir: f.repo.Dir}
	// a default branch that is not a commit here, whether or not its oid is
	// among the remote's heads and tags
	for _, def := range []string{main, "refs/x/main"} {
		if _, err := run(plain, map[string]string{def: bogusOID}, def, push("refs/heads/new", head)); err == nil {
			t.Errorf("unpeelable default branch %s ignored", def)
		}
	}
	// a failing merge-base (not "no common ancestor")
	f.repo.Git("checkout", "-q", "-b", "side", base)
	side := f.repo.Commit("side", map[string]string{"s.txt": "s\n"})
	g := wrapGit(t, f.repo.Dir, " merge-base ", 2, "", true, 128)
	if _, err := run(g, map[string]string{main: head}, main, push(main, side)); !strings.Contains(gitErrArgs(err), " merge-base ") {
		t.Errorf("merge-base failure: %v", err)
	}
}

func TestREQ_PG_004_ScannerReportErrorsFailClosed(t *testing.T) {
	for name, c := range map[string]struct {
		report string
		check  func(error) bool
	}{
		"report removed": {"-", func(err error) bool { return errors.Is(err, fs.ErrNotExist) }},
		"malformed, exit 0": {"[{", func(err error) bool {
			var se *json.SyntaxError
			return errors.As(err, &se)
		}},
		"one byte": {"x", func(err error) bool { return err != nil && strings.Contains(err.Error(), "report") }},
	} {
		t.Run(name, func(t *testing.T) {
			bin, _ := recScanner(t, c.report, 0)
			_, err, _, _ := scanWith(t, bin)
			if !c.check(err) {
				t.Fatalf("REQ-PG-004: err = %v", err)
			}
		})
	}
}

func TestREQ_PG_004_ScannerStartErrorWrapped(t *testing.T) {
	_, err, _, _ := scanWith(t, filepath.Join(t.TempDir(), "missing", "gitleaks"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want the start error wrapped", err)
	}
}

func TestREQ_PG_004_ScannerTempFilesFailClosed(t *testing.T) {
	f := newReqFixture(t, "")
	// the report file cannot be created
	bin, _ := recScanner(t, "[]", 0)
	f = f.withRules("gitleaks: {path: '" + bin + "'}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
	if _, err := f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("report temp file: err = %v", err)
	}
	// the generated config or ignore file cannot be created
	if _, err := NewScanner(f.cfg, f.repo.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("generated config: err = %v", err)
	}
	testutil.WriteFiles(t, f.cfg.Dir, map[string]string{"gitleaks.toml": "# wall\n"})
	if _, err := NewScanner(f.cfg, f.repo.Dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("generated ignore file: err = %v", err)
	}
}

const mib = 1 << 20

// REQ-PG-049: an added line of exactly 16 MiB is evaluated; one byte more
// fails closed.
func TestREQ_PG_049_DiffLineLimit(t *testing.T) {
	for n, fail := range map[int]bool{16 * mib: false, 16*mib + 1: true} {
		f := newReqFixture(t, "")
		base := f.repo.Commit("base", nil)
		head := f.repo.Commit("big", map[string]string{"big.txt": strings.Repeat("a", n) + "\n"})
		_, err := f.evalErr(map[string]string{main: base}, Options{SkipScanner: true}, push(main, head))
		if (err != nil) != fail {
			t.Errorf("added line of %d bytes: err = %v, want failure %v", n, err, fail)
		}
	}
}

// sizedScanner is a gitleaks stand-in writing out bytes to stdout, errBytes
// to stderr and a report of reportBytes ("[]" padded with spaces). Its stdout
// starts with the "commits scanned" line. -1 for out writes without end.
func sizedScanner(t *testing.T, out, errBytes, reportBytes int) string {
	t.Helper()
	pad := func(n int, stream string) string {
		if n <= 0 {
			return ""
		}
		return fmt.Sprintf("head -c %d /dev/zero | tr '\\0' x %s; ", n, stream)
	}
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -r ] && r=$2; shift; done\n" +
		fmt.Sprintf("printf '[]' > \"$r\"; head -c %d /dev/zero | tr '\\0' ' ' >> \"$r\"\n", reportBytes-2)
	if out < 0 {
		script += "printf '" + strings.TrimSuffix(scanned, "\n") + "\\n'; yes\n"
	} else {
		script += "printf '" + strings.TrimSuffix(scanned, "\n") + "\\n'; " + pad(out-len(scanned), "") + pad(errBytes, ">&2") + "exit 0\n"
	}
	p := filepath.Join(t.TempDir(), "gitleaks")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// REQ-PG-050: gitleaks' stdout, stderr and report are read up to 1 MiB
// each; one byte more fails closed. Output without end fails closed quickly.
func TestREQ_PG_050_EvaluatedOutputBounded(t *testing.T) {
	for name, c := range map[string]struct{ out, errB, report int }{
		"stdout": {mib, 0, 2}, "stderr": {len(scanned), mib, 2}, "report": {len(scanned), 0, mib},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err, _, _ := scanWith(t, sizedScanner(t, c.out, c.errB, c.report)); err != nil {
				t.Errorf("exactly 1 MiB: %v", err)
			}
			over := c
			switch name {
			case "stdout":
				over.out++
			case "stderr":
				over.errB++
			default:
				over.report++
			}
			_, err, _, _ := scanWith(t, sizedScanner(t, over.out, over.errB, over.report))
			if err == nil || !strings.Contains(err.Error(), "1 MiB") {
				t.Errorf("1 MiB + 1: err = %v; want fail closed naming 1 MiB", err)
			}
		})
	}
	t.Run("without end", func(t *testing.T) {
		start := time.Now()
		_, err, _, _ := scanWith(t, sizedScanner(t, -1, 0, 2))
		if err == nil {
			t.Error("endless output passes")
		}
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("endless output took %v", d)
		}
	})
}

// REQ-PG-052: a SHA in messages is shortened to 12 characters, a program's
// error text to 300.
func TestREQ_PG_052_ShortenedTexts(t *testing.T) {
	if got := short(strings.Repeat("a", 12)); got != strings.Repeat("a", 12) {
		t.Errorf("12-character SHA: %q", got)
	}
	if got := short(strings.Repeat("b", 13)); got != strings.Repeat("b", 12) {
		t.Errorf("13-character SHA: %q", got)
	}
	if got := firstLine(strings.Repeat("e", 300)); got != strings.Repeat("e", 300) {
		t.Errorf("300-character error text cut to %d", len(got))
	}
	if got := firstLine(strings.Repeat("e", 301)); len(got) != 300 {
		t.Errorf("301-character error text kept %d", len(got))
	}
}
