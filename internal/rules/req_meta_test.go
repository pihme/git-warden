package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
)

// metaChain writes n commits on top of parent (may be ""), one second apart,
// starting at start, and returns their ids oldest first.
func metaChain(t *testing.T, f *fixture, parent string, n int, start time.Time, signed bool, tag string) []string {
	t.Helper()
	tree := hashObject(t, f.repo, "tree", "", false)
	var out []string
	for i := 0; i < n; i++ {
		var ps []string
		if parent != "" {
			ps = []string{parent}
		}
		ts := start.Add(time.Duration(i) * time.Second).Unix()
		parent = rawCommit(t, f.repo, tree, ps, ts, ts, signed, fmt.Sprintf("%s %d", tag, i))
		out = append(out, parent)
	}
	return out
}

func unsignedCommits(fs []Finding) []string {
	var out []string
	for _, x := range findings(fs, "META-UNSIGNED") {
		out = append(out, x.Commit)
	}
	return out
}

const quietDates = "  META-BACKDATED: {enabled: false}\n  META-FUTURE: {enabled: false}\n"

func TestREQ_PG_028_UnsignedAfterSignedHistory(t *testing.T) {
	f := newReqFixture(t, "rules:\n  META-UNSIGNED: {lookback: 3}\n"+quietDates)
	start := f.now.Add(-time.Hour)
	signed := metaChain(t, f, "", 3, start, true, "signed")
	tip := signed[2]
	u := metaChain(t, f, tip, 2, start.Add(time.Minute), false, "unsigned")
	fs := f.eval(map[string]string{main: tip}, push(main, u[1]))
	if got := unsignedCommits(fs); len(got) != 2 || !(got[0] == u[0] && got[1] == u[1] || got[0] == u[1] && got[1] == u[0]) {
		t.Fatalf("REQ-PG-028: each unsigned commit is flagged once, got %v (want %v)", got, u)
	}
	expectColor(t, fs, "META-UNSIGNED", config.Yellow)
	if x := expect(t, fs, "META-UNSIGNED", ""); x.Ref != main || !strings.Contains(x.Message, "on main") {
		t.Fatalf("REQ-PG-028: finding %v", x)
	}

	// a signed commit on a signed history is fine
	s := metaChain(t, f, tip, 1, start.Add(time.Minute), true, "more signed")
	expectNone(t, f.eval(map[string]string{main: tip}, push(main, s[0])), "META-UNSIGNED")
}

func TestREQ_PG_028_DefaultLookbackIs20(t *testing.T) {
	f := newReqFixture(t, "rules:\n"+quietDates)
	start := f.now.Add(-time.Hour)
	root := metaChain(t, f, "", 1, start, false, "unsigned root")[0]
	s19 := metaChain(t, f, root, 19, start.Add(time.Second), true, "signed")
	tip19 := s19[18]
	u := metaChain(t, f, tip19, 1, start.Add(time.Minute), false, "unsigned a")
	// 19 signed + an unsigned root: the last 20 are not all signed
	expectNone(t, f.eval(map[string]string{main: tip19}, push(main, u[0])), "META-UNSIGNED")

	tip20 := metaChain(t, f, tip19, 1, start.Add(30*time.Second), true, "signed 20")[0]
	u = metaChain(t, f, tip20, 1, start.Add(time.Minute), false, "unsigned b")
	fs := f.eval(map[string]string{main: tip20}, push(main, u[0]))
	if x := expect(t, fs, "META-UNSIGNED", ""); !strings.Contains(x.Message, "last 20 commits") {
		t.Fatalf("REQ-PG-028: default lookback message %q", x.Message)
	}
}

