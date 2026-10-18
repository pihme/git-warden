// Package rules is the decision core of the Push Guard: it normalises a push
// into a delta and runs the deterministic rules over it. It knows nothing
// about platforms; everything it needs comes from Git.
package rules

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
)

// Kind is the kind of a ref change, computed against the remote's state.
type Kind string

const (
	Create  Kind = "create"
	Delete  Kind = "delete"
	FF      Kind = "ff"
	NonFF   Kind = "non_ff"
	TagMove Kind = "tag_move"
	Noop    Kind = "noop" // the remote is already there
)

// Update is one ref update of a push.
type Update struct {
	Ref      string `json:"ref"`
	Old      string `json:"old"`                 // remote state (fresh ls-remote), zero if absent
	New      string `json:"new"`                 // what the agent pushed, zero for a delete
	AgentOld string `json:"agent_old,omitempty"` // what the agent claimed as old (not trusted)
	Kind     Kind   `json:"kind"`
}

// IsTag reports whether the update is for a tag.
func (u Update) IsTag() bool { return strings.HasPrefix(u.Ref, "refs/tags/") }

// IsBranch reports whether the update is for a branch.
func (u Update) IsBranch() bool { return strings.HasPrefix(u.Ref, "refs/heads/") }

// Commit is the metadata of one commit.
type Commit struct {
	OID       string
	Parents   []string
	Author    time.Time
	Committer time.Time
	Signed    bool // has a gpgsig header; the signature itself is not verified
	Message   string
}

// FileChange is one entry of the cumulative diff. Lines are the added lines
// of Path. Content rules and the Pages rule read them here.
type FileChange struct {
	Status     byte // A, M, D, R, T (C is treated as A)
	Path       string
	OldPath    string // set for renames
	OldMode    string
	NewMode    string
	OldBlob    string
	NewBlob    string
	Added      int
	Deleted    int
	Binary     bool
	NewSize    int64
	Lines      []Line
	OldLines   int  // lines in the old blob, for SIZE-MASS-DELETE
	OldLinesOK bool // OldLines was read; false means the read was skipped
}

// Paths returns the names a change is checked under (both names for a rename).
func (f FileChange) Paths() []string {
	if f.OldPath != "" && f.OldPath != f.Path {
		return []string{f.OldPath, f.Path}
	}
	return []string{f.Path}
}

// Line is one added line of the cumulative diff.
type Line struct {
	No   int
	Text string
}

// RefDelta is the normalised change of one ref.
type RefDelta struct {
	Update
	OldCommit     string // remote old, peeled to a commit
	NewCommit     string // new, peeled to a commit
	Base          string // base of the cumulative diff (commit or empty tree)
	Files         []FileChange
	Added         map[string][]Line // copy of each change's Lines, keyed by Path
	Commits       []Commit          // new commits reachable from this ref
	HostsRead     bool              // CONTENT-PAGES-SCRIPT looked up hosts in the old tree
	HostsInTree   map[string]bool   // host → already present in the old tree
	LookbackRead  bool              // META-UNSIGNED read the lookback commits
	SignedHistory bool              // those commits are all signed
	NewBytes      int64             // size of all new objects reachable from this ref
}

// Delta is a normalised push.
type Delta struct {
	Refs            []*RefDelta
	Commits         []Commit          // all new commits of the push
	RemoteOIDs      []string          // oids of the remote's heads and tags
	Remote          map[string]string // the remote's refs
	DefaultBranch   string            // e.g. refs/heads/main; empty if unknown
	Now             time.Time
	EmptyTree       string
	ParentCommitter map[string]time.Time // committer time of parents that are not new commits
}

// Input is what Normalize needs.
type Input struct {
	Updates       []Update          // Ref, New and AgentOld; Old and Kind are computed
	Remote        map[string]string // fresh ls-remote of the remote
	DefaultBranch string            // full ref name, empty if unknown
	Now           time.Time
}

// RemoteOIDs returns the sorted unique oids of heads and tags in remote.
func RemoteOIDs(remote map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for name, oid := range remote {
		if (strings.HasPrefix(name, "refs/heads/") || strings.HasPrefix(name, "refs/tags/")) && !seen[oid] {
			seen[oid] = true
			out = append(out, oid)
		}
	}
	sort.Strings(out)
	return out
}

