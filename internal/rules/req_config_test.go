package rules

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
)

// documentedRules reads the rule tables in docs/push-guard-rules.md: rule ID
// and default color.
func documentedRules(t *testing.T) map[string]config.Color {
	t.Helper()
	data, err := os.ReadFile("../../docs/push-guard-rules.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `([A-Z]+(?:-[A-Z]+)+)` \\| (red|yellow) \\|")
	out := map[string]config.Color{}
	for _, m := range row.FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = config.Color(m[2])
	}
	if len(out) < 25 {
		t.Fatalf("found only %d rules in the rule tables", len(out))
	}
	return out
}

func TestREQ_PG_038_039_EveryRuleOnByDefaultWithDocumentedColor(t *testing.T) {
	f := newReqFixture(t, "")
	docs := documentedRules(t)
	for id, c := range docs {
		r, ok := f.cfg.Rules[id]
		if !ok {
			t.Errorf("documented rule %s has no default", id)
			continue
		}
		if !r.Enabled {
			t.Errorf("REQ-PG-038: %s is off by default", id)
		}
		if r.Color != c {
			t.Errorf("REQ-PG-039: %s default color %s, documented %s", id, r.Color, c)
		}
	}
	for id := range f.cfg.Rules {
		if _, ok := docs[id]; !ok {
			t.Errorf("rule %s has a default but is not in the rule tables", id)
		}
	}
}

func TestREQ_PG_038_DisabledRulesNeverFire(t *testing.T) {
	f := newReqFixture(t, `gitleaks: {path: /nonexistent/gitleaks}
rules:
  REF-COUNT: {enabled: false, max_refs: 0}
  PATH-RED: {enabled: false}
  META-UNSIGNED: {enabled: false, lookback: 1}
  CONTENT-SECRET: {enabled: false}
  SIZE-LARGE: {enabled: false, max_files: 0}
  REF-NON-FF: {enabled: false}
`)
	tree := hashObject(t, f.repo, "tree", "", false)
	now := f.now.Unix()
	signed := rawCommit(t, f.repo, tree, nil, now, now, true, "signed root")
	other := rawCommit(t, f.repo, tree, nil, now, now, true, "other root")
	f.repo.Git("reset", "--quiet", "--hard", signed)
	head := f.repo.Commit("unsigned", map[string]string{".github/workflows/ci.yml": "on: push\n"})
	sc, err := NewScanner(f.cfg, f.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	// the scanner binary does not exist: a disabled CONTENT-SECRET must not run it
	res, err := f.evalErr(map[string]string{main: signed, "refs/heads/x": other}, Options{Scanner: sc},
		push(main, head), push("refs/heads/x", head))
	if err != nil {
		t.Fatalf("REQ-PG-038: disabled CONTENT-SECRET still ran the scanner: %v", err)
	}
	expectNone(t, res.Findings, "REF-COUNT", "PATH-RED", "META-UNSIGNED", "CONTENT-SECRET", "SIZE-LARGE", "REF-NON-FF")
}

func TestREQ_PG_039_ColorOverrides(t *testing.T) {
	f := newReqFixture(t, `rules:
  PATH-YELLOW: {color: red}
  PATH-RED: {color: yellow}
  REF-COUNT: {color: yellow, max_refs: 1}
`)
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"go.mod": "module x\n", "LICENSE": "MIT\n"})
	fs := f.eval(map[string]string{main: base}, push(main, head), push("refs/heads/b", head))
	expect(t, fs, "PATH-YELLOW", "go.mod")
	expect(t, fs, "PATH-RED", "LICENSE")
	expect(t, fs, "REF-COUNT", "")
	expectColor(t, fs, "PATH-YELLOW", config.Red)
	expectColor(t, fs, "PATH-RED", config.Yellow)
	expectColor(t, fs, "REF-COUNT", config.Yellow)

	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":          "agent: {name: test-agent}\n",
		"repos/demo/warden.yaml": "remote: /nonexistent/remote.git\nrules:\n  PATH-RED: {color: green}\n",
	})
	if _, err := config.LoadRepo(dir, "demo"); err == nil {
		t.Error("REQ-PG-039: color green accepted; only red or yellow are valid")
	}
}

