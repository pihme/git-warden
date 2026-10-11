package rules

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
)

// totalFindings returns the per-ref (pathless) findings of rule.
func totalFindings(fs []Finding, rule string) []Finding {
	var out []Finding
	for _, x := range findings(fs, rule) {
		if x.Path == "" {
			out = append(out, x)
		}
	}
	return out
}

func expectTotal(t *testing.T, fs []Finding, rule, what string, want bool) {
	t.Helper()
	got := false
	for _, x := range totalFindings(fs, rule) {
		if strings.Contains(x.Message, what) {
			got = true
			if x.Ref != main {
				t.Errorf("%s %s on ref %q, want %s", rule, what, x.Ref, main)
			}
		}
	}
	if got != want {
		t.Errorf("%s for %q: fired=%v, want %v (findings %v)", rule, what, got, want, fs)
	}
}

func nFiles(n int, prefix string) map[string]string {
	m := map[string]string{}
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("%s%d.txt", prefix, i)] = fmt.Sprintf("%s %d\n", prefix, i)
	}
	return m
}

func TestREQ_PG_032_033_SizeDefaults(t *testing.T) {
	f := newReqFixture(t, "")
	want := map[string]map[string]int64{
		"SIZE-LIMIT":       {"max_files": 2000, "max_lines": 50000, "max_bytes": 50000000, "max_file_bytes": 10000000},
		"SIZE-LARGE":       {"max_files": 200, "max_lines": 5000, "max_commits": 100, "max_file_bytes": 1000000},
		"SIZE-MASS-DELETE": {"max_deleted_files": 20, "max_deleted_share": 50, "min_lines": 100},
	}
	for id, keys := range want {
		for k, v := range keys {
			got, err := f.cfg.Rule(id).Int(k)
			if err != nil || got != v {
				t.Errorf("%s.%s = %d, %v; want %d", id, k, got, err, v)
			}
		}
	}
	for id, c := range map[string]config.Color{"SIZE-LIMIT": config.Red, "SIZE-LARGE": config.Yellow, "SIZE-MASS-DELETE": config.Yellow} {
		if got := f.cfg.Rule(id).Color; got != c {
			t.Errorf("%s color %s, want %s", id, got, c)
		}
	}
}

// sizeRulesYAML sets one size limit and turns the path rules off.
func sizeRulesYAML(rule, key string, limit int64) string {
	return fmt.Sprintf("rules:\n  %s: {%s: %d}\n  PATH-YELLOW: {enabled: false}\n  PATH-RED: {enabled: false}\n", rule, key, limit)
}

func TestREQ_PG_032_033_FileAndLineTotals(t *testing.T) {
	for _, rule := range []string{"SIZE-LIMIT", "SIZE-LARGE"} {
		color := config.Red
		if rule == "SIZE-LARGE" {
			color = config.Yellow
		}
		t.Run(rule+"/max_files", func(t *testing.T) {
			f := newReqFixture(t, sizeRulesYAML(rule, "max_files", 3))
			base := f.repo.Commit("base", nil)
			at := f.repo.Commit("three", nFiles(3, "a"))
			expectTotal(t, f.eval(map[string]string{main: base}, push(main, at)), rule, "changed files", false)
			over := f.repo.Commit("four", nFiles(1, "b"))
			fs := f.eval(map[string]string{main: base}, push(main, over))
			expectTotal(t, fs, rule, "4 changed files (limit 3)", true)
			expectColor(t, fs, rule, color)
		})
		t.Run(rule+"/max_lines", func(t *testing.T) {
			f := newReqFixture(t, sizeRulesYAML(rule, "max_lines", 10))
			base := f.repo.Commit("base", map[string]string{"a.txt": lines("old", 4)})
			// 4 deleted + 6 added = 10 changed lines
			at := f.repo.Commit("ten", map[string]string{"a.txt": lines("new", 6)})
			expectTotal(t, f.eval(map[string]string{main: base}, push(main, at)), rule, "changed lines", false)
			over := f.repo.Commit("eleven", map[string]string{"b.txt": "x\n"})
			expectTotal(t, f.eval(map[string]string{main: base}, push(main, over)), rule, "11 changed lines (limit 10)", true)
		})
		t.Run(rule+"/max_file_bytes", func(t *testing.T) {
			f := newReqFixture(t, sizeRulesYAML(rule, "max_file_bytes", 100))
			base := f.repo.Commit("base", map[string]string{"gone.bin.txt": strings.Repeat("g", 500)})
			f.repo.Git("rm", "--quiet", "gone.bin.txt")
			head := f.repo.Commit("files", map[string]string{"at.txt": strings.Repeat("a", 100), "over.txt": strings.Repeat("o", 101)})
			fs := f.eval(map[string]string{main: base}, push(main, head))
			wantPaths(t, fs, rule, []string{"at.txt", "over.txt", "gone.bin.txt"}, "over.txt")
			if x := expect(t, fs, rule, "over.txt"); x.Ref != main || !strings.Contains(x.Message, "101 bytes (limit 100)") {
				t.Errorf("per-file finding %v", x)
			}
		})
	}
}

