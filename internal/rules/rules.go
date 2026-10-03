package rules

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
)

// Finding is one rule hit.
type Finding struct {
	Rule    string       `json:"rule"`
	Color   config.Color `json:"color"`
	Ref     string       `json:"ref,omitempty"`
	Path    string       `json:"path,omitempty"`
	Line    int          `json:"line,omitempty"`
	Commit  string       `json:"commit,omitempty"`
	Message string       `json:"message"`
}

// String formats a finding as one line for the agent.
func (f Finding) String() string {
	var b strings.Builder
	b.WriteString(f.Rule)
	if f.Ref != "" {
		b.WriteString(" " + f.Ref)
	}
	if f.Path != "" {
		b.WriteString(" " + f.Path)
		if f.Line > 0 {
			fmt.Fprintf(&b, ":%d", f.Line)
		}
	}
	if f.Commit != "" {
		b.WriteString(" commit " + short(f.Commit))
	}
	b.WriteString(": " + f.Message)
	return b.String()
}

// Result is the outcome of Evaluate.
type Result struct {
	Findings []Finding
	// Allowed records ref rules that were triggered but exempted by allow,
	// per rule ID and ref. Only these refs may be forwarded with a lease.
	Allowed map[string]map[string]bool
}

// IsAllowed reports whether rule was triggered for ref and exempted by allow.
func (r *Result) IsAllowed(rule, ref string) bool { return r.Allowed[rule][ref] }

// Verdict is the worst color of the findings, green if there are none.
func Verdict(findings []Finding) config.Color {
	v := config.Green
	for _, f := range findings {
		if f.Color.Worse(v) {
			v = f.Color
		}
	}
	return v
}

// Options tune an evaluation.
type Options struct {
	Scanner     *Scanner // nil skips CONTENT-SECRET (replay --skip-scanner only)
	SkipScanner bool
}

type eval struct {
	ctx  context.Context
	g    *gitx.Git
	cfg  *config.Config
	d    *Delta
	res  *Result
	seen map[string]bool
}

// Evaluate runs all deterministic rules except RATE-*, which need the journal.
func Evaluate(ctx context.Context, g *gitx.Git, cfg *config.Config, d *Delta, opt Options) (*Result, error) {
	e := &eval{ctx: ctx, g: g, cfg: cfg, d: d, res: &Result{Allowed: map[string]map[string]bool{}}, seen: map[string]bool{}}
	steps := []func() error{e.refRules, e.pathRules, e.modeRules, e.contentRules, e.pagesRule, e.metaRules, e.sizeRules}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	if !opt.SkipScanner {
		if opt.Scanner == nil {
			return nil, fmt.Errorf("no secret scanner configured")
		}
		if err := e.secretRule(opt.Scanner); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(e.res.Findings, func(i, j int) bool {
		a, b := e.res.Findings[i], e.res.Findings[j]
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Rule < b.Rule
	})
	return e.res, nil
}

// check decides one subject of rule id and records a finding if it fires.
func (e *eval) check(id, subject string, triggered bool, f Finding) {
	r := e.cfg.Rule(id)
	if !r.Fires(subject, triggered) {
		if triggered && r.Allowed(subject) && f.Ref != "" {
			if e.res.Allowed[id] == nil {
				e.res.Allowed[id] = map[string]bool{}
			}
			e.res.Allowed[id][f.Ref] = true
		}
		return
	}
	f.Rule, f.Color = id, r.Color
	key := fmt.Sprintf("%s|%s|%s|%d|%s|%s", f.Rule, f.Ref, f.Path, f.Line, f.Commit, f.Message)
	if e.seen[key] {
		return
	}
	e.seen[key] = true
	e.res.Findings = append(e.res.Findings, f)
}

// fire records a finding for a rule without subject (allow/deny ignored).
func (e *eval) fire(id string, f Finding) {
	r := e.cfg.Rule(id)
	if !r.Enabled {
		return
	}
	f.Rule, f.Color = id, r.Color
	e.res.Findings = append(e.res.Findings, f)
}

func (e *eval) refRules() error {
	max, err := e.cfg.Rule("REF-COUNT").Int("max_refs")
	if err != nil {
		return err
	}
	if n := len(e.d.Refs); int64(n) > max {
		e.fire("REF-COUNT", Finding{Message: fmt.Sprintf("%d refs in one push (limit %d)", n, max)})
	}
	for _, rd := range e.d.Refs {
		ref := rd.Ref
		f := Finding{Ref: ref}
		ns := !rd.IsBranch() && !rd.IsTag()
		f.Message = "ref outside refs/heads/* and refs/tags/*"
		if !ns {
			f.Message = "pushes to this ref are reserved for a human"
		}
		e.check("REF-NAMESPACE", ref, ns, f)
		switch rd.Kind {
		case Delete:
			e.check("REF-DELETE", ref, true, Finding{Ref: ref, Message: "ref is deleted"})
		case NonFF:
			e.check("REF-NON-FF", ref, true, Finding{Ref: ref, Message: "history is rewritten (not a fast-forward of the remote's " + short(rd.Old) + ")"})
		case TagMove:
			e.check("REF-TAG-MOVE", ref, true, Finding{Ref: ref, Message: "existing tag is moved"})
		case Create:
			if rd.IsTag() {
				e.check("REF-TAG-NEW", ref, true, Finding{Ref: ref, Message: "new tags can trigger releases; leave tagging to a human"})
			}
		}
	}
	return nil
}

func (e *eval) pathRules() error {
	for _, rd := range e.d.Refs {
		for _, fc := range rd.Files {
			for _, p := range fc.Paths() {
				for _, id := range []string{"PATH-RED", "PATH-YELLOW"} {
					r := e.cfg.Rule(id)
					msg := "this path is reserved for a human"
					if id == "PATH-YELLOW" {
						msg = "dependency, build, hook or Git metadata file; leave the change out and note it in the PR"
					}
					e.check(id, p, r.Matches(p), Finding{Ref: rd.Ref, Path: p, Message: msg})
				}
			}
		}
	}
	return nil
}

func (e *eval) modeRules() error {
	for _, rd := range e.d.Refs {
		for _, fc := range rd.Files {
			if fc.Status == 'D' {
				continue
			}
			f := Finding{Ref: rd.Ref, Path: fc.Path}
			switch fc.NewMode {
			case "100755":
				if fc.OldMode != "100755" {
					f.Message = "file becomes executable (mode 100755); commit it as 100644"
					e.check("MODE-EXEC", fc.Path, true, f)
				}
			case "120000":
				f.Message = "symlink added or changed"
				e.check("MODE-SYMLINK", fc.Path, true, f)
			case "160000":
				f.Message = "submodule (gitlink) added or changed"
				e.check("MODE-SUBMODULE", fc.Path, true, f)
			}
		}
	}
	return nil
}

// invisible reports whether r is a bidi control, zero-width character, BOM or
// Unicode tag character.
func invisible(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF:
		return true
	case r >= 0xE0000 && r <= 0xE007F:
		return true
	}
	return false
}