func TestREQ_PG_040_AllowPerSubject(t *testing.T) {
	f := newReqFixture(t, `rules:
  PATH-RED: {allow: ['LICENSE']}
  REF-DELETE: {allow: ['refs/heads/tmp/.*']}
`)
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("x", map[string]string{"LICENSE": "MIT\n", "CODEOWNERS": "* @a\n"})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "PATH-RED", []string{"LICENSE", "CODEOWNERS"}, "CODEOWNERS")

	res, err := f.evalErr(map[string]string{main: base, "refs/heads/tmp/x": base, "refs/heads/other": base}, Options{SkipScanner: true},
		push("refs/heads/tmp/x", gitx.ZeroOID), push("refs/heads/other", gitx.ZeroOID))
	if err != nil {
		t.Fatal(err)
	}
	if got := refs(res.Findings, "REF-DELETE"); got["refs/heads/tmp/x"] || !got["refs/heads/other"] {
		t.Errorf("REQ-PG-040: REF-DELETE fired on %v", got)
	}
	if !res.IsAllowed("REF-DELETE", "refs/heads/tmp/x") || res.IsAllowed("REF-DELETE", "refs/heads/other") {
		t.Errorf("REQ-PG-040: allowed refs %v", res.Allowed)
	}
}

func TestREQ_PG_041_DenyWinsOverAllow(t *testing.T) {
	f := newReqFixture(t, `rules:
  REF-NON-FF: {allow: ['.*'], deny: ['refs/heads/main']}
  PATH-YELLOW: {deny: ['README\.md']}
`)
	base := f.repo.Commit("base", map[string]string{"README.md": "a\n"})
	f.repo.Git("checkout", "--quiet", "--orphan", "rewrite")
	f.repo.Git("rm", "--quiet", "-rf", ".")
	rewrite := f.repo.Commit("rewrite", map[string]string{"README.md": "b\n"})
	remote := map[string]string{main: base, "refs/heads/feature": base}
	res, err := f.evalErr(remote, Options{SkipScanner: true}, push(main, rewrite), push("refs/heads/feature", rewrite))
	if err != nil {
		t.Fatal(err)
	}
	if got := refs(res.Findings, "REF-NON-FF"); !got[main] || got["refs/heads/feature"] {
		t.Errorf("REQ-PG-041: REF-NON-FF fired on %v, want main only", got)
	}
	if res.IsAllowed("REF-NON-FF", main) || !res.IsAllowed("REF-NON-FF", "refs/heads/feature") {
		t.Errorf("REQ-PG-041: a denied ref must not be recorded as allowed: %v", res.Allowed)
	}
	// deny fires without a trigger: README.md is on no PATH-YELLOW list
	expect(t, res.Findings, "PATH-YELLOW", "README.md")
}

func TestREQ_PG_042_PatternsMatchWholeSubject(t *testing.T) {
	f := newReqFixture(t, `rules:
  REF-DELETE: {allow: ['agent.*', 'refs/heads/agent/.*']}
  CONTENT-BINARY: {allow: ['docs', 'x\.png|y\.png']}
`)
	bin := "\x00\x01\x02binary\x00"
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("bins", map[string]string{"docs/a.png": bin, "x.png.bin": bin, "y.png": bin, "x.png": bin})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-BINARY", []string{"docs/a.png", "x.png.bin", "y.png", "x.png"}, "docs/a.png", "x.png.bin")

	remote := map[string]string{main: base, "refs/heads/main-agent-x": base, "refs/heads/agent-1": base, "refs/heads/agent/ok": base}
	fs = f.eval(remote, push("refs/heads/main-agent-x", gitx.ZeroOID), push("refs/heads/agent-1", gitx.ZeroOID), push("refs/heads/agent/ok", gitx.ZeroOID))
	got := refs(fs, "REF-DELETE")
	if !got["refs/heads/main-agent-x"] || !got["refs/heads/agent-1"] || got["refs/heads/agent/ok"] {
		t.Errorf("REQ-PG-042: REF-DELETE fired on %v", got)
	}
}

