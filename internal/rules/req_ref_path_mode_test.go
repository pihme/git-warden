package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
)

// ---- REF-* ----

func TestREQ_PG_006_RefDeleteBranchAndTag(t *testing.T) {
	f := newReqFixture(t, "")
	a := f.repo.Commit("a", nil)
	remote := map[string]string{main: a, "refs/heads/x": a, "refs/tags/v1": a}
	fs := f.eval(remote, push("refs/heads/x", gitx.ZeroOID), push("refs/tags/v1", gitx.ZeroOID))
	got := refs(fs, "REF-DELETE")
	if !got["refs/heads/x"] || !got["refs/tags/v1"] || len(got) != 2 {
		t.Fatalf("REF-DELETE on %v, want branch and tag: %v", got, fs)
	}
	expectColor(t, fs, "REF-DELETE", config.Red)
	if Verdict(fs) != config.Red {
		t.Fatalf("verdict %s", Verdict(fs))
	}
	// deleting a ref the remote does not have changes nothing
	expectNone(t, f.eval(remote, push("refs/heads/ghost", gitx.ZeroOID)), "REF-DELETE")
}

func TestREQ_PG_007_RefNonFastForward(t *testing.T) {
	f := newReqFixture(t, "")
	a := f.repo.Commit("a", map[string]string{"a.txt": "a\n"})
	b := f.repo.Commit("b", map[string]string{"b.txt": "b\n"})
	f.repo.Git("checkout", "--quiet", "-b", "side", a)
	c := f.repo.Commit("c", map[string]string{"c.txt": "c\n"})
	// unrelated history: no merge base at all
	f.repo.Git("checkout", "--quiet", "--orphan", "orphan")
	f.repo.Git("rm", "-rf", "--quiet", ".")
	o := f.repo.Commit("orphan", map[string]string{"o.txt": "o\n"})

	fs := f.eval(map[string]string{main: b}, push(main, c))
	expect(t, fs, "REF-NON-FF", "")
	expectColor(t, fs, "REF-NON-FF", config.Red)
	expect(t, f.eval(map[string]string{main: b}, push(main, o)), "REF-NON-FF", "")
	// a fast-forward is not a rewrite
	expectNone(t, f.eval(map[string]string{main: a}, push(main, b)), "REF-NON-FF")
}

func TestREQ_PG_008_RefTagMove(t *testing.T) {
	f := newReqFixture(t, "")
	a := f.repo.Commit("a", nil)
	b := f.repo.Commit("b", nil)
	remote := map[string]string{main: b, "refs/tags/v1": a}
	fs := f.eval(remote, push("refs/tags/v1", b))
	expect(t, fs, "REF-TAG-MOVE", "")
	expectColor(t, fs, "REF-TAG-MOVE", config.Red)
	// moving a tag forward is still a move (tags are not fast-forwarded)
	if f := find(fs, "REF-NON-FF"); f != nil {
		t.Fatalf("tag move reported as REF-NON-FF: %v", *f)
	}
	// pushing the tag to where it already is changes nothing
	expectNone(t, f.eval(remote, push("refs/tags/v1", a)), "REF-TAG-MOVE", "REF-TAG-NEW")
}

func TestREQ_PG_009_RefTagNew(t *testing.T) {
	f := newReqFixture(t, "")
	a := f.repo.Commit("a", nil)
	f.repo.Git("tag", "-a", "-m", "annotated", "v2", a)
	tagObj := f.repo.Git("rev-parse", "v2")
	fs := f.eval(map[string]string{main: a}, push("refs/tags/v1", a), push("refs/tags/v2", tagObj))
	got := refs(fs, "REF-TAG-NEW")
	if !got["refs/tags/v1"] || !got["refs/tags/v2"] {
		t.Fatalf("REF-TAG-NEW on %v: %v", got, fs)
	}
	expectColor(t, fs, "REF-TAG-NEW", config.Yellow)
	// a new branch is not a new tag
	expectNone(t, f.eval(map[string]string{main: a}, push("refs/heads/feature", a)), "REF-TAG-NEW")
}

