//go:build fuzz

package rules

// Fuzz and property tests for the parsers of git's diff output: diff-tree
// --raw -z, --numstat -z, the "+++ b/<path>" header of a patch and the
// "@@ … +c,d @@" hunk header. Run with `go test -tags=fuzz ./internal/rules`
// (seeds only) or `go test -tags=fuzz -fuzz=FuzzParseNumstat ./internal/rules`.
// See docs/test-strategy.md.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/testutil"
	"pgregory.net/rapid"
)

// gitQuote C-quotes a path the way git does with core.quotePath=true:
// control characters, `"`, `\` and every byte >= 0x80 are escaped.
func gitQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			if c < 0x20 || c >= 0x7f {
				fmt.Fprintf(&b, `\%03o`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func needsQuote(p string) bool {
	for i := 0; i < len(p); i++ {
		if c := p[i]; c < 0x20 || c >= 0x7f || c == '"' || c == '\\' {
			return true
		}
	}
	return false
}

func FuzzParseNumstat(f *testing.F) {
	f.Add(uint32(3), uint32(1), false, "a.txt", "", false)
	f.Add(uint32(0), uint32(0), true, "img.png", "", false)
	f.Add(uint32(7), uint32(2), false, "new name.txt", "old\tname.txt", true)
	f.Add(uint32(1), uint32(0), false, "tab\tin\tname", "", false)
	f.Add(uint32(1), uint32(1), true, "b", "a", true)
	f.Fuzz(func(t *testing.T, added, deleted uint32, binary bool, path, old string, rename bool) {
		if path == "" || strings.ContainsRune(path, 0) || strings.ContainsRune(old, 0) || (rename && old == "") {
			t.Skip("git never writes an empty or NUL-containing path")
		}
		stat := fmt.Sprintf("%d\t%d\t", added, deleted)
		if binary {
			stat = "-\t-\t"
		}
		entry := stat + path + "\x00"
		if rename {
			entry = stat + "\x00" + old + "\x00" + path + "\x00"
		}
		// The entry under test sits between two plain entries, so a parser
		// that consumes too much or too little shows up in the neighbours.
		raw := []byte("1\t1\tfirst\x00" + entry + "2\t0\tlast\x00")
		got, err := parseNumstat(raw)
		if err != nil {
			t.Fatalf("parseNumstat(%q): %v", raw, err)
		}
		want := numstat{added: int(added), deleted: int(deleted), binary: binary}
		if binary {
			want = numstat{binary: true}
		}
		if path != "first" && path != "last" && got[path] != want {
			t.Errorf("parseNumstat(%q)[%q] = %+v, want %+v", raw, path, got[path], want)
		}
		if path != "first" && got["last"] != (numstat{added: 2}) {
			t.Errorf("entry after %q misparsed: %+v", entry, got)
		}
		if rename {
			if _, ok := got[old]; ok && old != "first" && old != "last" && old != path {
				t.Errorf("old name %q of a rename got its own entry: %+v", old, got)
			}
		}
	})
}

func FuzzParseNumstatArbitrary(f *testing.F) {
	f.Add([]byte("1\t2\ta\x00-\t-\tb\x00"))
	f.Add([]byte("1\t2\t\x00old\x00new\x00"))
	f.Add([]byte("1\t2\t\x00old"))
	f.Add([]byte("x\t2\ta\x00"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := parseNumstat(raw) // must not panic
		if err == nil {
			for p, s := range got {
				if s.binary && (s.added != 0 || s.deleted != 0) {
					t.Errorf("binary entry %q with counts %+v", p, s)
				}
			}
		}
	})
}

var rawStatuses = []string{"A", "D", "M", "T", "R100", "R075", "C090"}

func FuzzParseRaw(f *testing.F) {
	f.Add(uint8(0), "a.txt", "")
	f.Add(uint8(4), "new name.txt", "old name.txt")
	f.Add(uint8(6), "copy.txt", "orig.txt")
	f.Add(uint8(2), "-dash\tand tab", "")
	f.Fuzz(func(t *testing.T, st uint8, path, old string) {
		status := rawStatuses[int(st)%len(rawStatuses)]
		renameLike := status[0] == 'R' || status[0] == 'C'
		if path == "" || strings.ContainsRune(path, 0) || strings.ContainsRune(old, 0) || (renameLike && old == "") {
			t.Skip("git never writes an empty or NUL-containing path")
		}
		oldBlob, newBlob := strings.Repeat("1", 40), strings.Repeat("2", 40)
		head := ":100644 100755 " + oldBlob + " " + newBlob + " " + status + "\x00"
		entry := head + path + "\x00"
		if renameLike {
			entry = head + old + "\x00" + path + "\x00"
		}
		plain := ":100644 100644 " + oldBlob + " " + newBlob + " M\x00plain\x00"
		got, err := parseRaw([]byte(plain + entry + plain))
		if err != nil {
			t.Fatalf("parseRaw: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d entries, want 3: %+v", len(got), got)
		}
		fc := got[1]
		if fc.Path != path || fc.OldMode != "100644" || fc.NewMode != "100755" || fc.OldBlob != oldBlob || fc.NewBlob != newBlob {
			t.Errorf("entry %q parsed as %+v", entry, fc)
		}
		switch status[0] {
		case 'R':
			if fc.Status != 'R' || fc.OldPath != old {
				t.Errorf("rename parsed as %+v", fc)
			}
		case 'C':
			if fc.Status != 'A' || fc.OldPath != "" {
				t.Errorf("copy shall count as an added file, got %+v", fc)
			}
		default:
			if fc.Status != status[0] || fc.OldPath != "" {
				t.Errorf("status %s parsed as %+v", status, fc)
			}
		}
		if got[2].Path != "plain" {
			t.Errorf("entry after %q misparsed: %+v", entry, got[2])
		}
	})
}

func FuzzParseRawArbitrary(f *testing.F) {
	f.Add([]byte(":100644 100644 1111 2222 M\x00a\x00"))
	f.Add([]byte(":100644 100644 1111 2222 R100\x00a"))
	f.Add([]byte(": \x00"))
	f.Add([]byte("garbage"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = parseRaw(raw) // must not panic
	})
}

func FuzzDiffHeaderPath(f *testing.F) {
	for _, s := range []string{"a.txt", "with space.txt", "ä.txt", "quo\"te", "tab\tx", "dir/sub/x", "b/nested", "dev/null", "\\back", "new\nline"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		_ = diffHeaderPath(p) // arbitrary input must not panic
		if p == "" || strings.ContainsRune(p, 0) {
			return
		}
		if needsQuote(p) {
			// Git C-quotes the whole "b/<path>" and appends a TAB only for
			// paths with a space.
			hdr := gitQuote("b/" + p)
			if strings.ContainsRune(p, ' ') {
				hdr += "\t"
			}
			if got := diffHeaderPath(hdr); got != p {
				t.Errorf("diffHeaderPath(%q) = %q, want %q", hdr, got, p)
			}
			return
		}
		hdr := "b/" + p
		if strings.ContainsRune(p, ' ') {
			hdr += "\t"
		}
		if got := diffHeaderPath(hdr); got != p {
			t.Errorf("diffHeaderPath(%q) = %q, want %q", hdr, got, p)
		}
	})
}

func FuzzHunkStart(f *testing.F) {
	f.Add(uint32(1), uint32(0), true, "")
	f.Add(uint32(42), uint32(3), false, " func main() {")
	f.Add(uint32(0), uint32(0), true, " @@ +9 @@")
	f.Fuzz(func(t *testing.T, c, d uint32, withCount bool, ctx string) {
		h := fmt.Sprintf("@@ -7,2 +%d @@%s", c, ctx)
		if withCount {
			h = fmt.Sprintf("@@ -7,2 +%d,%d @@%s", c, d, ctx)
		}
		if got := hunkStart(h); got != int(c) {
			t.Errorf("hunkStart(%q) = %d, want %d", h, got, c)
		}
		_ = hunkStart(ctx) // arbitrary input must not panic
	})
}

// fileName draws file names from an alphabet that is hard for the parsers:
// spaces, tabs, quotes, backslashes, newlines, non-ASCII, "b/" and "a/"
// prefixes and leading dashes.
func fileName() *rapid.Generator[string] {
	part := rapid.SampledFrom([]string{"a", "b", "x", " ", "\t", "\"", "\\", "\n", "ä", "€", "-", "b/", "a/", ".", "dev", "null", "@@", "+++", "\r"})
	seg := rapid.Custom(func(t *rapid.T) string {
		s := strings.Join(rapid.SliceOfN(part, 1, 5).Draw(t, "parts"), "")
		s = strings.ReplaceAll(s, "/", "_")
		if s == "." || s == ".." || s == ".git" {
			s = "f" + s
		}
		return s
	})
	return rapid.Custom(func(t *rapid.T) string {
		return strings.Join(rapid.SliceOfN(seg, 1, 3).Draw(t, "segments"), "/")
	})
}

// TestPropDiffPipelineFileNames commits files with generated names and checks
// that diffFiles and addedLines report every one of them under its exact name,
// with the right number of added lines.
func TestPropDiffPipelineFileNames(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		names := rapid.SliceOfNDistinct(fileName(), 1, 4, func(s string) string { return strings.ToLower(s) }).Draw(rt, "names")
		// A name that is a directory of another name can't be a file too.
		for _, a := range names {
			for _, b := range names {
				if a != b && strings.HasPrefix(b, a+"/") {
					rt.Skip("file and directory with the same name")
				}
			}
		}
		repo := testutil.Init(t, false)
		base := repo.Commit("base", nil)
		files := map[string]string{}
		want := map[string]int{}
		for i, n := range names {
			k := i + 1
			files[n] = strings.Repeat(fmt.Sprintf("line %d\n", i), k)
			want[n] = k
		}
		head := repo.Commit("odd names", files)
		g := &gitx.Git{Dir: repo.Dir}
		ctx := context.Background()

		fcs, err := diffFiles(ctx, g, base, head)
		if err != nil {
			rt.Fatal(err)
		}
		gotFiles := map[string]int{}
		for _, fc := range fcs {
			gotFiles[fc.Path] = fc.Added
		}
		if fmt.Sprint(gotFiles) != fmt.Sprint(want) {
			rt.Errorf("diffFiles: got %q, want %q", gotFiles, want)
		}

		added, err := addedLines(ctx, g, base, head)
		if err != nil {
			rt.Fatal(err)
		}
		gotLines := map[string]int{}
		for p, ls := range added {
			gotLines[p] = len(ls)
			for j, l := range ls {
				if l.No != j+1 {
					rt.Errorf("%q line %d numbered %d", p, j+1, l.No)
				}
			}
		}
		if fmt.Sprint(gotLines) != fmt.Sprint(want) {
			rt.Errorf("addedLines: got %q, want %q", gotLines, want)
		}
	})
}
