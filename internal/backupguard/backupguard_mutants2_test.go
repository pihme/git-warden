package backupguard

// gomutants survivor tests for internal/backupguard, round 2.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp/syntax"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
)

// mut2FakeScript is a stand-in for git-everref driven by files in its own
// directory: add-<branch> makes an add naming origin/<branch> fail with that
// file as output; run-out is printed by run --all, which then sleeps
// run-sleep seconds or exits run-exit.
const mut2FakeScript = `#!/bin/sh
echo "$*" >> "@DIR@/calls.log"
dir=$2
case " $* " in *" --version "*) echo "everref version v1.0.0"; exit 0;; esac
case " $* " in *" tags "*) git -C "$dir" config --add everref-remote.backup.tags origin; exit $?;; esac
case " $* " in *" add "*)
	for a in "$@"; do case "$a" in origin/*) if [ -f "@DIR@/add-${a#origin/}" ]; then cat "@DIR@/add-${a#origin/}"; exit 1; fi;; esac; done
	for a in "$@"; do case "$a" in origin/*) git -C "$dir" config "everref.remotes/$a.remote" backup || exit 2;; esac; done
	exit 0;;
esac
case " $* " in *" run --all "*)
	if [ -f "@DIR@/run-out" ]; then cat "@DIR@/run-out"; fi
	if [ -f "@DIR@/run-sleep" ]; then exec sleep "$(cat "@DIR@/run-sleep")"; fi
	if [ -f "@DIR@/run-exit" ]; then exit "$(cat "@DIR@/run-exit")"; fi
	exit 0;;
esac
exit 0
`

type mut2Fake struct{ dir, bin string }

func newMut2Fake(t *testing.T) mut2Fake {
	t.Helper()
	f := mut2Fake{dir: t.TempDir()}
	f.bin = filepath.Join(f.dir, "git-everref")
	if err := os.WriteFile(f.bin, []byte(strings.ReplaceAll(mut2FakeScript, "@DIR@", f.dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// set writes the control file name; an empty content removes it.
func (f mut2Fake) set(t *testing.T, name, content string) {
	t.Helper()
	p := filepath.Join(f.dir, name)
	if content == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f mut2Fake) calls(t *testing.T) []string { return qaLines(t, filepath.Join(f.dir, "calls.log")) }

func (f mut2Fake) config(extra string) string {
	return "everref:\n  path: " + f.bin + "\n  version: 1.0.0\n" + extra
}

func fakeProg(t *testing.T, body string) string {
	t.Helper()
	return filepath.Join(qaFakeProgram(t, "prog", body), "prog")
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply to root")
	}
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// ---------- everref.go ----------

func TestMutantsEverrefConstantsAndMajorVersion(t *testing.T) {
	if EverrefOK != 0 || EverrefRefError != 1 || EverrefUsage != 2 {
		t.Errorf("exit codes %d %d %d, want 0 1 2 (documented by git-everref)", EverrefOK, EverrefRefError, EverrefUsage)
	}
	if n, ok := majorVersion("abc"); n != 0 || ok {
		t.Errorf("majorVersion(abc) = %d, %v", n, ok)
	}
	if n, ok := majorVersion("v2.1.0"); n != 2 || !ok {
		t.Errorf("majorVersion(v2.1.0) = %d, %v", n, ok)
	}
}

func TestMutantsFindEverrefMessages(t *testing.T) {
	ctx := context.Background()
	if _, _, err := FindEverref(ctx, "git-everref-not-installed-anywhere", ""); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("missing binary: %v", err)
	}
	long := strings.Repeat("x", 301)
	_, _, err := FindEverref(ctx, fakeProg(t, "echo "+long+"; exit 3"), "")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 || !strings.HasSuffix(err.Error(), long[:300]+"…") {
		t.Errorf("failing --version: %v", err)
	}
	long = strings.Repeat("y", 301)
	if _, _, err := FindEverref(ctx, fakeProg(t, "echo "+long), ""); err == nil || !strings.Contains(err.Error(), `"`+long[:300]+`…"`) {
		t.Errorf("unexpected output: %v", err)
	}
	dev := fakeProg(t, "echo everref version dev")
	if path, v, err := FindEverref(ctx, dev, DevVersion); err != nil || path != dev || v != DevVersion {
		t.Errorf("dev build with version dev: %q %q %v", path, v, err)
	}
	if _, _, err := FindEverref(ctx, fakeProg(t, "echo everref version v1.0.0"), DevVersion); err == nil || !strings.Contains(err.Error(), "requires a source build (dev)") {
		t.Errorf("release with version dev: %v", err)
	}
	if _, _, err := FindEverref(ctx, fakeProg(t, "echo everref version abc"), ""); err == nil || !strings.Contains(err.Error(), `can't read a major version from "abc"`) {
		t.Errorf("version abc: %v", err)
	}
}

func TestMutantsEverrefRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var re *RunError
	_, err := (&Everref{Bin: missingEverref(t)}).Run(ctx, dir, "run")
	if !errors.As(err, &re) || re.ExitCode != -1 {
		t.Errorf("missing binary: %#v", err)
	}
	// a failure keeps the output (run --all events are still counted)
	out, err := (&Everref{Bin: fakeProg(t, "echo '[new] x'; exit 3")}).Run(ctx, dir, "run", "--all")
	var ee *exec.ExitError
	if out != "[new] x\n" || !errors.As(err, &re) || re.ExitCode != 3 || !errors.As(err, &ee) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("failing run: %q %v", out, err)
	}
	tctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err = (&Everref{Bin: fakeProg(t, "exec sleep 10")}).Run(tctx, dir, "run")
	if !errors.As(err, &re) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout: %v", err)
	}
}