// Normalize computes ref kinds, cumulative diffs, added lines and new commits.
// cfg decides which rule-specific reads to perform: a disabled rule is not read.
func Normalize(ctx context.Context, g *gitx.Git, cfg *config.Config, in Input) (*Delta, error) {
	d := &Delta{Remote: in.Remote, DefaultBranch: in.DefaultBranch, Now: in.Now}
	d.RemoteOIDs = RemoteOIDs(in.Remote)
	empty, err := g.Line(ctx, "hash-object", "-t", "tree", "/dev/null")
	if err != nil {
		return nil, err
	}
	d.EmptyTree = empty
	b := newBatch(g)
	seen := map[string]bool{}
	for _, u := range in.Updates {
		rd := &RefDelta{Update: u}
		rd.Old = in.Remote[u.Ref]
		if rd.Old == "" {
			rd.Old = gitx.ZeroOID
		}
		if err := d.kind(ctx, g, b, rd); err != nil {
			return nil, err
		}
		if rd.Kind != Delete && rd.Kind != Noop {
			if err := d.fill(ctx, g, b, cfg, rd); err != nil {
				return nil, err
			}
		}
		for _, c := range rd.Commits {
			if !seen[c.OID] {
				seen[c.OID] = true
				d.Commits = append(d.Commits, c)
			}
		}
		d.Refs = append(d.Refs, rd)
	}
	if err := d.readParents(ctx, b, cfg); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Delta) kind(ctx context.Context, g *gitx.Git, b *batch, rd *RefDelta) error {
	switch {
	case gitx.IsZero(rd.New) && gitx.IsZero(rd.Old):
		rd.Kind = Noop
		return nil
	case gitx.IsZero(rd.New):
		rd.Kind = Delete
		return nil
	case rd.New == rd.Old:
		rd.Kind = Noop
		return nil
	}
	nc, err := b.peel(ctx, rd.New)
	if err != nil {
		return fmt.Errorf("%s: %w", rd.Ref, err)
	}
	rd.NewCommit = nc
	if gitx.IsZero(rd.Old) {
		rd.Kind = Create
		return nil
	}
	oc, err := b.peel(ctx, rd.Old)
	if err != nil {
		return fmt.Errorf("%s (remote): %w", rd.Ref, err)
	}
	rd.OldCommit = oc
	if rd.IsTag() {
		rd.Kind = TagMove
		return nil
	}
	anc, err := isAncestor(ctx, g, oc, nc)
	if err != nil {
		return err
	}
	if anc {
		rd.Kind = FF
	} else {
		rd.Kind = NonFF
	}
	return nil
}

