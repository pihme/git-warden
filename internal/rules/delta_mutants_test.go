package rules

// gomutants survivor tests for delta.go: parsers, Normalize details and the
// fail-closed paths of every git step (via a git wrapper that fails or fakes
// one command).

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/gitx"
)

// wrapGit returns a git for dir that runs the real git, except for the nth
// command whose arguments contain match: that one prints out and exits 0,
// or, if fail is set, exits with code.
func wrapGit(t *testing.T, dir, match string, nth int, out string, fail bool, code int) *gitx.Git {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	act := "cat '" + filepath.Join(tmp, "out") + "'; exit 0"
	if fail {
		act = "echo 'fatal: injected' >&2; exit " + strconv.Itoa(code)
	}
	if err := os.WriteFile(filepath.Join(tmp, "out"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	cnt := filepath.Join(tmp, "count")
	script := "#!/bin/sh\ncase \" $* \" in\n*'" + match + "'*)\n" +
		"  n=$(cat '" + cnt + "' 2>/dev/null || echo 0); n=$((n+1)); echo $n > '" + cnt + "'\n" +
		"  if [ $n -eq " + strconv.Itoa(nth) + " ]; then cat >/dev/null; " + act + "; fi;;\nesac\n" +
		"exec '" + real + "' \"$@\"\n"
	bin := filepath.Join(tmp, "git")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &gitx.Git{Dir: dir, Bin: bin}
}

func gitErrArgs(err error) string {
	var ge *gitx.Error
	if errors.As(err, &ge) {
		return " " + strings.Join(ge.Args, " ") + " "
	}
	return ""
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

func TestMutantsNormalizeDetails(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	// the same new commit on two refs is listed once
	d := f.normalize(map[string]string{main: base}, push(main, head), push("refs/heads/b", head))
	if len(d.Commits) != 1 {
		t.Errorf("Commits = %d, want 1", len(d.Commits))
	}
	if d.Refs[0].Base != base {
		t.Errorf("FF base = %q, want the old commit", d.Refs[0].Base)
	}
	// a non-fast-forward diffs against the merge base
	f.repo.Git("checkout", "-q", "-b", "side", base)
	side := f.repo.Commit("side", map[string]string{"s.txt": "s\n"})
	d = f.normalize(map[string]string{main: head}, push(main, side))
	if rd := d.Refs[0]; rd.Kind != NonFF || len(rd.Files) != 1 || rd.Files[0].Path != "s.txt" || rd.Base != base {
		t.Errorf("non-ff: kind %s base %s files %v", rd.Kind, rd.Base, rd.Files)
	}
	normalize := func(remote map[string]string, def string, u Update) (*Delta, error) {
		return Normalize(context.Background(), &gitx.Git{Dir: f.repo.Dir}, Input{Updates: []Update{u}, Remote: remote, DefaultBranch: def, Now: f.now})
	}
	// a new ref with a default branch the remote does not have: empty-tree base
	d, err := normalize(map[string]string{}, main, push("refs/heads/new", head))
	if err != nil || d.Refs[0].Base != d.EmptyTree {
		t.Errorf("create without remote default: %v %v", d, err)
	}
	// ...and with a default branch that is not a commit here: fail closed
	if _, err := normalize(map[string]string{main: bogusOID}, main, push("refs/heads/new", head)); err == nil {
		t.Error("unpeelable default branch ignored")
	}
}

// Submodule entries point at commits of another repository: not sized.
func TestMutantsSubmoduleNotSized(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+bogusOID+",sub")
	f.repo.Git("commit", "-q", "-m", "submodule")
	d := f.normalize(map[string]string{main: base}, push(main, f.repo.Head()))
	if fs := d.Refs[0].Files; len(fs) != 1 || fs[0].NewMode != "160000" || fs[0].NewSize != 0 {
		t.Fatalf("files %v", fs)
	}
}

func TestMutantsAddedLinesLongAndPlusLines(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	long := strings.Repeat("y", 100*1024)
	head := f.repo.Commit("x", map[string]string{"f.txt": "++ not a header\n" + long + "\n"})
	got := f.normalize(map[string]string{main: base}, push(main, head)).Refs[0].Added
	want := map[string][]Line{"f.txt": {{No: 1, Text: "++ not a header"}, {No: 2, Text: long}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("added lines: %d paths, f.txt has %d lines", len(got), len(got["f.txt"]))
	}
}

func TestMutantsPathsAndRemoteOIDs(t *testing.T) {
	for _, c := range []struct {
		fc   FileChange
		want []string
	}{
		{FileChange{Path: "a"}, []string{"a"}},
		{FileChange{Path: "a", OldPath: "a"}, []string{"a"}},
		{FileChange{Path: "a", OldPath: "o"}, []string{"o", "a"}},
	} {
		if got := c.fc.Paths(); !slices.Equal(got, c.want) {
			t.Errorf("Paths(%+v) = %q, want %q", c.fc, got, c.want)
		}
	}
	got := RemoteOIDs(map[string]string{"refs/heads/a": "b1", "refs/heads/b": "b1", "refs/tags/t": "t1",
		"refs/notes/n": "n1", "HEAD": "h1"})
	if !slices.Equal(got, []string{"b1", "t1"}) {
		t.Errorf("RemoteOIDs = %q", got)
	}
	if got := revInput([]string{"a"}, nil); got != "a\n" {
		t.Errorf("revInput without excludes = %q", got)
	}
}

func TestMutantsParseRaw(t *testing.T) {
	const h = ":000000 100644 0000000000000000000000000000000000000000 45b983be36b73c0788dc9cbcb76cbb80fc7bb057 "
	fs, err := parseRaw([]byte("\x00" + h + "A\x00p\x00\x00" + h + "R100\x00old\x00new"))
	if err != nil || len(fs) != 2 || fs[0].Path != "p" || fs[1].OldPath != "old" || fs[1].Path != "new" {
		t.Fatalf("parseRaw = %+v, %v", fs, err)
	}
	if fs, err := parseRaw([]byte(h + "A\x00p")); err != nil || len(fs) != 1 {
		t.Errorf("entry without trailing NUL: %v, %v", fs, err)
	}
	if _, err := parseRaw([]byte("X" + h[1:] + "A\x00p\x00")); err == nil {
		t.Error("entry without ':' accepted")
	}
}

func TestMutantsParseNumstat(t *testing.T) {
	got, err := parseNumstat([]byte("\x001\t2\tp\x00-\t-\tbin\x003\t4\t\x00old\x00new"))
	want := map[string]numstat{"p": {1, 2, false}, "bin": {0, 0, true}, "new": {3, 4, false}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNumstat = %v, %v", got, err)
	}
	for _, bad := range []string{"1\t2\x00", "5\t-\tp\x00", "-\t5\tp\x00"} {
		if _, err := parseNumstat([]byte(bad)); err == nil {
			t.Errorf("parseNumstat(%q) accepted", bad)
		}
	}
}

func TestMutantsHeaderAndHunkParsing(t *testing.T) {
	if got := diffHeaderPath("\tfoo"); got != "" {
		t.Errorf("diffHeaderPath(tab first) = %q", got)
	}
	for h, want := range map[string]int{" +5": 5, "x5": 0, "@@ -1 ++5 @@": 5, "@@ -0,0 +5": 5, "@@ -1,2 +7,3 @@": 7} {
		if got := hunkStart(h); got != want {
			t.Errorf("hunkStart(%q) = %d, want %d", h, got, want)
		}
	}
}

func TestMutantsIdentTimeAndParseCommit(t *testing.T) {
	for ident, want := range map[string]int64{
		"> 5 +0000":                       5,
		"a <b> 1700000000":                1700000000,
		"a <b> 5000000000000000000 +0000": 5000000000000000000,
	} {
		if got, err := identTime(ident); err != nil || got.Unix() != want {
			t.Errorf("identTime(%q) = %v, %v", ident, got, err)
		}
	}
	if _, err := identTime("1700000000 +0000"); err == nil {
		t.Error("ident without '>' accepted")
	}
	for _, key := range []string{"author", "committer"} {
		_, err := ParseCommit("c", []byte(key+" a <b> notanumber +0000\n\nmsg"))
		var ne *strconv.NumError
		if !errors.As(err, &ne) {
			t.Errorf("%s: err = %v, want the parse error wrapped", key, err)
		}
	}
}

func TestMutantsNormalizeRound2(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	head := f.repo.Commit("x", map[string]string{"x.txt": "x\n", "y.txt": "y\ny\n"})
	run := func(g *gitx.Git, remote map[string]string, def string, u Update) (*Delta, error) {
		return Normalize(context.Background(), g, Input{Updates: []Update{u}, Remote: remote, DefaultBranch: def, Now: f.now})
	}
	plain := &gitx.Git{Dir: f.repo.Dir}
	// a default branch that does not peel fails closed even when its oid is
	// not among the remote's heads and tags
	if _, err := run(plain, map[string]string{"refs/x/main": bogusOID}, "refs/x/main", push("refs/heads/new", head)); err == nil {
		t.Error("unpeelable default branch ignored")
	}
	// a failing merge-base (not "no common ancestor") fails closed
	f.repo.Git("checkout", "-q", "-b", "side", base)
	side := f.repo.Commit("side", map[string]string{"s.txt": "s\n"})
	g := wrapGit(t, f.repo.Dir, " merge-base ", 2, "", true, 128)
	if _, err := run(g, map[string]string{main: head}, main, push(main, side)); !strings.Contains(gitErrArgs(err), " merge-base ") {
		t.Errorf("merge-base failure: %v", err)
	}
	// a file missing from numstat keeps zero counts; later files still get theirs
	g = wrapGit(t, f.repo.Dir, "--numstat", 1, "2\t0\ty.txt\x00", false, 0)
	d, err := run(g, map[string]string{main: base}, main, push(main, head))
	if err != nil {
		t.Fatal(err)
	}
	for _, fc := range d.Refs[0].Files {
		if want := map[string]int{"x.txt": 0, "y.txt": 2}[fc.Path]; fc.Added != want {
			t.Errorf("%s: Added %d, want %d", fc.Path, fc.Added, want)
		}
	}
}