// A child that keeps the output pipe open must not hold up the guard beyond
// the WaitDelay.
func TestMutantsWaitDelay(t *testing.T) {
	ctx := context.Background()
	limit := 15 * time.Second
	t.Run("FindEverref", func(t *testing.T) {
		t.Parallel()
		bin := fakeProg(t, "echo everref version v1.0.0; sleep 20 & exit 0")
		start := time.Now()
		_, _, _ = FindEverref(ctx, bin, "")
		if d := time.Since(start); d > limit {
			t.Errorf("FindEverref took %v", d)
		}
	})
	t.Run("Run", func(t *testing.T) {
		t.Parallel()
		ev := &Everref{Bin: fakeProg(t, "sleep 20 & exit 0")}
		start := time.Now()
		_, _ = ev.Run(ctx, t.TempDir(), "run")
		if d := time.Since(start); d > limit {
			t.Errorf("Run took %v", d)
		}
	})
	t.Run("notify", func(t *testing.T) {
		t.Parallel()
		g := &Guard{Defaults: &Defaults{NotifyCommand: []string{fakeProg(t, "sleep 20 & exit 0")}}}
		start := time.Now()
		_ = g.notify(ctx, &Result{Repo: "r"})
		if d := time.Since(start); d > limit {
			t.Errorf("notify took %v", d)
		}
	})
}

// ---------- bridge.go ----------

func TestMutantsTailAndTruncate(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"ab\ncd", 4, "…cd"},
		{"abc\n", 2, "…c\n"},
		{"a\nbc", 3, "…bc"},
		{"ab\nc", 3, "…c"},
		{"\x80\x80\x80", 2, "…"},
		{"xé", 1, "…"},
	} {
		if got := tail(c.s, c.n); got != c.want {
			t.Errorf("tail(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("truncate(abc, 3) = %q", got)
	}
}

func TestMutantsProtectedBranchesSkipsEmptyName(t *testing.T) {
	w := testutil.Init(t, false)
	w.Git("config", "everref.remotes/origin/.remote", "backup")
	w.Git("config", "everref.remotes/origin/main.remote", "backup")
	got, err := protectedBranches(context.Background(), w.Dir)
	if err != nil || len(got) != 1 || !got["main"] {
		t.Errorf("protectedBranches = %v, %v", got, err)
	}
}

func TestMutantsStatePermissions(t *testing.T) {
	ctx := context.Background()
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("run: %+v", res)
	}
	sd := g.Defaults.StateDir
	for path, want := range map[string]fs.FileMode{
		filepath.Join(sd, "locks"):            0o700,
		lockPath(sd, "r"):                     0o600,
		filepath.Join(sd, "repos", "r"):       0o700,
		filepath.Join(sd, "backup.jsonl"):     0o600,
		filepath.Join(sd, "repos", "r", ".."): 0o700,
	} {
		if got := modeOf(t, path); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
	// a failed preflight creates the state directory itself
	sd = filepath.Join(t.TempDir(), "new-state")
	dir = writeConfig(t, "everref:\n  path: "+missingEverref(t)+"\nstate_dir: "+sd+"\n", map[string]string{"r": "remote: " + bare + "\n"})
	if _, _, err := PreflightRun(ctx, dir, nil); err == nil {
		t.Fatal("preflight without everref passed")
	}
	if got := modeOf(t, sd); got != 0o700 {
		t.Errorf("state dir mode %o", got)
	}
	if got := modeOf(t, filepath.Join(sd, "backup.jsonl")); got != 0o600 {
		t.Errorf("backup.jsonl mode %o", got)
	}
}

func TestMutantsUnreadableStateDirs(t *testing.T) {
	skipIfRoot(t)
	ctx := context.Background()
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	base := filepath.Join(g.Defaults.StateDir, "repos", "r")
	if err := os.Chmod(base, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o700) })
	if res := g.RunRepo(ctx, "r"); res.OK || !strings.Contains(res.Error, "permission denied") {
		t.Errorf("run with an unreadable repo state directory: %+v", res)
	}

	// createOnce: a directory without marker that can't be listed
	b := t.TempDir()
	target := filepath.Join(b, "r")
	if err := os.Mkdir(target, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(target, 0o700) })
	built := false
	if err := createOnce(b, target, "HEAD", func(string) error { built = true; return nil }); !errors.Is(err, os.ErrPermission) || built {
		t.Errorf("unlistable dir: err %v, built %v", err, built)
	}
}