func TestREQ_PG_029_LookbackSource(t *testing.T) {
	f := newReqFixture(t, "rules:\n  META-UNSIGNED: {lookback: 3}\n"+quietDates)
	start := f.now.Add(-time.Hour)
	signed := metaChain(t, f, "", 3, start, true, "signed")
	tip := signed[2]
	u := metaChain(t, f, tip, 1, start.Add(time.Minute), false, "unsigned")[0]

	// exactly lookback commits of history is enough
	expect(t, f.eval(map[string]string{main: tip}, push(main, u)), "META-UNSIGNED", "")

	// a new branch is measured against the default branch
	fs := f.eval(map[string]string{main: tip}, push("refs/heads/feature", u))
	if x := expect(t, fs, "META-UNSIGNED", ""); x.Ref != "refs/heads/feature" || !strings.Contains(x.Message, "on feature") {
		t.Fatalf("REQ-PG-029: new branch finding %v", x)
	}
	// so is a new tag; its subject is the full ref
	fs = f.eval(map[string]string{main: tip}, push("refs/tags/v1", u))
	if x := expect(t, fs, "META-UNSIGNED", ""); !strings.Contains(x.Message, "on refs/tags/v1") {
		t.Fatalf("REQ-PG-029: new tag finding %v", x)
	}
	// without a known default branch there is no history: quiet
	expectNone(t, f.eval(nil, push("refs/heads/feature", u)), "META-UNSIGNED")

	// fewer than lookback commits: quiet
	short := metaChain(t, f, "", 2, start, true, "short")
	u2 := metaChain(t, f, short[1], 1, start.Add(time.Minute), false, "unsigned 2")[0]
	expectNone(t, f.eval(map[string]string{main: short[1]}, push(main, u2)), "META-UNSIGNED")
}

func TestREQ_PG_029_LookbackFollowsFirstParents(t *testing.T) {
	f := newReqFixture(t, "rules:\n  META-UNSIGNED: {lookback: 3}\n"+quietDates)
	start := f.now.Add(-time.Hour)
	tree := hashObject(t, f.repo, "tree", "", false)
	signed := metaChain(t, f, "", 2, start, true, "signed")
	// an unsigned side commit, newer than everything on the first-parent line
	side := rawCommit(t, f.repo, tree, []string{signed[0]}, start.Add(10*time.Minute).Unix(), start.Add(10*time.Minute).Unix(), false, "side")
	merge := rawCommit(t, f.repo, tree, []string{signed[1], side}, start.Add(11*time.Minute).Unix(), start.Add(11*time.Minute).Unix(), true, "merge")
	u := metaChain(t, f, merge, 1, start.Add(20*time.Minute), false, "unsigned")[0]
	// first parents: merge, signed[1], signed[0] are all signed, so the rule fires
	fs := f.eval(map[string]string{main: merge}, push(main, u))
	if got := unsignedCommits(fs); len(got) != 1 || got[0] != u {
		t.Fatalf("REQ-PG-029: lookback must follow first parents only, got %v", fs)
	}
}

