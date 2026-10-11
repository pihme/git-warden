package rules

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Unit tests for the parsers and helpers in delta.go that no requirement
// names. Rejected inputs (fail closed) are in TestREQ_PG_004_Parsers.

// Long added lines are kept whole, and content starting with "++" is not
// taken for a diff header.
func TestAddedLines_LongLinesAndPlusPrefixedContent(t *testing.T) {
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

func TestPathsAndRemoteOIDs(t *testing.T) {
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

func TestParseRaw(t *testing.T) {
	const h = ":000000 100644 0000000000000000000000000000000000000000 45b983be36b73c0788dc9cbcb76cbb80fc7bb057 "
	fs, err := parseRaw([]byte("\x00" + h + "A\x00p\x00\x00" + h + "R100\x00old\x00new"))
	if err != nil || len(fs) != 2 || fs[0].Path != "p" || fs[1].OldPath != "old" || fs[1].Path != "new" {
		t.Fatalf("parseRaw = %+v, %v", fs, err)
	}
	if fs, err := parseRaw([]byte(h + "A\x00p")); err != nil || len(fs) != 1 {
		t.Errorf("entry without trailing NUL: %v, %v", fs, err)
	}
}

func TestParseNumstat(t *testing.T) {
	got, err := parseNumstat([]byte("\x001\t2\tp\x00-\t-\tbin\x003\t4\t\x00old\x00new"))
	want := map[string]numstat{"p": {1, 2, false}, "bin": {0, 0, true}, "new": {3, 4, false}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNumstat = %v, %v", got, err)
	}
}

func TestDiffHeaderPathAndHunkStart(t *testing.T) {
	if got := diffHeaderPath("\tfoo"); got != "" {
		t.Errorf("diffHeaderPath(tab first) = %q", got)
	}
	for h, want := range map[string]int{" +5": 5, "x5": 0, "@@ -1 ++5 @@": 5, "@@ -0,0 +5": 5, "@@ -1,2 +7,3 @@": 7} {
		if got := hunkStart(h); got != want {
			t.Errorf("hunkStart(%q) = %d, want %d", h, got, want)
		}
	}
}

func TestParseCommit(t *testing.T) {
	for ident, want := range map[string]int64{
		"> 5 +0000":                       5,
		"a <b> 1700000000":                1700000000,
		"a <b> 5000000000000000000 +0000": 5000000000000000000,
	} {
		if got, err := identTime(ident); err != nil || got.Unix() != want {
			t.Errorf("identTime(%q) = %v, %v", ident, got, err)
		}
	}
}