// Every value of everref-remote.backup.tags counts, not only the last one.
func TestMutantsTagsCheckedAgainstEveryValue(t *testing.T) {
	ctx := context.Background()
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	if _, err := qaGitIn(t, BridgePath(g.Defaults.StateDir, "r"), "config", "--add", "everref-remote.backup.tags", "other"); err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("second run: %+v", res)
	}
	if n := f.count(t, " tags "); n != 1 {
		t.Errorf("tags called %d times, want 1:\n%s", n, strings.Join(f.calls(t), "\n"))
	}
}

func TestMutantsAddFailureReasonsAndOutput(t *testing.T) {
	ctx := context.Background()
	f := newMut2Fake(t)
	bare, _ := remoteWith(t, "main", "bad1", "bad2")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	z301, z299 := strings.Repeat("z", 301), strings.Repeat("z", 299)
	f.set(t, "add-bad1", "a1\n"+z301)
	f.set(t, "add-bad2", "first\n"+z299)
	res := g.RunRepo(ctx, "r")
	if res.OK || res.AddFailed["bad1"] != "…"+z301[1:] || res.AddFailed["bad2"] != z299 || len(res.AddFailed) != 2 {
		t.Errorf("AddFailed = %q", res.AddFailed)
	}
	if want := "a1\n" + z301 + "\nfirst\n" + z299; res.Output != want {
		t.Errorf("Output = %q, want %q", res.Output, want)
	}
	// the output kept is the last 40 lines and at most 4000 bytes
	f.set(t, "add-bad2", "")
	var lines []string
	for i := 1; i <= 41; i++ {
		lines = append(lines, "l"+string(rune('0'+i/10))+string(rune('0'+i%10)))
	}
	f.set(t, "add-bad1", strings.Join(lines, "\n")+"\n")
	if res := g.RunRepo(ctx, "r"); res.Output != strings.Join(lines[1:], "\n") {
		t.Errorf("41 lines: Output = %q", res.Output)
	}
	w := strings.Repeat("w", 4001)
	f.set(t, "add-bad1", w[:4000])
	if res := g.RunRepo(ctx, "r"); res.Output != w[:4000] {
		t.Errorf("4000 bytes: Output has %d bytes, starts %q", len(res.Output), res.Output[:min(5, len(res.Output))])
	}
	f.set(t, "add-bad1", w)
	if res := g.RunRepo(ctx, "r"); res.Output != "…"+w[:4000] {
		t.Errorf("4001 bytes: Output has %d bytes, starts %q", len(res.Output), res.Output[:min(5, len(res.Output))])
	}
}

func TestMutantsBranchesSortedAndFailuresListed(t *testing.T) {
	ctx := context.Background()
	f := newMut2Fake(t)
	// more than 8 refs: Go iterates small maps in insertion order (rotated)
	bare, _ := remoteWith(t, "main", "b1", "b2", "b3", "bad1", "bad2", "bad3", "bad4", "c1", "c2", "c3", "c4")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	// 9 failures: the names come from a map too
	for _, b := range []string{"b3", "bad1", "bad2", "bad3", "bad4", "c1", "c2", "c3", "c4"} {
		f.set(t, "add-"+b, "refused\n")
	}
	res := g.RunRepo(ctx, "r")
	if want := "9 branch(es) could not be protected: b3, bad1, bad2, bad3, bad4, c1, c2, c3, c4"; res.Error != want {
		t.Errorf("Error = %q, want %q", res.Error, want)
	}
	var first string
	for _, c := range f.calls(t) {
		if strings.Contains(c, " add ") {
			first = c
			break
		}
	}
	if !strings.HasSuffix(first, " -q add origin/b1 origin/b2 origin/b3 origin/bad1 origin/bad2 origin/bad3 origin/bad4 origin/c1 origin/c2 origin/c3 origin/c4 origin/main --remote backup") {
		t.Errorf("first add call: %q", first)
	}
}