func (e *eval) contentRules() error {
	blob := e.cfg.Rule("CONTENT-BLOB")
	minB64, err := blob.Int("min_base64")
	if err != nil {
		return err
	}
	minHex, err := blob.Int("min_hex")
	if err != nil {
		return err
	}
	for _, rd := range e.d.Refs {
		for _, fc := range rd.Files {
			if fc.Status == 'D' || fc.NewMode == "160000" || fc.NewMode == "120000" {
				continue
			}
			lines := rd.Added[fc.Path]
			binary := fc.Binary
			msg := "binary file added or changed"
			if !binary {
				for _, l := range lines {
					if !utf8.ValidString(l.Text) {
						binary, msg = true, "file is not valid UTF-8 text"
						break
					}
				}
			}
			if binary {
				e.check("CONTENT-BINARY", fc.Path, true, Finding{Ref: rd.Ref, Path: fc.Path, Message: msg})
				continue
			}
			for _, l := range lines {
				f := Finding{Ref: rd.Ref, Path: fc.Path, Line: l.No}
				lower := strings.ToLower(l.Text)
				if strings.Contains(lower, "gitleaks:allow") || strings.Contains(lower, "trufflehog:ignore") {
					f.Message = "inline exception for the secret scanner"
					e.check("CONTENT-SCANNER-ALLOW", fc.Path, true, f)
				}
				for i, r := range l.Text {
					if invisible(r) && !(r == 0xFEFF && l.No == 1 && i == 0) {
						f.Message = fmt.Sprintf("invisible or bidi control character U+%04X", r)
						e.check("CONTENT-INVISIBLE", fc.Path, true, f)
						break
					}
				}
				if kind, n := longLiteral(l.Text, int(minB64), int(minHex)); kind != "" {
					limit := minB64
					if kind == "hex" {
						limit = minHex
					}
					f.Message = fmt.Sprintf("%s literal of %d characters (limit %d); keep generated or encoded data out of the push", kind, n, limit)
					e.check("CONTENT-BLOB", fc.Path, true, f)
				}
			}
		}
	}
	return nil
}