func TestREQ_PG_032_MaxBytes(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("blob", map[string]string{"data.txt": strings.Repeat("0123456789abcdef\n", 64)})
	d := f.normalize(map[string]string{main: base}, push(main, head))
	n := d.Refs[0].NewBytes
	if n <= 0 {
		t.Fatalf("NewBytes = %d", n)
	}
	at := f.withRules(fmt.Sprintf("rules:\n  SIZE-LIMIT: {max_bytes: %d}\n", n))
	expectTotal(t, at.eval(map[string]string{main: base}, push(main, head)), "SIZE-LIMIT", "bytes of new objects", false)
	over := f.withRules(fmt.Sprintf("rules:\n  SIZE-LIMIT: {max_bytes: %d}\n", n-1))
	expectTotal(t, over.eval(map[string]string{main: base}, push(main, head)), "SIZE-LIMIT",
		fmt.Sprintf("%d bytes of new objects (limit %d)", n, n-1), true)
}

// NewBytes is the sum of the sizes of all new objects, not the size of any
// single one: checked against git's own count with several new blobs.
func TestREQ_PG_032_NewBytesSumsAllNewObjects(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("blobs", map[string]string{
		"a.txt": strings.Repeat("a", 1000) + "\n",
		"b.txt": strings.Repeat("b", 3000) + "\n",
		"c.txt": strings.Repeat("c", 5000) + "\n",
	})
	objs := f.repo.Git("rev-list", "--objects", base+".."+head)
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(objs), "\n") {
		id, _, _ := strings.Cut(l, " ")
		ids = append(ids, id)
	}
	cmd := exec.Command("git", "cat-file", "--batch-check=%(objectsize)")
	cmd.Dir, cmd.Env = f.repo.Dir, append(os.Environ(), f.repo.Env...)
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var want, largest int64
	for _, l := range strings.Fields(string(out)) {
		s, err := strconv.ParseInt(l, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		want += s
		largest = max(largest, s)
	}
	if len(ids) < 5 || want <= largest {
		t.Fatalf("fixture: %d objects, sum %d, largest %d", len(ids), want, largest)
	}
	if got := f.normalize(map[string]string{main: base}, push(main, head)).Refs[0].NewBytes; got != want {
		t.Errorf("NewBytes = %d; want %d (sum of %d new objects)", got, want, len(ids))
	}
}

func TestREQ_PG_033_MaxCommits(t *testing.T) {
	f := newReqFixture(t, sizeRulesYAML("SIZE-LARGE", "max_commits", 2))
	base := f.repo.Commit("base", nil)
	f.repo.Commit("c1", map[string]string{"1.txt": "1\n"})
	two := f.repo.Commit("c2", map[string]string{"2.txt": "2\n"})
	expectTotal(t, f.eval(map[string]string{main: base}, push(main, two)), "SIZE-LARGE", "new commits", false)
	three := f.repo.Commit("c3", map[string]string{"3.txt": "3\n"})
	fs := f.eval(map[string]string{main: base}, push(main, three))
	expectTotal(t, fs, "SIZE-LARGE", "3 new commits (limit 2)", true)
	// SIZE-LIMIT has no commit limit
	if len(totalFindings(fs, "SIZE-LIMIT")) != 0 {
		t.Errorf("SIZE-LIMIT fired on commits: %v", fs)
	}
}