func TestMutantsRunOutputLimitsAndTimeout(t *testing.T) {
	ctx := context.Background()
	f := newMut2Fake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	v := strings.Repeat("v", 4001)
	f.set(t, "run-exit", "1")
	f.set(t, "run-out", v[:4000])
	res := g.RunRepo(ctx, "r")
	if res.OK || res.EverrefExit != 1 || res.Output != v[:4000] || strings.Contains(res.Error, "timeout") {
		t.Errorf("4000 bytes: exit %d, %d bytes, error %q", res.EverrefExit, len(res.Output), res.Error)
	}
	f.set(t, "run-out", v)
	if res := g.RunRepo(ctx, "r"); res.Output != "…"+v[:4000] {
		t.Errorf("4001 bytes: %d bytes, starts %q", len(res.Output), res.Output[:min(5, len(res.Output))])
	}
	// a timeout says so and keeps everref's partial output
	f.set(t, "run-exit", "")
	f.set(t, "run-out", "partial\n")
	f.set(t, "run-sleep", "10")
	dir = writeConfig(t, f.config("timeout: 2s\n"), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ = qaGuard(t, dir)
	res = g.RunRepo(ctx, "r")
	if res.OK || !strings.HasPrefix(res.Error, "timeout 2s (defaults.yaml) exceeded, killed: ") || res.Output != "partial" {
		t.Errorf("timeout: error %q, output %q", res.Error, res.Output)
	}
}

func TestMutantsNotifyArgsAndFailure(t *testing.T) {
	ctx := context.Background()
	args := filepath.Join(t.TempDir(), "args")
	g := &Guard{Defaults: &Defaults{NotifyCommand: []string{fakeProg(t, `printf '%s' "$*" > `+args+`; cat > /dev/null`), "--flag", "x y"}}}
	if err := g.notify(ctx, &Result{Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := qaRead(t, args); got != "--flag x y" {
		t.Errorf("notify args %q", got)
	}
	long := strings.Repeat("n", 301)
	g.Defaults.NotifyCommand = []string{fakeProg(t, "echo "+long+"; exit 3")}
	err := g.notify(ctx, &Result{Repo: "r"})
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 || !strings.HasSuffix(err.Error(), long[:300]+"…") {
		t.Errorf("failing notify: %v", err)
	}
}

func TestMutantsAppendJournalOnDirectory(t *testing.T) {
	sd := t.TempDir()
	if err := os.Mkdir(filepath.Join(sd, "backup.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	g := &Guard{Defaults: &Defaults{StateDir: sd}}
	if err := g.appendJournal(&Result{Repo: "r"}); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("appendJournal: %v", err)
	}
}

// ---------- check.go ----------

func TestREQ_BG_097_CheckConfigLines(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"a": "remote: " + bare + "\n", "b": "remote: " + bare + "\n"})
	var out strings.Builder
	if err := CheckConfig(ctx, dir, false, &out); err != nil {
		t.Fatalf("%v\n%s", err, &out)
	}
	sd := filepath.Join(dir, "state")
	for _, want := range []string{
		"note: everref.version is not set; the default 1.0.0 applies (git-everref must report major version 1)\n",
		"repo a: " + bare + " -> " + BackupPath(sd, "a") + "\n",
		"repo b: " + bare + " -> " + BackupPath(sd, "b") + "\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
	// every repo's missing credential is a problem, whatever the map order
	// (a two-entry map starts with its first key 7 times in 8, hence 64 repos)
	repos := map[string]string{}
	for i := range 64 {
		n := fmt.Sprintf("r%02d", i)
		repos[n] = "remote: ssh://git@example.invalid/" + n + ".git\ncredential: /nonexistent/key-" + n + "\n"
	}
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: 1.0.0\n", repos)
	out.Reset()
	if err := CheckConfig(ctx, dir, false, &out); err == nil || err.Error() != "64 problem(s) in the configuration" {
		t.Errorf("64 missing credentials: %v\n%s", err, &out)
	}
}

// ---------- config.go ----------

func TestMutantsLoadDefaultsErrors(t *testing.T) {
	if DefaultTimeout != 30*time.Minute {
		t.Errorf("DefaultTimeout = %v", DefaultTimeout)
	}
	d, err := LoadDefaults(t.TempDir())
	if err != nil || d.Timeout != 30*time.Minute {
		t.Errorf("defaults: %+v %v", d, err)
	}
	if _, err := LoadDefaults(""); err == nil {
		t.Error("empty configuration directory accepted")
	}
	if _, err := LoadDefaults(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing directory: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefaults(file); err == nil || err.Error() != "configuration directory "+file+" is not a directory" {
		t.Errorf("file as directory: %v", err)
	}
	dir := writeConfig(t, "bogus: 1\n", nil)
	var te *yaml.TypeError
	if _, err := LoadDefaults(dir); !errors.As(err, &te) {
		t.Errorf("unknown key: %v", err)
	}
}

func TestMutantsReposAndLoadRepo(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{"b": "remote: /x\n"})
	if err := os.WriteFile(filepath.Join(dir, "repos", "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Repos(dir); err != nil || strings.Join(got, ",") != "b" {
		t.Errorf("Repos = %v, %v", got, err)
	}
	dir = writeConfig(t, "", map[string]string{"r": "remote: /x\nexclude_branches: ['(']\n"})
	var se *syntax.Error
	if _, err := LoadRepo(dir, "r"); !errors.As(err, &se) {
		t.Errorf("bad exclude_branches: %v", err)
	}
}

// With the working directory gone, a relative configuration directory can't
// be made absolute: that error is reported as it is.
func TestMutantsRelativeConfigDirWithoutWorkingDir(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("getwd works in a removed directory here")
	}
	if _, err := LoadDefaults("cfg"); err == nil || strings.HasPrefix(err.Error(), "configuration directory") {
		t.Errorf("LoadDefaults: %v", err)
	}
	if _, err := LoadRepo("cfg", "r"); err == nil || errors.Is(err, ErrUnknownRepo) {
		t.Errorf("LoadRepo: %v", err)
	}
}

// ---------- run.go ----------

func TestMutantsPreflightProblemCount(t *testing.T) {
	ctx := context.Background()
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "everref:\n  path: "+missingEverref(t)+"\n", map[string]string{"r": "remote: " + bare + "\n"})
	if _, _, err := Preflight(ctx, dir, nil); err == nil || !strings.HasPrefix(err.Error(), "everref not found") {
		t.Errorf("one problem: %v", err)
	}
	// two problems are still recorded by PreflightRun
	dir = writeConfig(t, "everref:\n  path: "+missingEverref(t)+"\n", nil)
	if _, _, err := PreflightRun(ctx, dir, nil); err == nil || !strings.HasPrefix(err.Error(), "2 problems: ") {
		t.Fatalf("two problems: %v", err)
	}
	j := readJournal(t, filepath.Join(dir, "state"))
	if len(j) != 1 || !j[0].Preflight || !strings.HasPrefix(j[0].Error, "preflight: 2 problems: ") {
		t.Errorf("journal: %+v", j)
	}
}

func TestMutantsRunAllOutput(t *testing.T) {
	ctx := context.Background()
	f := newMut2Fake(t)
	f.set(t, "run-out", "[new] a\n[new] b\n[new lineage] c\n[deleted] d\n[re-tagged] e\n")
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	var out strings.Builder
	g.Out = &out
	if err := g.RunAll(ctx, []string{"r", "x"}); err == nil || err.Error() != "backup failed for x" {
		t.Errorf("RunAll: %v", err)
	}
	for _, want := range []string{"repo r: ok: 1 branches, 2 new, 1 rewritten, 1 deleted, 1 re-tagged\n", "repo x: FAILED: unknown repo"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
	if j := readJournal(t, g.Defaults.StateDir); len(j) != 2 || j[0].DurationMS <= 0 {
		t.Errorf("journal: %+v", j)
	}
}

func TestMutantsListRefsErrorIsWrapped(t *testing.T) {
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + filepath.Join(t.TempDir(), "missing.git") + "\n"})
	g, _ := qaGuard(t, dir)
	err := g.runRepo(context.Background(), "r", &Result{})
	var ge *gitx.Error
	if !errors.As(err, &ge) || !strings.HasPrefix(err.Error(), "list remote refs: ") {
		t.Errorf("runRepo: %v", err)
	}
}