// longLiteral finds the longest run of hex or base64 characters on a line.
func longLiteral(s string, minB64, minHex int) (string, int) {
	isHex := func(c byte) bool {
		return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
	}
	isB64 := func(c byte) bool {
		return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c == '+' || c == '/' || c == '-' || c == '_' || c == '='
	}
	hexRun, b64Run, maxHex, maxB64 := 0, 0, 0, 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isHex(c) {
			hexRun++
		} else {
			hexRun = 0
		}
		if isB64(c) {
			b64Run++
		} else {
			b64Run = 0
		}
		maxHex, maxB64 = max(maxHex, hexRun), max(maxB64, b64Run)
	}
	if maxHex >= minHex {
		return "hex", maxHex
	}
	if maxB64 >= minB64 {
		return "base64", maxB64
	}
	return "", 0
}

var (
	scriptSrc = regexp.MustCompile(`(?i)<script\b[^>]*?\bsrc\s*=\s*["']?([^"'\s>]+)`)
	linkHref  = regexp.MustCompile(`(?i)<link\b[^>]*?\bhref\s*=\s*["']?([^"'\s>]+)`)
	urlHost   = regexp.MustCompile(`(?i)^(?:https?:)?//([^/?#:]+)`)
)

func (e *eval) pagesRule() error {
	if e.cfg.PagesBranch == "" {
		return nil
	}
	pages := "refs/heads/" + e.cfg.PagesBranch
	for _, rd := range e.d.Refs {
		if rd.Ref != pages || rd.Kind == Delete || rd.Kind == Noop {
			continue
		}
		known := map[string]bool{}
		for _, fc := range rd.Files {
			for _, l := range rd.Added[fc.Path] {
				var urls []string
				for _, re := range []*regexp.Regexp{scriptSrc, linkHref} {
					for _, m := range re.FindAllStringSubmatch(l.Text, -1) {
						urls = append(urls, m[1])
					}
				}
				for _, u := range urls {
					m := urlHost.FindStringSubmatch(u)
					if m == nil {
						continue
					}
					host := strings.ToLower(m[1])
					seen, ok := known[host]
					if !ok {
						var err error
						if seen, err = e.hostInTree(rd.OldCommit, host); err != nil {
							return err
						}
						known[host] = seen
					}
					if !seen {
						e.check("CONTENT-PAGES-SCRIPT", fc.Path, true, Finding{Ref: rd.Ref, Path: fc.Path, Line: l.No,
							Message: "script or stylesheet from new external host " + host + " on the Pages branch"})
					}
				}
			}
		}
	}
	return nil
}

