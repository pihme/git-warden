package rules

// gomutants survivor tests for rules.go. Most build the Delta by hand so a
// single rule decision can be checked without a full Normalize run.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/gitx"
)

const bogusOID = "1111111111111111111111111111111111111111"

// evalD evaluates a hand-built delta without the secret scanner.
func (f *fixture) evalD(d *Delta) (*Result, error) {
	f.t.Helper()
	if d.Now.IsZero() {
		d.Now = f.now
	}
	return Evaluate(context.Background(), &gitx.Git{Dir: f.repo.Dir}, f.cfg, d, Options{SkipScanner: true})
}

func (f *fixture) evalDOK(d *Delta) []Finding {
	f.t.Helper()
	res, err := f.evalD(d)
	if err != nil {
		f.t.Fatal(err)
	}
	return res.Findings
}

func refD(name string, kind Kind, files ...FileChange) *RefDelta {
	return &RefDelta{Update: Update{Ref: name, Kind: kind}, Files: files, Added: map[string][]Line{}}
}

func TestMutantsFindingStringAndShort(t *testing.T) {
	if got := (Finding{Rule: "R", Path: "p", Line: 1, Message: "m"}).String(); got != "R p:1: m" {
		t.Errorf("String = %q", got)
	}
	if got := short(strings.Repeat("a", 12)); got != strings.Repeat("a", 12) {
		t.Errorf("short(12) = %q", got)
	}
	if got := short(strings.Repeat("b", 13)); got != strings.Repeat("b", 12) {
		t.Errorf("short(13) = %q", got)
	}
}

// A limit that is missing from a hand-built config fails the evaluation.
func TestREQ_PG_004_MissingLimitFailsClosed(t *testing.T) {
	for _, c := range []struct{ rule, key string }{
		{"REF-COUNT", "max_refs"},
		{"CONTENT-BLOB", "min_base64"}, {"CONTENT-BLOB", "min_hex"},
		{"META-UNSIGNED", "lookback"}, {"META-BACKDATED", "max_age"}, {"META-FUTURE", "max_skew"},
		{"SIZE-LIMIT", "max_bytes"}, {"SIZE-LARGE", "max_commits"}, {"SIZE-MASS-DELETE", "min_lines"},
	} {
		f := newFixture(t, "")
		delete(f.cfg.Rules[c.rule].Limits, c.key)
		if _, err := f.evalD(&Delta{}); err == nil || !strings.Contains(err.Error(), c.key) {
			t.Errorf("%s without %s: err = %v", c.rule, c.key, err)
		}
	}
}

func TestMutantsFindingsSortedByRefThenRule(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-COUNT: {max_refs: 1}\n")
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{refD("refs/notes/b", Delete), refD("refs/heads/a", Delete)}})
	var got []string
	for _, x := range fs {
		got = append(got, x.Ref+" "+x.Rule)
	}
	want := []string{" REF-COUNT", "refs/heads/a REF-DELETE", "refs/notes/b REF-DELETE", "refs/notes/b REF-NAMESPACE"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order\n got %q\nwant %q", got, want)
	}
}

