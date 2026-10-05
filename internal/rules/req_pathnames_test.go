package rules

// Line-based CONTENT rules must see added lines in files with unusual names:
// spaces (git appends a TAB to "+++ b/<path>" then), non-ASCII characters,
// quotes and tabs (git C-quotes those paths). See docs/push-guard-testspec.md.

import (
	"strings"
	"testing"
)

var oddNames = []string{"with space.txt", "dir with space/x.txt", "ä.txt", "quo\"te.txt", "tab\tx.txt"}

func oddNameFiles(content string) map[string]string {
	m := map[string]string{}
	for _, n := range oddNames {
		m[n] = content
	}
	return m
}

func TestREQ_PG_021_ScannerAllowInOddFileNames(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("odd", oddNameFiles("x = 1 // gitleaks:allow\n"))
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-SCANNER-ALLOW", oddNames, oddNames...)
}

func TestREQ_PG_022_InvisibleInOddFileNames(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("odd", oddNameFiles("x = 1 \u202e// hidden\n"))
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-INVISIBLE", oddNames, oddNames...)
}

func TestREQ_PG_025_BlobInOddFileNames(t *testing.T) {
	f := newReqFixture(t, "")
	base := f.repo.Commit("base", nil)
	head := f.repo.Commit("odd", oddNameFiles("k = "+strings.Repeat("QUJD", 60)[:220]+"\n"))
	fs := f.eval(map[string]string{main: base}, push(main, head))
	wantPaths(t, fs, "CONTENT-BLOB", oddNames, oddNames...)
}

func TestREQ_PG_026_PagesScriptInOddFileNames(t *testing.T) {
	f := newReqFixture(t, "pages_branch: site\n")
	base := f.repo.Commit("base", map[string]string{"index.html": "<p>hi</p>\n"})
	head := f.repo.Commit("odd", oddNameFiles(newHostPage))
	fs := f.eval(map[string]string{"refs/heads/site": base}, push("refs/heads/site", head))
	wantPaths(t, fs, "CONTENT-PAGES-SCRIPT", oddNames, oddNames...)
}
