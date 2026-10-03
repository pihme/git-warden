// Package pushguard is the Push Guard program: the pre-receive hook that
// checks and forwards pushes, the human commands, and the HTTP server.
package pushguard

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/rules"
)

// Messages the agent sees.
const (
	MsgInternal    = "internal error, try again later"
	MsgRateLimited = "rate limited, try later"
	MsgUnknownRepo = "rejected: unknown repo"
)

// MsgWaiting is the whole response to a red push.
func MsgWaiting(id string) string { return fmt.Sprintf("rejected: waiting for a human (push %s)", id) }

// Hook is one run of the pre-receive hook.
type Hook struct {
	ConfigDir string
	Repo      string
	GitDir    string    // the guard's bare repository (the hook's working directory)
	In        io.Reader // pre-receive input
	Out       io.Writer // shown to the agent
	Now       time.Time
}

// NewID returns a push id: UTC time plus random suffix.
func NewID(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}

// ParseUpdates reads "old new ref" lines.
func ParseUpdates(r io.Reader) ([]rules.Update, error) {
	var out []rules.Update
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 {
			return nil, fmt.Errorf("malformed pre-receive line %q", sc.Text())
		}
		out = append(out, rules.Update{AgentOld: f[0], New: f[1], Ref: f[2]})
	}
	return out, sc.Err()
}

type run struct {
	h       *Hook
	ctx     context.Context
	id      string
	wall    *config.Config
	cfg     *config.Config
	jr      *journal.Journal
	entries []journal.Entry
	entry   journal.Entry
	g       *gitx.Git
}

func (r *run) say(format string, args ...any) { fmt.Fprintf(r.h.Out, format+"\n", args...) }

// Run executes the hook and returns its exit code.
func (h *Hook) Run(ctx context.Context) int {
	if h.Now.IsZero() {
		h.Now = time.Now().UTC()
	}
	r := &run{h: h, ctx: ctx, id: NewID(h.Now), g: &gitx.Git{Dir: h.GitDir}}
	r.entry = journal.Entry{Time: h.Now, Event: journal.EventPush, ID: r.id, Repo: h.Repo}
	code, err := r.main()
	if err != nil {
		r.entry.Verdict, r.entry.Reason, r.entry.Error = string(config.Red), journal.ReasonInternalError, err.Error()
		r.entry.Forwarded = false
		r.say("%s", MsgInternal)
		code = 1
	}
	if r.jr != nil {
		if jerr := r.jr.Append(r.entry); jerr != nil {
			fallbackLog(r.wall, fmt.Sprintf("push %s: journal: %v (entry error: %s)", r.id, jerr, r.entry.Error))
			if code == 0 {
				// the push went to the remote; the agent's push is accepted anyway
				return 0
			}
		}
	} else if err != nil {
		fallbackLog(r.wall, fmt.Sprintf("push %s: %v", r.id, err))
	}
	return code
}

// fallbackLog records an error when the journal can't be written. Never to
// the agent's output.
func fallbackLog(wall *config.Config, msg string) {
	path := filepath.Join(os.TempDir(), "push-guard-error.log")
	if wall != nil {
		path = filepath.Join(wall.StateDir, "error.log")
		_ = os.MkdirAll(wall.StateDir, 0o700)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), msg)
}