// Only a triggered, enabled rule exempted by allow records a lease exemption.
func TestMutantsAllowedOnlyWhenTriggeredAndExempt(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-NAMESPACE: {allow: ['refs/heads/main']}\n  REF-NON-FF: {enabled: false}\n")
	res, err := f.evalD(&Delta{Refs: []*RefDelta{refD(main, NonFF)}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsAllowed("REF-NAMESPACE", main) {
		t.Error("REF-NAMESPACE not triggered, but recorded as allowed")
	}
	if res.IsAllowed("REF-NON-FF", main) {
		t.Error("disabled REF-NON-FF recorded as allowed")
	}
	e := &eval{cfg: newFixture(t, "rules:\n  REF-NON-FF: {allow: ['x']}\n").cfg,
		res: &Result{Allowed: map[string]map[string]bool{}}, seen: map[string]bool{}}
	e.check("REF-NON-FF", "x", true, Finding{})
	if len(e.res.Allowed) != 0 {
		t.Errorf("finding without ref recorded as allowed: %v", e.res.Allowed)
	}
}

func TestMutantsRefAndPathMessages(t *testing.T) {
	f := newFixture(t, "rules:\n  REF-NAMESPACE: {deny: ['refs/heads/main']}\n")
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{
		refD(main, FF, FileChange{Status: 'M', Path: ".github/workflows/ci.yml"}, FileChange{Status: 'M', Path: "go.mod"}),
		refD("refs/notes/x", FF),
	}})
	for _, x := range findings(fs, "REF-NAMESPACE") {
		want := map[string]string{main: "pushes to this ref are reserved for a human",
			"refs/notes/x": "ref outside refs/heads/* and refs/tags/*"}[x.Ref]
		if x.Message != want {
			t.Errorf("REF-NAMESPACE %s: %q, want %q", x.Ref, x.Message, want)
		}
	}
	if len(findings(fs, "REF-NAMESPACE")) != 2 {
		t.Errorf("REF-NAMESPACE findings: %v", fs)
	}
	if x := expect(t, fs, "PATH-RED", ".github/workflows/ci.yml"); x.Message != "this path is reserved for a human" {
		t.Errorf("PATH-RED message %q", x.Message)
	}
	if x := expect(t, fs, "PATH-YELLOW", "go.mod"); !strings.HasPrefix(x.Message, "dependency, build, hook") {
		t.Errorf("PATH-YELLOW message %q", x.Message)
	}
}

func TestMutantsModeRules(t *testing.T) {
	f := newFixture(t, "")
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{refD(main, FF,
		FileChange{Status: 'D', Path: "gone", OldMode: "120000", NewMode: "120000"},
		FileChange{Status: 'A', Path: "run", NewMode: "100755"},
		FileChange{Status: 'A', Path: "link", NewMode: "120000"},
		FileChange{Status: 'A', Path: "sub", NewMode: "160000"},
	)}})
	if p := paths(fs, "MODE-SYMLINK"); p["gone"] || !p["link"] {
		t.Errorf("MODE-SYMLINK on %v, want only link", p)
	}
	for rule, msg := range map[string]string{
		"MODE-EXEC":      "file becomes executable (mode 100755); commit it as 100644",
		"MODE-SYMLINK":   "symlink added or changed",
		"MODE-SUBMODULE": "submodule (gitlink) added or changed",
	} {
		if x := expect(t, fs, rule, ""); x.Message != msg {
			t.Errorf("%s message %q", rule, x.Message)
		}
	}
}

func TestMutantsContentRules(t *testing.T) {
	f := newFixture(t, "")
	allow := []Line{{No: 1, Text: "// gitleaks:allow"}}
	rd := refD(main, FF,
		FileChange{Status: 'A', Path: "sub", NewMode: "160000"},
		FileChange{Status: 'A', Path: "link", NewMode: "120000"},
		FileChange{Status: 'A', Path: "a.go", NewMode: "100644"})
	rd.Added = map[string][]Line{"sub": allow, "link": allow, "a.go": {
		{No: 1, Text: "\u200bstart"},
		{No: 2, Text: "x \u200b y \u200c"},
		{No: 3, Text: "// gitleaks:allow"},
	}}
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{rd}})
	if p := paths(fs, "CONTENT-SCANNER-ALLOW"); p["sub"] || p["link"] || !p["a.go"] {
		t.Errorf("CONTENT-SCANNER-ALLOW on %v, want only a.go", p)
	}
	if x := expect(t, fs, "CONTENT-SCANNER-ALLOW", "a.go"); x.Message != "inline exception for the secret scanner" {
		t.Errorf("message %q", x.Message)
	}
	byLine := map[int][]string{}
	for _, x := range findings(fs, "CONTENT-INVISIBLE") {
		byLine[x.Line] = append(byLine[x.Line], x.Message)
	}
	if len(byLine[1]) != 1 {
		t.Errorf("zero-width space at the start of line 1 must fire: %v", byLine)
	}
	if m := byLine[2]; len(m) != 1 || !strings.Contains(m[0], "U+200B") {
		t.Errorf("line 2: %q, want one finding for the first character", m)
	}
}