func TestREQ_PG_032_SizeSkipsDeletions(t *testing.T) {
	f := newReqFixture(t, "")
	f.cfg.Rule("SIZE-LARGE").Limits["max_files"] = 0 // below the range (REQ-PG-046), set in-process
	base := f.repo.Commit("base", nFiles(3, "a"))
	expectNone(t, f.eval(map[string]string{main: base, "refs/heads/old": base}, Update{Ref: "refs/heads/old", New: gitx.ZeroOID}), "SIZE-LARGE")
}

func TestREQ_PG_034_DeletedFiles(t *testing.T) {
	f := newReqFixture(t, "rules:\n  SIZE-MASS-DELETE: {max_deleted_files: 2}\n  PATH-YELLOW: {enabled: false}\n")
	base := f.repo.Commit("base", nFiles(3, "gone"))
	f.repo.Git("rm", "--quiet", "gone0.txt", "gone1.txt")
	two := f.repo.Commit("delete two", map[string]string{"keep.txt": "k\n"})
	expectTotal(t, f.eval(map[string]string{main: base}, push(main, two)), "SIZE-MASS-DELETE", "files deleted", false)
	f.repo.Git("rm", "--quiet", "gone2.txt")
	three := f.repo.Commit("delete three", map[string]string{"keep2.txt": "k\n"})
	fs := f.eval(map[string]string{main: base}, push(main, three))
	expectTotal(t, fs, "SIZE-MASS-DELETE", "3 files deleted (limit 2)", true)
	expectColor(t, fs, "SIZE-MASS-DELETE", config.Yellow)
}

func TestREQ_PG_034_DeletedShare(t *testing.T) {
	f := newReqFixture(t, "rules:\n  SIZE-MASS-DELETE: {max_deleted_share: 50, min_lines: 10}\n")
	base := f.repo.Commit("base", map[string]string{
		"half.txt":   lines("h", 20),                           // delete 10 of 20: exactly 50 %
		"more.txt":   lines("m", 20),                           // delete 11 of 20
		"small.txt":  lines("s", 10),                           // exactly min_lines: never checked
		"eleven.txt": lines("e", 11),                           // just over min_lines
		"noeol.txt":  strings.TrimSuffix(lines("n", 11), "\n"), // 11 lines without final newline
	})
	head := f.repo.Commit("trim", map[string]string{
		"half.txt":   lines("h", 10),
		"more.txt":   lines("m", 9),
		"small.txt":  lines("s", 1),
		"eleven.txt": lines("e", 5),
		"noeol.txt":  lines("n", 5),
	})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "SIZE-MASS-DELETE", []string{"half.txt", "more.txt", "small.txt", "eleven.txt", "noeol.txt"},
		"more.txt", "eleven.txt", "noeol.txt")
	if x := expect(t, fs, "SIZE-MASS-DELETE", "more.txt"); !strings.Contains(x.Message, "11 of the file's 20 lines") {
		t.Errorf("share finding %v", x)
	}
}

func TestREQ_PG_034_DeletedShareDefaults(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"at.txt": lines("a", 100), "over.txt": lines("o", 101)})
	head := f.repo.Commit("trim", map[string]string{"at.txt": lines("a", 1), "over.txt": lines("o", 50)})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "SIZE-MASS-DELETE", []string{"at.txt", "over.txt"}, "over.txt")
}

func TestREQ_PG_035_ShareOnRenamesNotDeletes(t *testing.T) {
	f := newReqFixture(t, "rules:\n  SIZE-MASS-DELETE: {max_deleted_share: 50, min_lines: 10}\n  PATH-YELLOW: {enabled: false}\n")
	long := lines("this is a long line that keeps the rename similar enough, number ", 10)
	base := f.repo.Commit("base", map[string]string{"old.txt": long + lines("x", 12), "gone.txt": lines("g", 50)})
	f.repo.Git("mv", "old.txt", "new.txt")
	f.repo.Git("rm", "--quiet", "gone.txt")
	head := f.repo.Commit("rename and trim", map[string]string{"new.txt": long})
	d := f.normalize(map[string]string{main: base}, push(main, head))
	renamed := false
	for _, fc := range d.Refs[0].Files {
		if fc.Status == 'R' && fc.Path == "new.txt" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("fixture: new.txt not detected as rename: %+v", d.Refs[0].Files)
	}
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "SIZE-MASS-DELETE", []string{"new.txt", "old.txt", "gone.txt"}, "new.txt")
}