func (r *run) main() (int, error) {
	updates, err := ParseUpdates(r.h.In)
	if err != nil {
		return 1, err
	}
	r.entry.Updates = updates
	if r.wall, err = config.Load(r.h.ConfigDir); err != nil {
		return 1, err
	}
	r.entry.Agent = r.wall.AgentName
	r.jr = journal.Open(r.wall.StateDir)
	if !config.ValidRepoName(r.h.Repo) {
		return r.unknownRepo()
	}
	unlock, err := LockRepo(r.wall.StateDir, r.h.Repo)
	if err != nil {
		return 1, err
	}
	defer unlock()

	r.cfg, err = config.LoadRepo(r.h.ConfigDir, r.h.Repo)
	if errors.Is(err, config.ErrUnknownRepo) {
		return r.unknownRepo()
	}
	if err != nil {
		return 1, err
	}
	ctx, cancel := context.WithTimeout(r.ctx, r.cfg.Timeout)
	defer cancel()
	r.ctx = ctx
	if r.entries, err = r.jr.Read(); err != nil {
		return 1, err
	}
	if limited, err := r.rateLimited(); err != nil || limited {
		return 1, err
	}

	remote, err := gitx.NewRemote(r.g, r.cfg.Remote, r.cfg.Credential)
	if err != nil {
		return 1, err
	}
	refs, err := remote.ListRefs(ctx)
	if err != nil {
		return 1, err
	}
	if err := ensureObjects(ctx, r.g, remote, rules.RemoteOIDs(refs.Refs)); err != nil {
		return 1, err
	}
	defBranch := refs.Head
	if r.cfg.DefaultBranch != "" {
		defBranch = "refs/heads/" + r.cfg.DefaultBranch
	}

	var approved, rest []rules.Update
	for _, u := range updates {
		if journal.OpenApproval(r.entries, r.h.Repo, u.Ref, u.New) {
			approved = append(approved, u)
		} else {
			rest = append(rest, u)
		}
	}
	in := rules.Input{Remote: refs.Refs, DefaultBranch: defBranch, Now: r.h.Now}
	in.Updates = approved
	dApproved, err := rules.Normalize(ctx, r.g, in)
	if err != nil {
		return 1, err
	}
	in.Updates = rest
	dRest, err := rules.Normalize(ctx, r.g, in)
	if err != nil {
		return 1, err
	}
	r.entry.Updates = nil
	for _, rd := range append(append([]*rules.RefDelta{}, dApproved.Refs...), dRest.Refs...) {
		r.entry.Updates = append(r.entry.Updates, rd.Update)
	}

	res := &rules.Result{Allowed: map[string]map[string]bool{}}
	if len(dRest.Refs) > 0 {
		sc, err := rules.NewScanner(r.cfg, r.absGitDir())
		if err != nil {
			return 1, err
		}
		defer sc.Close()
		if res, err = rules.Evaluate(ctx, r.g, r.cfg, dRest, rules.Options{Scanner: sc}); err != nil {
			return 1, err
		}
	}
	findings := res.Findings
	verdict := rules.Verdict(findings)
	streak, err := r.streak(&findings, &verdict)
	if err != nil {
		return 1, err
	}
	r.entry.Findings, r.entry.Verdict = findings, string(verdict)

	switch verdict {
	case config.Green:
		return r.forward(remote, dApproved, dRest, res)
	case config.Yellow:
		r.say("push-guard: rejected (push %s). Fix these findings and push again:", r.id)
		for _, f := range findings {
			r.say("  %s", f.String())
		}
		return 1, nil
	}
	if streak {
		r.entry.Reason = journal.ReasonStreak
	}
	if b, err := writeBundle(ctx, r.g, r.absGitDir(), r.wall.StateDir, r.h.Repo, r.id, dRest); err != nil {
		r.entry.Error = "bundle: " + err.Error()
	} else {
		r.entry.Bundle = b
	}
	kind := "red_push"
	if streak {
		kind = "yellow_streak"
	}
	r.notify(kind, append(dApproved.Commits, dRest.Commits...))
	r.say("%s", MsgWaiting(r.id))
	return 1, nil
}

func (r *run) absGitDir() string {
	if filepath.IsAbs(r.h.GitDir) {
		return r.h.GitDir
	}
	if abs, err := filepath.Abs(r.h.GitDir); err == nil {
		return abs
	}
	return r.h.GitDir
}

func (r *run) unknownRepo() (int, error) {
	r.entry.Verdict, r.entry.Reason = string(config.Red), journal.ReasonUnknownRepo
	r.notify("unknown_repo", nil)
	r.say("%s", MsgUnknownRepo)
	return 1, nil
}