func isAncestor(ctx context.Context, g *gitx.Git, a, b string) (bool, error) {
	_, err := g.Run(ctx, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if gitx.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

func mergeBase(ctx context.Context, g *gitx.Git, a, b string) (string, error) {
	out, err := g.Line(ctx, "merge-base", a, b)
	if err == nil {
		return out, nil
	}
	if gitx.ExitCode(err) == 1 {
		return "", nil
	}
	return "", err
}

func (d *Delta) fill(ctx context.Context, g *gitx.Git, b *batch, cfg *config.Config, rd *RefDelta) error {
	base := ""
	var err error
	switch rd.Kind {
	case FF:
		base = rd.OldCommit
	case NonFF, TagMove:
		base, err = mergeBase(ctx, g, rd.OldCommit, rd.NewCommit)
	case Create:
		if def, ok := d.Remote[d.DefaultBranch]; ok && d.DefaultBranch != "" {
			var dc string
			if dc, err = b.peel(ctx, def); err == nil {
				base, err = mergeBase(ctx, g, dc, rd.NewCommit)
			}
		}
	}
	if err != nil {
		return err
	}
	if base == "" {
		base = d.EmptyTree
	}
	rd.Base = base

	if rd.Files, err = diffFiles(ctx, g, base, rd.NewCommit); err != nil {
		return err
	}
	if err := b.sizes(ctx, rd.Files); err != nil {
		return err
	}
	added, err := addedLines(ctx, g, base, rd.NewCommit)
	if err != nil {
		return err
	}
	if err := attachLines(rd.Files, added); err != nil {
		return err
	}
	rd.Added = addedByPath(rd.Files)
	revs := revInput([]string{rd.New}, d.RemoteOIDs)
	oids, err := g.RunInput(ctx, strings.NewReader(revs), "rev-list", "--stdin")
	if err != nil {
		return err
	}
	if rd.Commits, err = b.commits(ctx, gitx.SplitLines(string(oids))); err != nil {
		return err
	}
	objs, err := g.RunInput(ctx, strings.NewReader(revs), "rev-list", "--objects", "--stdin")
	if err != nil {
		return err
	}
	var ids []string
	for _, l := range gitx.SplitLines(string(objs)) {
		id, _, _ := strings.Cut(l, " ")
		ids = append(ids, id)
	}
	if rd.IsTag() && rd.New != rd.NewCommit {
		ids = append(ids, rd.New) // the annotated tag object itself
	}
	if rd.NewBytes, err = b.totalSize(ctx, ids); err != nil {
		return err
	}
	if cfg.Rule("SIZE-MASS-DELETE").Enabled {
		del := cfg.Rule("SIZE-MASS-DELETE")
		if err := countOldLines(ctx, g, rd.Files, del.MaxDeletedShare, del.MinLines); err != nil {
			return err
		}
	}
	if err := d.readHosts(ctx, g, cfg, rd); err != nil {
		return err
	}
	return d.readLookback(ctx, g, b, cfg, rd)
}

// diffFiles runs diff-tree --raw and --numstat with rename detection.
func diffFiles(ctx context.Context, g *gitx.Git, base, head string) ([]FileChange, error) {
	raw, err := g.Run(ctx, "diff-tree", "-r", "-z", "-M", "--no-commit-id", "--raw", "--no-abbrev", base, head)
	if err != nil {
		return nil, err
	}
	files, err := parseRaw(raw)
	if err != nil {
		return nil, err
	}
	num, err := g.Run(ctx, "diff-tree", "-r", "-z", "-M", "--no-commit-id", "--numstat", base, head)
	if err != nil {
		return nil, err
	}
	stats, err := parseNumstat(num)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range files {
		s, ok := stats[files[i].Path]
		if !ok {
			return nil, fmt.Errorf("diff-tree: numstat missing for %q", files[i].Path)
		}
		files[i].Added, files[i].Deleted, files[i].Binary = s.added, s.deleted, s.binary
		seen[files[i].Path] = true
	}
	var extra []string
	for path := range stats {
		if !seen[path] {
			extra = append(extra, path)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return nil, fmt.Errorf("diff-tree: numstat path %q has no raw entry", extra[0])
	}
	return files, nil
}

// attachLines puts each patch path's added lines on the matching raw change.
// A patch path with no raw entry fails closed.
func attachLines(files []FileChange, added map[string][]Line) error {
	byPath := map[string]*FileChange{}
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	var extra []string
	for path, lines := range added {
		fc, ok := byPath[path]
		if !ok {
			extra = append(extra, path)
			continue
		}
		fc.Lines = lines
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("diff-tree: patch path %q has no raw entry", extra[0])
	}
	return nil
}

// addedByPath copies each change's lines into the per-path map.
func addedByPath(files []FileChange) map[string][]Line {
	out := map[string][]Line{}
	for _, fc := range files {
		if len(fc.Lines) > 0 {
			out[fc.Path] = fc.Lines
		}
	}
	return out
}

// countOldLines records the old blob's line count for files whose deletion
// share SIZE-MASS-DELETE might report. The candidate test matches the rule.
func countOldLines(ctx context.Context, g *gitx.Git, files []FileChange, share, minLines int64) error {
	for i := range files {
		fc := &files[i]
		if fc.Status != 'M' && fc.Status != 'R' || fc.Binary || int64(fc.Deleted)*100 <= minLines*share {
			continue
		}
		data, err := g.Run(ctx, "cat-file", "blob", fc.OldBlob)
		if err != nil {
			return err
		}
		fc.OldLines = oldLineCount(data)
		fc.OldLinesOK = true
	}
	return nil
}

// oldLineCount counts lines the way a mass-deletion share does: a trailing
// partial line counts as one.
func oldLineCount(data []byte) int {
	n := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// readHosts records, for the Pages branch, whether each external host in the
// added lines is already in the old tree.
func (d *Delta) readHosts(ctx context.Context, g *gitx.Git, cfg *config.Config, rd *RefDelta) error {
	rule := cfg.Rule("CONTENT-PAGES-SCRIPT")
	if !rule.Enabled || cfg.PagesBranch == "" || rd.Ref != "refs/heads/"+cfg.PagesBranch {
		return nil
	}
	hosts := map[string]bool{}
	for _, fc := range rd.Files {
		for _, l := range fc.Lines {
			for _, h := range externalHosts(l.Text) {
				hosts[h] = true
			}
		}
	}
	rd.HostsInTree = map[string]bool{}
	for host := range hosts {
		seen, err := hostInTree(ctx, g, rd.OldCommit, host)
		if err != nil {
			return err
		}
		rd.HostsInTree[host] = seen
	}
	rd.HostsRead = true
	return nil
}

func hostInTree(ctx context.Context, g *gitx.Git, commit, host string) (bool, error) {
	if commit == "" {
		return false, nil
	}
	_, err := g.Run(ctx, "grep", "-q", "-I", "-i", "-F", "-e", host, commit, "--")
	if err == nil {
		return true, nil
	}
	if gitx.ExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// needsLookback reports whether META-UNSIGNED has to read history for rd.
// A new ref or a tag with no default branch on the remote stays quiet.
func needsLookback(d *Delta, rd *RefDelta) bool {
	if rd.OldCommit == "" || rd.Kind == TagMove || rd.IsTag() {
		_, ok := d.Remote[d.DefaultBranch]
		return ok && d.DefaultBranch != ""
	}
	return true
}

func (d *Delta) readLookback(ctx context.Context, g *gitx.Git, b *batch, cfg *config.Config, rd *RefDelta) error {
	rule := cfg.Rule("META-UNSIGNED")
	if !rule.Enabled || rule.Lookback <= 0 || len(rd.Commits) == 0 || !needsLookback(d, rd) {
		return nil
	}
	signed, err := signedHistory(ctx, g, b, d, rd, int(rule.Lookback))
	if err != nil {
		return err
	}
	rd.LookbackRead = true
	rd.SignedHistory = signed
	return nil
}

// signedHistory reports whether the last n first-parent commits of the branch
// (the remote's old state, or the default branch for a new ref) all carry a
// signature. With fewer than n commits of history the rule stays quiet.
func signedHistory(ctx context.Context, g *gitx.Git, b *batch, d *Delta, rd *RefDelta, n int) (bool, error) {
	start := rd.OldCommit
	if start == "" || rd.Kind == TagMove || rd.IsTag() {
		def, ok := d.Remote[d.DefaultBranch]
		if !ok || d.DefaultBranch == "" {
			return false, nil
		}
		var err error
		if start, err = b.peel(ctx, def); err != nil {
			return false, err
		}
	}
	oids, err := g.Lines(ctx, "rev-list", "--first-parent", fmt.Sprintf("--max-count=%d", n), start)
	if err != nil {
		return false, err
	}
	if len(oids) < n {
		return false, nil
	}
	cs, err := b.commits(ctx, oids)
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

// readParents loads the committer time of parents that are not themselves new
// commits, for META-BACKDATED.
func (d *Delta) readParents(ctx context.Context, b *batch, cfg *config.Config) error {
	if !cfg.Rule("META-BACKDATED").Enabled {
		return nil
	}
	have := map[string]bool{}
	for _, c := range d.Commits {
		have[c.OID] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, rd := range d.Refs {
		for _, c := range rd.Commits {
			for _, p := range c.Parents {
				if p != "" && !have[p] && !seen[p] {
					seen[p] = true
					missing = append(missing, p)
				}
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	cs, err := b.commits(ctx, missing)
	if err != nil {
		return err
	}
	d.ParentCommitter = map[string]time.Time{}
	for _, c := range cs {
		d.ParentCommitter[c.OID] = c.Committer
	}
	return nil
}

func parseRaw(raw []byte) ([]FileChange, error) {
	parts := bytes.Split(raw, []byte{0})
	var out []FileChange
	for i := 0; i < len(parts); i++ {
		head := string(parts[i])
		if head == "" {
			continue
		}
		if !strings.HasPrefix(head, ":") {
			return nil, fmt.Errorf("diff-tree: unexpected raw entry %q", head)
		}
		f := strings.Fields(head[1:])
		if len(f) != 5 || i+1 >= len(parts) {
			return nil, fmt.Errorf("diff-tree: malformed raw entry %q", head)
		}
		fc := FileChange{OldMode: f[0], NewMode: f[1], OldBlob: f[2], NewBlob: f[3], Status: f[4][0]}
		i++
		fc.Path = string(parts[i])
		if fc.Status == 'R' || fc.Status == 'C' {
			if i+1 >= len(parts) {
				return nil, fmt.Errorf("diff-tree: malformed rename entry %q", head)
			}
			i++
			fc.OldPath, fc.Path = fc.Path, string(parts[i])
			if fc.Status == 'C' {
				fc.Status, fc.OldPath = 'A', ""
			}
		}
		out = append(out, fc)
	}
	return out, nil
}

type numstat struct {
	added, deleted int
	binary         bool
}

func parseNumstat(raw []byte) (map[string]numstat, error) {
	parts := bytes.Split(raw, []byte{0})
	out := map[string]numstat{}
	for i := 0; i < len(parts); i++ {
		entry := string(parts[i])
		if entry == "" {
			continue
		}
		f := strings.SplitN(entry, "\t", 3)
		if len(f) != 3 {
			return nil, fmt.Errorf("diff-tree: malformed numstat entry %q", entry)
		}
		var s numstat
		if f[0] == "-" && f[1] == "-" {
			s.binary = true
		} else {
			a, err1 := strconv.Atoi(f[0])
			dl, err2 := strconv.Atoi(f[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("diff-tree: malformed numstat entry %q", entry)
			}
			s.added, s.deleted = a, dl
		}
		path := f[2]
		if path == "" { // rename: old and new name follow
			if i+2 >= len(parts) {
				return nil, fmt.Errorf("diff-tree: malformed numstat rename %q", entry)
			}
			path = string(parts[i+2])
			i += 2
		}
		out[path] = s
	}
	return out, nil
}

// diffHeaderPath extracts the path from a git diff "+++ …" / "--- …" payload
// (the text after the "+++ " / "--- " prefix). Git may append a tab and an
// optional timestamp after the path; paths with spaces use that tab as the
// delimiter. Paths with unusual characters are C-quoted.
func diffHeaderPath(p string) string {
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	if strings.HasPrefix(p, `"`) {
		if uq, err := strconv.Unquote(p); err == nil {
			p = uq
		}
	}
	if p == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// maxDiffLine is the longest added line in a push's diff (REQ-PG-049).
const maxDiffLine = 16 << 20

// addedLines parses the -U0 patch of the cumulative diff.
func addedLines(ctx context.Context, g *gitx.Git, base, head string) (map[string][]Line, error) {
	out, err := g.Run(ctx, "diff-tree", "-r", "-p", "-M", "-U0", "--no-commit-id", "--no-color",
		"--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", base, head)
	if err != nil {
		return nil, err
	}
	res := map[string][]Line{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	// An added line of up to maxDiffLine is read; a longer one fails closed
	// (REQ-PG-049). The buffer also holds the leading '+' and the newline.
	sc.Buffer(make([]byte, 64*1024), maxDiffLine+2)
	path, no := "", 0
	inHeader := false
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "diff --git "):
			path, inHeader = "", true
		case inHeader && strings.HasPrefix(l, "+++ "):
			path = diffHeaderPath(strings.TrimPrefix(l, "+++ "))
		case strings.HasPrefix(l, "@@ "):
			inHeader = false
			no = hunkStart(l)
		case inHeader:
		case strings.HasPrefix(l, "+"):
			if path != "" {
				res[path] = append(res[path], Line{No: no, Text: l[1:]})
			}
			no++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// hunkStart returns the first new line number of "@@ -a,b +c,d @@".
func hunkStart(h string) int {
	i := strings.Index(h, " +")
	if i < 0 {
		return 0
	}
	s := h[i+2:]
	if j := strings.IndexAny(s, ", "); j >= 0 {
		s = s[:j]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// ---- cat-file helpers ----

type batch struct{ g *gitx.Git }

func newBatch(g *gitx.Git) *batch { return &batch{g: g} }

// peel resolves oid to a commit; anything else fails closed.
func (b *batch) peel(ctx context.Context, oid string) (string, error) {
	out, err := b.g.Run(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", oid+"^{commit}")
	if err != nil {
		if gitx.ExitCode(err) == 1 {
			return "", fmt.Errorf("object %s is missing or does not point to a commit", oid)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (b *batch) check(ctx context.Context, oids []string) (map[string]int64, error) {
	sizes := map[string]int64{}
	if len(oids) == 0 {
		return sizes, nil
	}
	out, err := b.g.RunInput(ctx, strings.NewReader(strings.Join(oids, "\n")+"\n"),
		"cat-file", "--batch-check=%(objectname) %(objectsize)")
	if err != nil {
		return nil, err
	}
	for _, l := range gitx.SplitLines(string(out)) {
		f := strings.Fields(l)
		if len(f) == 2 && f[1] == "missing" {
			return nil, fmt.Errorf("object %s missing", f[0])
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("cat-file: unexpected %q", l)
		}
		n, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return nil, err
		}
		sizes[f[0]] = n
	}
	return sizes, nil
}

func (b *batch) sizes(ctx context.Context, files []FileChange) error {
	var ids []string
	for _, f := range files {
		if f.Status != 'D' && f.NewMode != "160000" && !gitx.IsZero(f.NewBlob) {
			ids = append(ids, f.NewBlob)
		}
	}
	sizes, err := b.check(ctx, ids)
	if err != nil {
		return err
	}
	for i := range files {
		files[i].NewSize = sizes[files[i].NewBlob]
	}
	return nil
}

func (b *batch) totalSize(ctx context.Context, oids []string) (int64, error) {
	sizes, err := b.check(ctx, oids)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, s := range sizes {
		n += s
	}
	return n, nil
}

// commits reads the metadata of the given commits.
func (b *batch) commits(ctx context.Context, oids []string) ([]Commit, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	out, err := b.g.RunInput(ctx, strings.NewReader(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	var res []Commit
	for range oids {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("cat-file --batch: %w", err)
		}
		f := strings.Fields(header)
		if len(f) != 3 || f[1] != "commit" {
			return nil, fmt.Errorf("cat-file --batch: unexpected %q", strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, err
		}
		body := make([]byte, size+1) // object plus trailing newline
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		c, err := ParseCommit(f[0], body[:size])
		if err != nil {
			return nil, err
		}
		res = append(res, c)
	}
	return res, nil
}

// ParseCommit parses a raw commit object.
func ParseCommit(oid string, raw []byte) (Commit, error) {
	c := Commit{OID: oid}
	head, msg, _ := bytes.Cut(raw, []byte("\n\n"))
	c.Message = string(msg)
	for _, l := range strings.Split(string(head), "\n") {
		key, val, _ := strings.Cut(l, " ")
		switch key {
		case "parent":
			c.Parents = append(c.Parents, val)
		case "author":
			t, err := identTime(val)
			if err != nil {
				return c, fmt.Errorf("commit %s: author: %w", oid, err)
			}
			c.Author = t
		case "committer":
			t, err := identTime(val)
			if err != nil {
				return c, fmt.Errorf("commit %s: committer: %w", oid, err)
			}
			c.Committer = t
		case "gpgsig", "gpgsig-sha256":
			c.Signed = true
		}
	}
	return c, nil
}

// identTime parses the timestamp of "Name <email> 1700000000 +0100".
func identTime(ident string) (time.Time, error) {
	i := strings.LastIndex(ident, ">")
	if i < 0 {
		return time.Time{}, errors.New("malformed ident")
	}
	f := strings.Fields(ident[i+1:])
	if len(f) < 1 {
		return time.Time{}, errors.New("missing timestamp")
	}
	sec, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0).UTC(), nil
}

// revInput is rev-list --stdin input for "<include...> --not <exclude...>".
func revInput(include, exclude []string) string {
	var b strings.Builder
	for _, o := range include {
		b.WriteString(o + "\n")
	}
	if len(exclude) > 0 {
		b.WriteString("--not\n")
		for _, o := range exclude {
			b.WriteString(o + "\n")
		}
	}
	return b.String()
}