func (e *eval) hostInTree(commit, host string) (bool, error) {
	if commit == "" {
		return false, nil
	}
	_, err := e.g.Run(e.ctx, "grep", "-q", "-I", "-i", "-F", "-e", host, commit, "--")
	if err == nil {
		return true, nil
	}
	if gitx.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

func branchSubject(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

func (e *eval) metaRules() error {
	unsigned := e.cfg.Rule("META-UNSIGNED")
	lookback, err := unsigned.Int("lookback")
	if err != nil {
		return err
	}
	maxAge, err := e.cfg.Rule("META-BACKDATED").Duration("max_age")
	if err != nil {
		return err
	}
	maxSkew, err := e.cfg.Rule("META-FUTURE").Duration("max_skew")
	if err != nil {
		return err
	}
	b := newBatch(e.g)
	parentTimes := map[string]time.Time{}
	for _, c := range e.d.Commits {
		parentTimes[c.OID] = c.Committer
	}
	for _, rd := range e.d.Refs {
		if len(rd.Commits) == 0 {
			continue
		}
		subj := branchSubject(rd.Ref)
		signedHistory := false
		if unsigned.Enabled && lookback > 0 {
			if signedHistory, err = e.signedHistory(b, rd, int(lookback)); err != nil {
				return err
			}
		}
		for _, c := range rd.Commits {
			f := Finding{Ref: rd.Ref, Commit: c.OID}
			if signedHistory && !c.Signed {
				f.Message = fmt.Sprintf("commit is not signed, but the last %d commits on %s are", lookback, subj)
				e.check("META-UNSIGNED", subj, true, f)
			}
			for _, p := range c.Parents {
				pt, ok := parentTimes[p]
				if !ok {
					pcs, err := b.commits(e.ctx, []string{p})
					if err != nil {
						return err
					}
					pt = pcs[0].Committer
					parentTimes[p] = pt
				}
				if c.Committer.Before(pt) {
					f.Message = "committer date is earlier than its parent's; recommit with the current date"
					e.check("META-BACKDATED", subj, true, f)
				}
			}
			if c.Committer.Before(e.d.Now.Add(-maxAge)) {
				f.Message = fmt.Sprintf("committer date is more than %s before the push; recommit with the current date", maxAge)
				e.check("META-BACKDATED", subj, true, f)
			}
			if c.Author.After(e.d.Now.Add(maxSkew)) || c.Committer.After(e.d.Now.Add(maxSkew)) {
				f.Message = fmt.Sprintf("author or committer date is more than %s after the push; fix the clock and recommit", maxSkew)
				e.check("META-FUTURE", subj, true, f)
			}
		}
	}
	return nil
}

// signedHistory reports whether the last n first-parent commits of the branch
// (the remote's old state, or the default branch for a new ref) all carry a
// signature. With fewer than n commits of history the rule stays quiet.
func (e *eval) signedHistory(b *batch, rd *RefDelta, n int) (bool, error) {
	start := rd.OldCommit
	if start == "" || rd.Kind == TagMove || rd.IsTag() {
		def, ok := e.d.Remote[e.d.DefaultBranch]
		if !ok || e.d.DefaultBranch == "" {
			return false, nil
		}
		var err error
		if start, err = b.peel(e.ctx, def); err != nil {
			return false, err
		}
	}
	oids, err := e.g.Lines(e.ctx, "rev-list", "--first-parent", fmt.Sprintf("--max-count=%d", n), start)
	if err != nil {
		return false, err
	}
	if len(oids) < n {
		return false, nil
	}
	cs, err := b.commits(e.ctx, oids)
	if err != nil {
		return false, err
	}
	for _, c := range cs {
		if !c.Signed {
			return false, nil
		}
	}
	return true, nil
}

func (e *eval) sizeRules() error {
	read := func(id string, keys ...string) (map[string]int64, error) {
		out := map[string]int64{}
		for _, k := range keys {
			v, err := e.cfg.Rule(id).Int(k)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	}
	hard, err := read("SIZE-LIMIT", "max_files", "max_lines", "max_bytes", "max_file_bytes")
	if err != nil {
		return err
	}
	soft, err := read("SIZE-LARGE", "max_files", "max_lines", "max_commits", "max_file_bytes")
	if err != nil {
		return err
	}
	del, err := read("SIZE-MASS-DELETE", "max_deleted_files", "max_deleted_share", "min_lines")
	if err != nil {
		return err
	}
	for _, rd := range e.d.Refs {
		if rd.Kind == Delete || rd.Kind == Noop {
			continue
		}
		var lines, deleted int64
		for _, fc := range rd.Files {
			lines += int64(fc.Added + fc.Deleted)
			if fc.Status == 'D' {
				deleted++
			}
		}
		totals := map[string]int64{"max_files": int64(len(rd.Files)), "max_lines": lines,
			"max_bytes": rd.NewBytes, "max_commits": int64(len(rd.Commits))}
		names := map[string]string{"max_files": "changed files", "max_lines": "changed lines",
			"max_bytes": "bytes of new objects", "max_commits": "new commits"}
		for _, rule := range []struct {
			id string
			l  map[string]int64
		}{{"SIZE-LIMIT", hard}, {"SIZE-LARGE", soft}} {
			for _, k := range []string{"max_files", "max_lines", "max_bytes", "max_commits"} {
				limit, ok := rule.l[k]
				if ok && totals[k] > limit {
					e.check(rule.id, rd.Ref, true, Finding{Ref: rd.Ref,
						Message: fmt.Sprintf("%d %s (limit %d); split the change into smaller pushes", totals[k], names[k], limit)})
				}
			}
			for _, fc := range rd.Files {
				if fc.Status != 'D' && fc.NewSize > rule.l["max_file_bytes"] {
					e.check(rule.id, fc.Path, true, Finding{Ref: rd.Ref, Path: fc.Path,
						Message: fmt.Sprintf("file has %d bytes (limit %d)", fc.NewSize, rule.l["max_file_bytes"])})
				}
			}
		}
		if deleted > del["max_deleted_files"] {
			e.check("SIZE-MASS-DELETE", rd.Ref, true, Finding{Ref: rd.Ref,
				Message: fmt.Sprintf("%d files deleted (limit %d)", deleted, del["max_deleted_files"])})
		}
		if err := e.massDeleteShare(rd, del["max_deleted_share"], del["min_lines"]); err != nil {
			return err
		}
	}
	return nil
}

func (e *eval) massDeleteShare(rd *RefDelta, share, minLines int64) error {
	for _, fc := range rd.Files {
		if fc.Status != 'M' && fc.Status != 'R' || fc.Binary || int64(fc.Deleted)*100 <= minLines*share {
			continue
		}
		data, err := e.g.Run(e.ctx, "cat-file", "blob", fc.OldBlob)
		if err != nil {
			return err
		}
		old := int64(strings.Count(string(data), "\n"))
		if len(data) > 0 && data[len(data)-1] != '\n' {
			old++
		}
		if old > minLines && int64(fc.Deleted)*100 > old*share {
			e.check("SIZE-MASS-DELETE", fc.Path, true, Finding{Ref: rd.Ref, Path: fc.Path,
				Message: fmt.Sprintf("%d of the file's %d lines removed (limit %d%%)", fc.Deleted, old, share)})
		}
	}
	return nil
}

func short(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}