func TestREQ_PG_010_RefNamespace(t *testing.T) {
	f := newReqFixture(t, "")
	a := f.repo.Commit("a", nil)
	outside := []string{"refs/notes/commits", "refs/warden/state", "refs/pull/1/head", "refs/meta/config", "refs/remotes/origin/main"}
	var ups []Update
	for _, r := range outside {
		ups = append(ups, push(r, a))
	}
	ups = append(ups, push("refs/heads/ok", a), push("refs/tags/ok", a))
	fs := f.eval(map[string]string{main: a}, ups...)
	got := refs(fs, "REF-NAMESPACE")
	for _, r := range outside {
		if !got[r] {
			t.Errorf("REF-NAMESPACE did not fire on %s", r)
		}
	}
	if got["refs/heads/ok"] || got["refs/tags/ok"] {
		t.Errorf("REF-NAMESPACE fired inside refs/heads or refs/tags: %v", got)
	}
	expectColor(t, fs, "REF-NAMESPACE", config.Red)
}

func TestREQ_PG_011_RefCountDefaultBoundary(t *testing.T) {
	f := newReqFixture(t, "")
	if n, err := f.cfg.Rule("REF-COUNT").Int("max_refs"); err != nil || n != 10 {
		t.Fatalf("default max_refs = %d, %v; want 10", n, err)
	}
	a := f.repo.Commit("a", nil)
	ups := func(n int) []Update {
		var out []Update
		for i := 0; i < n; i++ {
			out = append(out, push(fmt.Sprintf("refs/heads/b%02d", i), a))
		}
		return out
	}
	expectNone(t, f.eval(map[string]string{main: a}, ups(10)...), "REF-COUNT")
	fs := f.eval(map[string]string{main: a}, ups(11)...)
	c := expect(t, fs, "REF-COUNT", "")
	if c.Color != config.Red || !strings.Contains(c.Message, "11 refs") {
		t.Fatalf("REF-COUNT finding %v", c)
	}
}

// ---- PATH-* ----

func TestREQ_PG_012_PathRedAddedModifiedDeletedRenamed(t *testing.T) {
	f := newReqFixture(t, "")
	body := lines("rule line ", 8)
	base := f.repo.Commit("base", map[string]string{"LICENSE": "MIT\n", "CODEOWNERS": "* @x\n", "docs/rules.txt": body})
	f.repo.Git("rm", "--quiet", "CODEOWNERS")
	f.repo.Git("mv", "docs/rules.txt", "CLAUDE.md")
	head := f.repo.Commit("change", map[string]string{
		".github/workflows/ci.yml": "on: push\n", // added
		"LICENSE":                  "Apache\n",   // modified
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	cands := []string{".github/workflows/ci.yml", "LICENSE", "CODEOWNERS", "CLAUDE.md", "docs/rules.txt"}
	wantPaths(t, fs, "PATH-RED", cands, ".github/workflows/ci.yml", "LICENSE", "CODEOWNERS", "CLAUDE.md")
	expectColor(t, fs, "PATH-RED", config.Red)
}

func TestREQ_PG_013_PathYellowAddedModifiedDeleted(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"go.mod": "module x\n", ".gitattributes": "* text\n", "README.md": "x\n"})
	f.repo.Git("rm", "--quiet", ".gitattributes")
	head := f.repo.Commit("change", map[string]string{
		"Makefile":  "all:\n",     // added
		"go.mod":    "module y\n", // modified
		"README.md": "x\ny\n",     // not on the list
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "PATH-YELLOW", []string{"Makefile", "go.mod", ".gitattributes", "README.md"}, "Makefile", "go.mod", ".gitattributes")
	expectColor(t, fs, "PATH-YELLOW", config.Yellow)
}

func TestREQ_PG_014_RenameCheckedUnderBothNames(t *testing.T) {
	f := newReqFixture(t, "")
	body := lines("content line ", 8)
	base := f.repo.Commit("base", map[string]string{
		"LICENSE": body, "notes-red.txt": body + "r\n", "Makefile": body + "m\n", "notes-yellow.txt": body + "y\n",
	})
	f.repo.Git("mv", "LICENSE", "license.txt")     // out of PATH-RED
	f.repo.Git("mv", "notes-red.txt", "AGENTS.md") // into PATH-RED
	f.repo.Git("mv", "Makefile", "build.txt")      // out of PATH-YELLOW
	f.repo.Git("mv", "notes-yellow.txt", "go.mod") // into PATH-YELLOW
	head := f.repo.Commit("renames", nil)
	d := f.normalize(map[string]string{main: base}, push(main, head))
	for _, fc := range d.Refs[0].Files {
		if fc.Status != 'R' {
			t.Fatalf("fixture: %s is %c, not a rename", fc.Path, fc.Status)
		}
	}
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "PATH-RED", []string{"LICENSE", "license.txt", "AGENTS.md", "notes-red.txt"}, "LICENSE", "AGENTS.md")
	wantPaths(t, fs, "PATH-YELLOW", []string{"Makefile", "build.txt", "go.mod", "notes-yellow.txt"}, "Makefile", "go.mod")
}