// rateLimited applies RATE-LIMIT. Rate-limited pushes are not evaluated, and
// only the first one of a series warns the human.
func (r *run) rateLimited() (bool, error) {
	rule := r.cfg.Rule("RATE-LIMIT")
	if !rule.Enabled {
		return false, nil
	}
	max, err := rule.Int("max_pushes")
	if err != nil {
		return false, err
	}
	window, err := rule.Duration("window")
	if err != nil {
		return false, err
	}
	n := journal.PushCount(r.entries, r.wall.AgentName, r.h.Now, window)
	if int64(n) < max {
		return false, nil
	}
	r.entry.Findings = []rules.Finding{{Rule: "RATE-LIMIT", Color: rule.Color,
		Message: fmt.Sprintf("%d pushes within %s (limit %d)", n+1, window, max)}}
	r.entry.Verdict, r.entry.Reason = string(rule.Color), journal.ReasonRateLimited
	if last := journal.LastPush(r.entries, r.wall.AgentName); last == nil || last.Reason != journal.ReasonRateLimited {
		r.notify("rate_limited", nil)
	}
	r.say("push-guard: %s", MsgRateLimited)
	return true, nil
}

// streak applies RATE-YELLOW-STREAK and reports whether it made the push red.
func (r *run) streak(findings *[]rules.Finding, verdict *config.Color) (bool, error) {
	rule := r.cfg.Rule("RATE-YELLOW-STREAK")
	if !rule.Enabled {
		return false, nil
	}
	add := func(msg string) {
		*findings = append(*findings, rules.Finding{Rule: rule.ID, Color: rule.Color, Message: msg})
		if rule.Color.Worse(*verdict) {
			*verdict = rule.Color
		}
	}
	if journal.StreakActive(r.entries, r.h.Repo) {
		add("yellow streak active; waiting for a human to approve a SHA or reset the streak")
		return true, nil
	}
	if *verdict != config.Yellow {
		return false, nil
	}
	max, err := rule.Int("max_yellow")
	if err != nil {
		return false, err
	}
	window, err := rule.Duration("window")
	if err != nil {
		return false, err
	}
	n := journal.YellowCount(r.entries, r.h.Repo, r.h.Now, window) + 1
	if int64(n) <= max {
		return false, nil
	}
	add(fmt.Sprintf("%d yellow rejections within %s (limit %d)", n, window, max))
	err = r.jr.Append(journal.Entry{Time: r.h.Now, Event: journal.EventStreak, ID: r.id, Agent: r.wall.AgentName, Repo: r.h.Repo})
	return true, err
}

// ensureObjects makes sure the remote's heads and tags are readable locally,
// fetching missing ones by oid (no ref is updated; inside the hook they land
// in the quarantine and vanish with it).
func ensureObjects(ctx context.Context, g *gitx.Git, remote *gitx.Remote, oids []string) error {
	missing, err := missingObjects(ctx, g, oids)
	if err != nil || len(missing) == 0 {
		return err
	}
	if err := remote.FetchObjects(ctx, missing); err != nil {
		return fmt.Errorf("fetching remote objects: %w", err)
	}
	if missing, err = missingObjects(ctx, g, oids); err != nil {
		return err
	}
	if len(missing) > 0 {
		return fmt.Errorf("remote objects still missing after fetch: %s", strings.Join(missing, " "))
	}
	return nil
}

func missingObjects(ctx context.Context, g *gitx.Git, oids []string) ([]string, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	out, err := g.RunInput(ctx, strings.NewReader(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, l := range gitx.SplitLines(string(out)) {
		if name, ok := strings.CutSuffix(l, " missing"); ok {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// LockRepo takes the per-repo lock in stateDir/locks.
func LockRepo(stateDir, repo string) (func(), error) {
	dir := filepath.Join(stateDir, "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, repo+".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