func TestMutantsLongLiteral(t *testing.T) {
	for _, c := range []struct {
		s              string
		minB64, minHex int
		kind           string
		n              int
	}{
		{"", 0, 0, "hex", 0},
		{"", 0, 1, "base64", 0},
		{"!!", 1, 1, "", 0},
		{"zz", 200, 1, "", 0},
		{"abc", 100, 3, "hex", 3},
		{"ab!abc", 100, 3, "hex", 3},
		{"x09afAF", 100, 6, "hex", 6},
		{"09afAF", 100, 6, "hex", 6},
		{"/:`gG@[{", 100, 1, "", 0},
		{"9:9", 100, 2, "", 0},
		{"0909", 100, 4, "hex", 4},
		{"azAZ09+/-_=", 11, 100, "base64", 11},
		{"a!az", 3, 100, "", 0},
		{"zZ9", 3, 100, "base64", 3},
		{"0Zz-", 4, 100, "base64", 4},
		{":`{@[", 1, 100, "", 0},
		{"z:z", 2, 100, "", 0},
		{"Z@Z", 2, 100, "", 0},
		{"0`0", 2, 100, "", 0},
	} {
		kind, n := longLiteral(c.s, c.minB64, c.minHex)
		if kind != c.kind || n != c.n {
			t.Errorf("longLiteral(%q, %d, %d) = %q, %d; want %q, %d", c.s, c.minB64, c.minHex, kind, n, c.kind, c.n)
		}
	}
	// each hex digit class boundary counts as hex
	for _, ch := range "09afAF" {
		if kind, _ := longLiteral(strings.Repeat(string(ch), 5), 100, 5); kind != "hex" {
			t.Errorf("%q not hex", ch)
		}
	}
	for _, ch := range "gG:`@/" {
		if kind, _ := longLiteral(strings.Repeat(string(ch), 5), 100, 5); kind == "hex" {
			t.Errorf("%q counted as hex", ch)
		}
	}
	for _, ch := range "09azAZ+/-_=" {
		if kind, _ := longLiteral(strings.Repeat(string(ch), 5), 5, 100); kind == "" {
			t.Errorf("%q not base64", ch)
		}
	}
}

func TestREQ_PG_026_PagesScriptHosts(t *testing.T) {
	f := newFixture(t, "")
	pages := "refs/heads/gh-pages"
	html := FileChange{Status: 'A', Path: "index.html", NewMode: "100644"}
	lines := []Line{
		{No: 1, Text: `<script src="https://a.example/x.js"></script><script src="https://b.example/y.js"></script>`},
		{No: 2, Text: `<script src="app.js"></script><link href="https://c.example/s.css">`},
	}
	other := refD(main, FF, html)
	other.Added["index.html"] = lines
	pg := refD(pages, Create, html)
	pg.Added["index.html"] = lines
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{other, pg}})
	hosts := map[string]bool{}
	for _, x := range findings(fs, "CONTENT-PAGES-SCRIPT") {
		if x.Ref != pages {
			t.Errorf("finding on %s", x.Ref)
		}
		hosts[strings.Fields(x.Message)[7]] = true
	}
	if len(hosts) != 3 || !hosts["a.example"] || !hosts["b.example"] || !hosts["c.example"] {
		t.Errorf("hosts %v, want a, b and c.example", hosts)
	}
	for _, k := range []Kind{Delete, Noop} {
		rd := refD(pages, k, html)
		rd.Added["index.html"] = lines
		if fs := f.evalDOK(&Delta{Refs: []*RefDelta{rd}}); len(findings(fs, "CONTENT-PAGES-SCRIPT")) != 0 {
			t.Errorf("%s of the Pages branch checked: %v", k, fs)
		}
	}
	// the old tree cannot be searched: fail closed
	rd := refD(pages, FF, html)
	rd.OldCommit = bogusOID
	rd.Added["index.html"] = lines
	if _, err := f.evalD(&Delta{Refs: []*RefDelta{rd}}); err == nil {
		t.Error("git grep failure on the old tree was ignored")
	}
}

func commitD(oid string, at time.Time, parents ...string) Commit {
	return Commit{OID: oid, Author: at, Committer: at, Parents: parents}
}