func TestREQ_PG_043_Subjects(t *testing.T) {
	future := func(f *fixture) string {
		tree := hashObject(t, f.repo, "tree", "", false)
		ts := f.now.Add(time.Hour).Unix()
		return rawCommit(t, f.repo, tree, nil, ts, ts, false, "future")
	}
	t.Run("META branch without refs/heads", func(t *testing.T) {
		f := newReqFixture(t, "rules:\n  META-FUTURE: {allow: ['feature', 'refs/tags/v1']}\n")
		c := future(f)
		fs := f.eval(nil, push("refs/heads/feature", c), push("refs/heads/refs/heads/feature", c), push("refs/tags/v1", c), push("refs/tags/v2", c))
		got := refs(fs, "META-FUTURE")
		if got["refs/heads/feature"] || got["refs/tags/v1"] || !got["refs/tags/v2"] {
			t.Errorf("REQ-PG-043: META-FUTURE fired on %v", got)
		}
		f2 := f.withRules("rules:\n  META-FUTURE: {allow: ['refs/heads/feature', 'v1']}\n")
		got = refs(f2.eval(nil, push("refs/heads/feature", c), push("refs/tags/v1", c)), "META-FUTURE")
		if !got["refs/heads/feature"] || !got["refs/tags/v1"] {
			t.Errorf("REQ-PG-043: META subject must be the short branch and the full tag ref, fired on %v", got)
		}
	})
	t.Run("REF full ref", func(t *testing.T) {
		f := newReqFixture(t, "rules:\n  REF-DELETE: {allow: ['main', 'x']}\n")
		base := f.repo.Commit("base", nil)
		fs := f.eval(map[string]string{main: base, "refs/heads/x": base}, push("refs/heads/x", gitx.ZeroOID))
		expect(t, fs, "REF-DELETE", "")
	})
	t.Run("SIZE totals by ref, per-file by path", func(t *testing.T) {
		f := newReqFixture(t, "rules:\n  SIZE-LARGE: {max_files: 0, max_file_bytes: 1, allow: ['refs/heads/bulk']}\n")
		base := f.repo.Commit("base", nil)
		head := f.repo.Commit("x", map[string]string{"big.txt": "big\n"})
		fs := f.eval(map[string]string{main: base}, push("refs/heads/bulk", head))
		if len(totalFindings(fs, "SIZE-LARGE")) != 0 {
			t.Errorf("REQ-PG-043: SIZE-LARGE total not exempted by ref: %v", fs)
		}
		expect(t, fs, "SIZE-LARGE", "big.txt")
		f2 := f.withRules("rules:\n  SIZE-LARGE: {max_files: 0, max_file_bytes: 1, allow: ['big\\.txt']}\n")
		fs = f2.eval(map[string]string{main: base}, push("refs/heads/bulk", head))
		if len(totalFindings(fs, "SIZE-LARGE")) != 1 || len(paths(fs, "SIZE-LARGE")) != 1 {
			t.Errorf("REQ-PG-043: per-file SIZE-LARGE not exempted by path: %v", fs)
		}
	})
	t.Run("CONTENT and MODE by path", func(t *testing.T) {
		f := newReqFixture(t, "rules:\n  CONTENT-INVISIBLE: {allow: ['a\\.txt']}\n  MODE-EXEC: {allow: ['run']}\n")
		base := f.repo.Commit("base", nil)
		f.repo.WriteMode("run", "#!/bin/sh\n", 0o755)
		f.repo.WriteMode("run2", "#!/bin/sh\n", 0o755)
		head := f.repo.Commit("x", map[string]string{"a.txt": "a\u200bb\n", "b.txt": "a\u200bb\n"})
		fs := f.eval(map[string]string{main: base}, push(main, head))
		wantPaths(t, fs, "CONTENT-INVISIBLE", []string{"a.txt", "b.txt"}, "b.txt")
		wantPaths(t, fs, "MODE-EXEC", []string{"run", "run2"}, "run2")
	})
	t.Run("REF-COUNT ignores allow", func(t *testing.T) {
		dir := t.TempDir()
		testutil.WriteFiles(t, dir, map[string]string{
			"defaults.yaml":          "agent: {name: test-agent}\n",
			"repos/demo/warden.yaml": "remote: /nonexistent/remote.git\nrules:\n  REF-COUNT: {max_refs: 1, allow: ['.*']}\n",
		})
		cfg, err := config.LoadRepo(dir, "demo")
		if err != nil {
			t.Skipf("allow on REF-COUNT rejected at load (acceptable): %v", err)
		}
		f := newReqFixture(t, "")
		f.cfg = cfg
		base := f.repo.Commit("base", nil)
		head := f.repo.Commit("x", map[string]string{"a.txt": "a\n"})
		expect(t, f.eval(map[string]string{main: base}, push(main, head), push("refs/heads/b", head)), "REF-COUNT", "")
	})
}

