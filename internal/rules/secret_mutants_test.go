package rules

// gomutants survivor tests for secret.go (CONTENT-SECRET, REQ-PG-004/020).

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
)

// recScanner is a stand-in gitleaks that records its working directory,
// arguments and two environment variables in dir, then writes report to the
// -r file (or removes the file if report is "-") and exits with code.
func recScanner(t *testing.T, report string, code int) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"report": report})
	write := "cat '" + filepath.Join(dir, "report") + "' > \"$r\"\n"
	if report == "-" {
		write = "rm -f \"$r\"\n"
	}
	script := "#!/bin/sh\nd='" + dir + "'\npwd -P > \"$d/pwd\"\nprintf '%s\\n' \"$@\" > \"$d/args\"\n" +
		"printf '%s' \"${WARDEN_MUT_ENV-unset}\" > \"$d/env\"\n" +
		"while [ $# -gt 0 ]; do [ \"$1\" = -r ] && r=$2; shift; done\n" + write +
		"echo '1:00PM INF 1 commits scanned.'\nexit " + strconv.Itoa(code) + "\n"
	bin = filepath.Join(dir, "gitleaks")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// scanEval evaluates updates against remote with the scanner bin and git g.
func scanEval(t *testing.T, f *fixture, g *gitx.Git, remote map[string]string, updates ...Update) (*Result, error) {
	t.Helper()
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	def := ""
	if _, ok := remote[main]; ok {
		def = main
	}
	d, err := Normalize(context.Background(), g, Input{Updates: updates, Remote: remote, DefaultBranch: def, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	return Evaluate(context.Background(), g, f.cfg, d, Options{Scanner: sc})
}

func readRec(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("scanner did not record %s: %v", name, err)
	}
	return string(b)
}

// logOpts returns the --log-opts value the scanner was started with.
func logOpts(t *testing.T, dir string) string {
	t.Helper()
	for _, a := range strings.Split(readRec(t, dir, "args"), "\n") {
		if v, ok := strings.CutPrefix(a, "--log-opts="); ok {
			return v
		}
	}
	t.Fatal("no --log-opts argument")
	return ""
}

// The scanner runs in the repository with the guard's git environment, and
// scans exactly the new tips that carry new commits, excluding the remote.
func TestREQ_PG_004_ScannerInvocation(t *testing.T) {
	bin, dir := recScanner(t, "[]", 0)
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	g := &gitx.Git{Dir: f.repo.Dir, Env: []string{"WARDEN_MUT_ENV=from-guard"}}
	if _, err := scanEval(t, f, g, map[string]string{main: base}, push(main, head), push("refs/heads/old", base)); err != nil {
		t.Fatal(err)
	}
	if got := logOpts(t, dir); got != head+" --not "+base {
		t.Errorf("log-opts = %q, want only the tip with new commits, excluding the remote", got)
	}
	want, _ := filepath.EvalSymlinks(f.repo.Dir)
	if got := strings.TrimSpace(readRec(t, dir, "pwd")); got != want {
		t.Errorf("scanner ran in %q, want %q", got, want)
	}
	if got := readRec(t, dir, "env"); got != "from-guard" {
		t.Errorf("scanner env WARDEN_MUT_ENV = %q, want the guard's git environment", got)
	}
}

func TestREQ_PG_004_ScannerLogOptsWithoutRemote(t *testing.T) {
	bin, dir := recScanner(t, "[]", 0)
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	if _, err := scanEval(t, f, &gitx.Git{Dir: f.repo.Dir}, map[string]string{}, push(main, head)); err != nil {
		t.Fatal(err)
	}
	if got := logOpts(t, dir); got != head {
		t.Errorf("log-opts = %q, want %q (no --not for an empty remote)", got, head)
	}
}

// A push without new commits is not scanned at all.
func TestREQ_PG_004_NoNewCommitsNotScanned(t *testing.T) {
	bin, dir := recScanner(t, "[]", 2)
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	if _, err := scanEval(t, f, &gitx.Git{Dir: f.repo.Dir}, map[string]string{main: head}, push("refs/heads/x", head)); err != nil {
		t.Fatalf("push without new commits: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "args")); err == nil {
		t.Error("scanner ran for a push without new commits")
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

// Without wall files, NewScanner generates an empty ignore file and defaults
// the binary to gitleaks on PATH.
func TestREQ_PG_020_GeneratedScannerFiles(t *testing.T) {
	cfg := &config.Config{Dir: t.TempDir()}
	sc, err := NewScanner(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if sc.Bin != "gitleaks" {
		t.Errorf("Bin = %q", sc.Bin)
	}
	b, err := os.ReadFile(sc.IgnoreFile)
	if err != nil || len(b) != 0 {
		t.Errorf("ignore file %q: %q, %v; want an empty file", sc.IgnoreFile, b, err)
	}
	b, err = os.ReadFile(sc.ConfigFile)
	if err != nil || !strings.Contains(string(b), "useDefault = true") {
		t.Errorf("config file %q: %q, %v", sc.ConfigFile, b, err)
	}
}

func TestMutantsFirstLineTruncation(t *testing.T) {
	for in, want := range map[string]string{
		strings.Repeat("a", 300):        strings.Repeat("a", 300),
		strings.Repeat("b", 301):        strings.Repeat("b", 300),
		strings.Repeat("c", 302):        strings.Repeat("c", 300),
		"  first\nsecond":               "first",
		"\n\nonly\n":                    "only",
		strings.Repeat("d", 400) + "\n": strings.Repeat("d", 300),
	} {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%.20q…) = %d chars %.20q…, want %d chars", in, len(got), got, len(want))
		}
	}
}

// A .gitleaksignore that cannot be checked (here: the path is too long) fails
// closed, even if the scanner itself could run in the directory.
func TestREQ_PG_020_UncheckableDotGitleaksIgnoreFailsClosed(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 4080-200 {
		dir = filepath.Join(dir, strings.Repeat("d", 200))
	}
	dir = filepath.Join(dir, strings.Repeat("e", 4085-len(dir)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("cannot create a long path: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".gitleaksignore")); !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Skipf("Lstat of the long path: %v", err)
	}
	bin, _ := recScanner(t, "[]", 0)
	f := newReqFixture(t, "gitleaks: {path: '"+bin+"'}\n")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	sc.RepoDir = dir
	if _, err := f.evalErr(map[string]string{main: base}, Options{Scanner: sc}, push(main, head)); !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Fatalf("REQ-PG-020: err = %v, want ENAMETOOLONG", err)
	}
}