func TestREQ_PG_030_BackdatedBoundaries(t *testing.T) {
	f := newReqFixture(t, "rules:\n  META-FUTURE: {enabled: false}\n")
	now := f.now
	tree := hashObject(t, f.repo, "tree", "", false)
	at := func(parent string, author, committer time.Time, msg string) string {
		return rawCommit(t, f.repo, tree, []string{parent}, author.Unix(), committer.Unix(), false, msg)
	}
	oldBase := metaChain(t, f, "", 1, now.Add(-30*24*time.Hour), false, "old base")[0]
	remote := map[string]string{main: oldBase}

	exact := at(oldBase, now.Add(-24*time.Hour), now.Add(-24*time.Hour), "exactly 24h")
	expectNone(t, f.eval(remote, push(main, exact)), "META-BACKDATED")

	over := at(oldBase, now.Add(-24*time.Hour-time.Second), now.Add(-24*time.Hour-time.Second), "24h and 1s")
	fs := f.eval(remote, push(main, over))
	if x := expect(t, fs, "META-BACKDATED", ""); x.Commit != over || !strings.Contains(x.Message, "before the push") {
		t.Fatalf("REQ-PG-030: finding %v", x)
	}
	expectColor(t, fs, "META-BACKDATED", config.Yellow)

	// only the committer date counts: an old author date is fine
	rebased := at(oldBase, now.Add(-90*24*time.Hour), now, "rebased")
	expectNone(t, f.eval(remote, push(main, rebased)), "META-BACKDATED")

	// committer date vs. parent: equal is fine, one second earlier fires
	p := at(oldBase, now.Add(-time.Hour), now.Add(-time.Hour), "parent")
	same := at(p, now.Add(-time.Hour), now.Add(-time.Hour), "same second")
	expectNone(t, f.eval(map[string]string{main: p}, push(main, same)), "META-BACKDATED")
	earlier := at(p, now, now.Add(-time.Hour-time.Second), "one second earlier")
	fs = f.eval(map[string]string{main: p}, push(main, earlier))
	if x := expect(t, fs, "META-BACKDATED", ""); x.Commit != earlier || !strings.Contains(x.Message, "parent") {
		t.Fatalf("REQ-PG-030: parent finding %v", x)
	}

	// the parent may be part of the same push; only the earlier child fires
	c1 := at(oldBase, now.Add(-time.Hour), now.Add(-time.Hour), "c1")
	c2 := at(c1, now.Add(-time.Hour-time.Second), now.Add(-time.Hour-time.Second), "c2")
	fs = f.eval(remote, push(main, c2))
	if bs := findings(fs, "META-BACKDATED"); len(bs) != 1 || bs[0].Commit != c2 {
		t.Fatalf("REQ-PG-030: want one finding on c2, got %v", bs)
	}
}

func TestREQ_PG_031_FutureBoundaries(t *testing.T) {
	f := newReqFixture(t, "rules:\n  META-BACKDATED: {enabled: false}\n")
	now := f.now
	tree := hashObject(t, f.repo, "tree", "", false)
	base := metaChain(t, f, "", 1, now.Add(-time.Hour), false, "base")[0]
	remote := map[string]string{main: base}
	at := func(author, committer time.Time, msg string) string {
		return rawCommit(t, f.repo, tree, []string{base}, author.Unix(), committer.Unix(), false, msg)
	}
	limit := now.Add(10 * time.Minute)

	expectNone(t, f.eval(remote, push(main, at(limit, limit, "exactly 10m"))), "META-FUTURE")

	for name, c := range map[string]string{
		"both 10m1s":      at(limit.Add(time.Second), limit.Add(time.Second), "both"),
		"committer 10m1s": at(now, limit.Add(time.Second), "committer"),
		"author 11m":      at(now.Add(11*time.Minute), now, "author"),
	} {
		fs := f.eval(remote, push(main, c))
		x := expect(t, fs, "META-FUTURE", "")
		if x.Commit != c {
			t.Errorf("REQ-PG-031 %s: finding on %s", name, x.Commit)
		}
		expectColor(t, fs, "META-FUTURE", config.Yellow)
	}
}

// REQ-PG-030, REQ-PG-031: the date rules check the commits of updated refs
// (not deleted ones), META-FUTURE says what to fix, and an unreadable parent
// fails closed.
func TestREQ_PG_030_031_DateRulesPerRefAndFailClosed(t *testing.T) {
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
	if _, err := f.evalD(&Delta{Refs: []*RefDelta{m}, Commits: []Commit{orphan}}); err == nil || !strings.Contains(err.Error(), "was not read") {
		t.Fatalf("missing parent: %v", err)
	}
}

func TestREQ_PG_028_SignedHistoryErrorsAndDisable(t *testing.T) {
	c := commitD("2222222222222222222222222222222222222222", time.Now())
	rd := refD(main, FF)
	rd.OldCommit = bogusOID
	rd.Commits = []Commit{c}
	d := func() *Delta { return &Delta{Refs: []*RefDelta{rd}, Commits: []Commit{c}} }
	if _, err := newFixture(t, "").evalD(d()); err == nil || !strings.Contains(err.Error(), "lookback") || !strings.Contains(err.Error(), "was not read") {
		t.Fatalf("missing lookback: %v", err)
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