func TestREQ_PG_044_CumulativeDiffAndPerCommitMeta(t *testing.T) {
	f := newReqFixture(t, "rules:\n  SIZE-LARGE: {max_lines: 10}\n")
	base := f.repo.Commit("base", map[string]string{"keep.txt": "k\n"})
	f.repo.Commit("add", map[string]string{".github/workflows/ci.yml": "on: push\n", "a.txt": "a\u202eb\n"})
	f.repo.Git("rm", "--quiet", ".github/workflows/ci.yml")
	head := f.repo.Commit("remove", map[string]string{"a.txt": "clean\n"})
	fs := f.eval(map[string]string{main: base}, push(main, head))
	expectNone(t, fs, "PATH-RED", "CONTENT-INVISIBLE")

	// size totals still add up over split commits
	f.repo.Commit("six", map[string]string{"s1.txt": lines("s", 6)})
	split := f.repo.Commit("six more", map[string]string{"s2.txt": lines("t", 6)})
	fs = f.eval(map[string]string{main: head}, push(main, split))
	expectTotal(t, fs, "SIZE-LARGE", "12 changed lines (limit 10)", true)

	// metadata is checked per commit: a backdated middle commit is named
	tree := hashObject(t, f.repo, "tree", "", false)
	now := f.now.Add(time.Minute) // after split, which was committed on the wall clock
	c1 := rawCommit(t, f.repo, tree, []string{split}, now.Unix(), now.Unix(), false, "c1")
	c2 := rawCommit(t, f.repo, tree, []string{c1}, now.Add(-48*time.Hour).Unix(), now.Add(-48*time.Hour).Unix(), false, "c2")
	c3 := rawCommit(t, f.repo, tree, []string{c2}, now.Unix(), now.Unix(), false, "c3")
	fs = f.eval(map[string]string{main: split}, push(main, c3))
	for _, x := range findings(fs, "META-BACKDATED") {
		if x.Commit != c2 {
			t.Errorf("REQ-PG-044: META-BACKDATED on %s, want only c2 %s", x.Commit, c2)
		}
	}
	if countRule(fs, "META-BACKDATED") == 0 {
		t.Errorf("REQ-PG-044: backdated middle commit not flagged: %v", fs)
	}
}

func TestREQ_PG_045_KindAndOldFromRemote(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	remoteTip := f.repo.Commit("someone else", map[string]string{"r.txt": "r\n"})
	f.repo.Git("reset", "--quiet", "--hard", base)
	agent := f.repo.Commit("agent", map[string]string{"a.txt": "a\n"})
	remote := map[string]string{main: remoteTip}

	// the agent claims a fast-forward from base; the remote says otherwise
	lie := Update{Ref: main, New: agent, AgentOld: base, Old: base, Kind: FF}
	d := f.normalize(remote, lie)
	if rd := d.Refs[0]; rd.Kind != NonFF || rd.Old != remoteTip {
		t.Errorf("REQ-PG-045: kind %s old %s, want %s from the remote %s", rd.Kind, rd.Old, NonFF, remoteTip)
	}
	expect(t, f.eval(remote, lie), "REF-NON-FF", "")

	// the agent claims to create a ref that exists on the remote
	create := Update{Ref: main, New: agent, AgentOld: gitx.ZeroOID, Kind: Create}
	if rd := f.normalize(remote, create).Refs[0]; rd.Kind != NonFF || rd.Old != remoteTip {
		t.Errorf("REQ-PG-045: claimed create: kind %s old %s", rd.Kind, rd.Old)
	}
	// and to update a ref the remote does not have
	upd := Update{Ref: "refs/tags/v9", New: agent, AgentOld: base, Old: base}
	if rd := f.normalize(remote, upd).Refs[0]; rd.Kind != Create || rd.Old != gitx.ZeroOID {
		t.Errorf("REQ-PG-045: claimed update of a missing ref: kind %s old %s", rd.Kind, rd.Old)
	}
}

func TestREQ_PG_002_FindingString(t *testing.T) {
	for _, c := range []struct {
		f    Finding
		want string
	}{
		{Finding{Rule: "PATH-YELLOW", Ref: main, Path: "go.mod", Message: "m"}, "PATH-YELLOW refs/heads/main go.mod: m"},
		{Finding{Rule: "CONTENT-BLOB", Ref: main, Path: "a.txt", Line: 7, Message: "m"}, "CONTENT-BLOB refs/heads/main a.txt:7: m"},
		{Finding{Rule: "META-FUTURE", Ref: main, Commit: strings.Repeat("ab", 20), Message: "m"}, "META-FUTURE refs/heads/main commit abababababab: m"},
		{Finding{Rule: "REF-COUNT", Message: "11 refs"}, "REF-COUNT: 11 refs"},
		{Finding{Rule: "X", Commit: "abc", Message: "m"}, "X commit abc: m"},
	} {
		if got := c.f.String(); got != c.want {
			t.Errorf("REQ-PG-002: %q, want %q", got, c.want)
		}
	}
}