func TestMutantsMetaRulesRefSelection(t *testing.T) {
	f := newFixture(t, "")
	old := commitD("2222222222222222222222222222222222222222", f.now.Add(-48*time.Hour))
	del := refD("refs/heads/x", Delete)
	del.OldCommit = bogusOID
	m := refD(main, Create)
	m.Commits = []Commit{old}
	fs := f.evalDOK(&Delta{Refs: []*RefDelta{del, m}, Commits: []Commit{old}})
	if len(findings(fs, "META-BACKDATED")) != 1 {
		t.Errorf("META-BACKDATED: %v", fs)
	}
	// future dates carry their own message
	fut := commitD("3333333333333333333333333333333333333333", f.now.Add(time.Hour))
	m.Commits = []Commit{fut}
	fs = f.evalDOK(&Delta{Refs: []*RefDelta{m}, Commits: []Commit{fut}})
	if x := expect(t, fs, "META-FUTURE", ""); !strings.Contains(x.Message, "after the push; fix the clock") {
		t.Errorf("META-FUTURE message %q", x.Message)
	}
	// a parent that cannot be read fails closed
	orphan := commitD("4444444444444444444444444444444444444444", f.now, bogusOID)
	m.Commits = []Commit{orphan}
	if _, err := f.evalD(&Delta{Refs: []*RefDelta{m}, Commits: []Commit{orphan}}); err == nil {
		t.Error("unreadable parent ignored")
	}
}

func TestREQ_PG_028_SignedHistoryErrorsAndDisable(t *testing.T) {
	c := commitD("2222222222222222222222222222222222222222", time.Now())
	rd := refD(main, FF)
	rd.OldCommit = bogusOID
	rd.Commits = []Commit{c}
	d := func() *Delta { return &Delta{Refs: []*RefDelta{rd}, Commits: []Commit{c}} }
	if _, err := newFixture(t, "").evalD(d()); err == nil {
		t.Error("rev-list failure on the old tip ignored")
	}
	if _, err := newFixture(t, "rules:\n  META-UNSIGNED: {enabled: false}\n").evalD(d()); err != nil {
		t.Errorf("disabled META-UNSIGNED still reads history: %v", err)
	}
	// new ref, default branch named but not on the remote: quiet
	nr := refD("refs/heads/new", Create)
	nr.Commits = []Commit{c}
	if _, err := newFixture(t, "").evalD(&Delta{Refs: []*RefDelta{nr}, Commits: []Commit{c}, Remote: map[string]string{}, DefaultBranch: main}); err != nil {
		t.Errorf("default branch missing on the remote: %v", err)
	}
	// default branch on the remote but not a commit here: fail closed
	if _, err := newFixture(t, "").evalD(&Delta{Refs: []*RefDelta{nr}, Commits: []Commit{c}, Remote: map[string]string{main: bogusOID}, DefaultBranch: main}); err == nil {
		t.Error("unpeelable default branch ignored")
	}
}

func TestREQ_PG_028_LookbackZeroAndOne(t *testing.T) {
	for lookback, want := range map[string]int{"0": 0, "1": 1} {
		f := newReqFixture(t, "rules:\n  META-UNSIGNED: {lookback: "+lookback+"}\n"+quietDates)
		start := f.now.Add(-time.Hour)
		tip := metaChain(t, f, "", 1, start, true, "signed")[0]
		u := metaChain(t, f, tip, 1, start.Add(time.Minute), false, "unsigned")
		if got := unsignedCommits(f.eval(map[string]string{main: tip}, push(main, u[0]))); len(got) != want {
			t.Errorf("lookback %s: %d META-UNSIGNED findings, want %d", lookback, len(got), want)
		}
	}
}

// A branch update looks back on its own old tip, not the default branch.
func TestREQ_PG_029_BranchUsesOwnHistory(t *testing.T) {
	f := newReqFixture(t, "rules:\n  META-UNSIGNED: {lookback: 1}\n"+quietDates)
	start := f.now.Add(-time.Hour)
	def := metaChain(t, f, "", 1, start, false, "unsigned default")[0]
	feat := metaChain(t, f, "", 1, start, true, "signed feature")[0]
	u := metaChain(t, f, feat, 1, start.Add(time.Minute), false, "unsigned")
	fs := f.eval(map[string]string{main: def, "refs/heads/feature": feat}, push("refs/heads/feature", u[0]))
	if got := unsignedCommits(fs); len(got) != 1 {
		t.Errorf("META-UNSIGNED %v, want the new commit flagged", got)
	}
}

func TestMutantsSizeRulesRefSelection(t *testing.T) {
	f := newFixture(t, "rules:\n  SIZE-MASS-DELETE: {max_deleted_files: 0}\n  SIZE-LARGE: {max_files: 0}\n")
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