// REQ-PG-032 to 034: size rules check updated refs only, never deleted or
// unchanged ones, and never size deleted files.
func TestREQ_PG_032_033_034_SizeRulesPerRef(t *testing.T) {
	f := newFixture(t, "")
	// zero is below the configurable range (REQ-PG-046); set in-process so any file fires
	f.cfg.Rule("SIZE-MASS-DELETE").Limits["max_deleted_files"] = 0
	f.cfg.Rule("SIZE-LARGE").Limits["max_files"] = 0
	gone := FileChange{Status: 'D', Path: "a", NewSize: 2000000}
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{refD("refs/heads/x", Delete, gone), refD("refs/heads/y", Noop, gone),
		refD("refs/heads/a", FF, FileChange{Status: 'M', Path: "m"}), refD(main, FF, gone)}})
	if r := refs(fs, "SIZE-MASS-DELETE"); len(r) != 1 || !r[main] {
		t.Errorf("SIZE-MASS-DELETE on %v, want only main", r)
	}
	if r := refs(fs, "SIZE-LARGE"); len(r) != 2 || !r[main] || !r["refs/heads/a"] {
		t.Errorf("SIZE-LARGE on %v, want a and main", r)
	}
	for _, x := range append(findings(fs, "SIZE-LARGE"), findings(fs, "SIZE-LIMIT")...) {
		if x.Path == "a" {
			t.Errorf("deleted file checked for size: %v", x)
		}
	}
}

// Submodule entries point at commits of another repository: not sized.
func TestREQ_PG_032_SubmoduleNotSized(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", map[string]string{"a.txt": "a\n"})
	f.repo.Git("update-index", "--add", "--cacheinfo", "160000,"+bogusOID+",sub")
	f.repo.Git("commit", "-q", "-m", "submodule")
	d := f.normalize(map[string]string{main: base}, push(main, f.repo.Head()))
	if fs := d.Refs[0].Files; len(fs) != 1 || fs[0].NewMode != "160000" || fs[0].NewSize != 0 {
		t.Fatalf("files %v", fs)
	}
}

func TestREQ_PG_035_MassDeleteShareEdges(t *testing.T) {
	f := newFixture(t, "")
	mod := func(blob string, deleted int, binary bool) *Delta {
		return &Delta{Refs: []*RefDelta{refD(main, FF, FileChange{Status: 'M', Path: "f", OldBlob: blob, Deleted: deleted, Binary: binary})}}
	}
	if _, err := f.evalD(mod(bogusOID, 200, false)); err == nil {
		t.Error("unreadable old blob ignored")
	}
	if _, err := f.evalD(mod(bogusOID, 200, true)); err != nil {
		t.Errorf("binary file read for line counting: %v", err)
	}
	// 50 deleted lines * 100 <= 100 min_lines * 50%: not a candidate, not read
	if _, err := f.evalD(mod(bogusOID, 50, false)); err != nil {
		t.Errorf("non-candidate read: %v", err)
	}
	empty := hashObject(t, f.repo, "blob", "", false)
	if fs := f.evalDOK(mod(empty, 200, false)); len(findings(fs, "SIZE-MASS-DELETE")) != 0 {
		t.Errorf("empty old file: %v", fs)
	}
	one := hashObject(t, f.repo, "blob", "x", false)
	f0 := newFixture(t, "rules:\n  SIZE-MASS-DELETE: {min_lines: 0}\n")
	f0.repo = f.repo
	if fs := f0.evalDOK(mod(one, 1, false)); len(findings(fs, "SIZE-MASS-DELETE")) != 1 {
		t.Errorf("one line without newline, removed: %v", fs)
	}
	// 100 of 101 lines at a 99% share: 10000 > 9999
	f99 := newFixture(t, "rules:\n  SIZE-MASS-DELETE: {max_deleted_share: 99}\n")
	f99.repo = f.repo
	b101 := hashObject(t, f.repo, "blob", lines("l", 101), false)
	if fs := f99.evalDOK(mod(b101, 100, false)); len(findings(fs, "SIZE-MASS-DELETE")) != 1 {
		t.Errorf("100 of 101 lines at 99%%: %v", fs)
	}
}