func TestREQ_PG_015_PathOnBothListsIsRed(t *testing.T) {
	// LICENSE is on PATH-RED by default; put it on PATH-YELLOW too.
	f := newReqFixture(t, "rules:\n  PATH-YELLOW: {match: ['LICENSE']}\n")
	base := f.repo.Commit("base", map[string]string{"LICENSE": "MIT\n"})
	head := f.repo.Commit("change", map[string]string{"LICENSE": "Apache\n"})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	expect(t, fs, "PATH-RED", "LICENSE")
	if Verdict(fs) != config.Red {
		t.Fatalf("path on both lists gave verdict %s: %v", Verdict(fs), fs)
	}
}

// ---- MODE-* ----

func TestREQ_PG_016_ModeExecOnlyWhenBecomingExecutable(t *testing.T) {
	f := newReqFixture(t, "")
	f.repo.Write("tool", "x\n")
	f.repo.WriteMode("already", "x\n", 0o755)
	f.repo.WriteMode("down", "x\n", 0o755)
	base := f.repo.Commit("base", nil)
	f.repo.WriteMode("tool", "x\n", 0o755)    // 100644 -> 100755
	f.repo.WriteMode("already", "y\n", 0o755) // stays 100755, content changes
	f.repo.WriteMode("down", "x\n", 0o644)    // 100755 -> 100644
	f.repo.WriteMode("new", "x\n", 0o755)     // new executable file
	head := f.repo.Commit("modes", nil)
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "MODE-EXEC", []string{"tool", "already", "down", "new"}, "tool", "new")
	expectColor(t, fs, "MODE-EXEC", config.Yellow)
}

func TestREQ_PG_017_ModeSymlinkAddedOrChanged(t *testing.T) {
	f := newReqFixture(t, "")
	link := func(target, name string) {
		p := filepath.Join(f.repo.Dir, name)
		os.Remove(p)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	f.repo.Write("a.txt", "a\n")
	f.repo.Write("b.txt", "b\n")
	link("a.txt", "changed")
	link("a.txt", "removed")
	link("a.txt", "same")
	base := f.repo.Commit("base", nil)
	link("b.txt", "changed")
	link("a.txt", "added")
	os.Remove(filepath.Join(f.repo.Dir, "removed"))
	f.repo.Write("other.txt", "touch\n")
	head := f.repo.Commit("links", nil)
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "MODE-SYMLINK", []string{"changed", "added", "removed", "same"}, "changed", "added")
	expectColor(t, fs, "MODE-SYMLINK", config.Yellow)
}

func TestREQ_PG_018_ModeSubmoduleAddedOrChanged(t *testing.T) {
	f := newReqFixture(t, "")
	x := f.repo.Commit("x", map[string]string{"x.txt": "x\n"})
	y := f.repo.Commit("y", map[string]string{"y.txt": "y\n"})
	// gitlinks have no work tree directory, so commit with update-index only
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+x+",vendor/changed")
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+x+",vendor/removed")
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+x+",vendor/same")
	f.repo.Git("commit", "--quiet", "-m", "base")
	base := f.repo.Head()
	f.repo.Git("update-index", "--cacheinfo", "160000,"+y+",vendor/changed")
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+y+",vendor/added")
	f.repo.Git("update-index", "--force-remove", "vendor/removed")
	f.repo.Git("commit", "--quiet", "-m", "gitlinks")
	head := f.repo.Head()
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "MODE-SUBMODULE", []string{"vendor/changed", "vendor/added", "vendor/removed", "vendor/same"}, "vendor/changed", "vendor/added")
	expectColor(t, fs, "MODE-SUBMODULE", config.Yellow)
}
