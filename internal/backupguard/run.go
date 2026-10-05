package backupguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pihme/git-warden/internal/gitx"
)

// Identity of the journal commits everref writes in the bridge clone.
const (
	committerName  = "git-warden backup-guard"
	committerEmail = "backup-guard@localhost"
	addChunk       = 100 // branches per everref add call
)

// Result is one repo's run; it is also the line appended to backup.jsonl.
type Result struct {
	Time        time.Time         `json:"time"`
	Guard       string            `json:"guard"`               // always "backup"
	Repo        string            `json:"repo,omitempty"`      // empty for a failed preflight
	Preflight   bool              `json:"preflight,omitempty"` // the preflight failed; no repo ran
	Skipped     bool              `json:"skipped,omitempty"`   // another run held the repo's lock
	OK          bool              `json:"ok"`
	Everref     string            `json:"everref,omitempty"` // version
	Branches    int               `json:"branches"`          // on the remote, after exclude_branches
	Excluded    int               `json:"excluded,omitempty"`
	Added       []string          `json:"added,omitempty"`      // newly protected in this run
	AddFailed   map[string]string `json:"add_failed,omitempty"` // branch -> reason
	New         int               `json:"new,omitempty"`        // lineages started (new branch or tag)
	Rewritten   int               `json:"rewritten,omitempty"`  // history rewritten: new lineage
	Deleted     int               `json:"deleted,omitempty"`    // tombstones written
	Retagged    int               `json:"retagged,omitempty"`   // tags moved
	Regressed   int               `json:"regressed,omitempty"`  // tip moved back to an ancestor
	EverrefExit int               `json:"everref_exit"`         // exit code of everref run, -1 if not run
	DurationMS  int64             `json:"duration_ms"`
	Error       string            `json:"error,omitempty"`
	Output      string            `json:"output,omitempty"` // tail of everref's output, on failure only
	Notify      string            `json:"notify,omitempty"` // notify.command error

	addOutput string // output of the everref add calls that still failed singly
}

// Guard runs the Backup Guard for one configuration directory.
type Guard struct {
	Defaults *Defaults
	Everref  string // resolved binary from Preflight
	Version  string // everref version from Preflight
	Out      io.Writer
}

// Preflight checks everything a run needs before anything is touched:
// defaults.yaml, git (2.42 or newer), the everref binary and its major
// version (everref.version), at least one configured repo, and every repo's
// backup.yaml (which also checks the folder name). Any failure is fatal: the
// Backup Guard fails closed rather than pretending a backup happened. Once
// defaults.yaml has loaded, every problem found is reported in one error.
// Preflight records nothing; a run uses PreflightRun.
func Preflight(ctx context.Context, configDir string, out io.Writer) (*Guard, []string, error) {
	g, repos, _, err := preflight(ctx, configDir, out)
	return g, repos, err
}

// PreflightRun is the preflight of backup-guard run. A failure after
// defaults.yaml has loaded is recorded like a failed repo: one line with
// "preflight": true in state_dir/backup.jsonl and a backup_failed warning
// to notify.command. Nothing else is created (no lock, bridge or backup
// repository). If defaults.yaml doesn't load there is nowhere to record it,
// and if the line can't be written the error says so; the run fails either way.
func PreflightRun(ctx context.Context, configDir string, out io.Writer) (*Guard, []string, error) {
	g, repos, d, err := preflight(ctx, configDir, out)
	if err == nil {
		return g, repos, nil
	}
	if d == nil {
		return nil, nil, err
	}
	res := &Result{Time: time.Now().UTC(), Guard: "backup", Preflight: true, EverrefExit: -1, Error: "preflight: " + err.Error()}
	fg := &Guard{Defaults: d, Out: io.Discard}
	if nerr := fg.notify(ctx, res); nerr != nil {
		res.Notify = nerr.Error()
	}
	if jerr := fg.appendJournal(res); jerr != nil {
		return nil, nil, fmt.Errorf("%w; the failure could not be recorded in %s: %v", err, filepath.Join(d.StateDir, "backup.jsonl"), jerr)
	}
	return nil, nil, err
}

func preflight(ctx context.Context, configDir string, out io.Writer) (*Guard, []string, *Defaults, error) {
	d, err := LoadDefaults(configDir)
	if err != nil {
		return nil, nil, nil, err
	}
	var problems []string
	if _, _, err := gitx.CheckGit(ctx); err != nil { // the same git check as the Push Guard's
		problems = append(problems, err.Error())
	}
	bin, version, err := FindEverref(ctx, d.Everref, d.EverrefVersion)
	if err != nil {
		problems = append(problems, err.Error())
	}
	repos, err := Repos(d.Dir)
	switch {
	case err != nil:
		problems = append(problems, err.Error())
	case len(repos) == 0:
		problems = append(problems, fmt.Sprintf("no repos configured: add %s/repos/<name>/%s", d.Dir, RepoFile))
	}
	for _, name := range repos {
		if _, err := LoadRepo(d.Dir, name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	switch len(problems) {
	case 0:
	case 1:
		return nil, nil, d, errors.New(problems[0])
	default:
		return nil, nil, d, fmt.Errorf("%d problems: %s", len(problems), strings.Join(problems, "; "))
	}
	if out == nil {
		out = io.Discard
	}
	return &Guard{Defaults: d, Everref: bin, Version: version, Out: out}, repos, d, nil
}

// RunAll runs the given repos one after the other. It returns an error if
// any repo failed; the others still run.
func (g *Guard) RunAll(ctx context.Context, repos []string) error {
	var failed []string
	for _, name := range repos {
		res := g.RunRepo(ctx, name)
		if res.OK {
			fmt.Fprintf(g.Out, "repo %s: ok: %d branches, %d new, %d rewritten, %d deleted, %d re-tagged\n",
				name, res.Branches, res.New, res.Rewritten, res.Deleted, res.Retagged)
		} else {
			failed = append(failed, name)
			fmt.Fprintf(g.Out, "repo %s: FAILED: %s\n", name, res.Error)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("backup failed for %s", strings.Join(failed, ", "))
	}
	return nil
}

// RunRepo backs up one repo, records the run in backup.jsonl and warns
// through notify.command when it failed.
func (g *Guard) RunRepo(ctx context.Context, name string) *Result {
	start := time.Now()
	res := &Result{Time: start.UTC(), Guard: "backup", Repo: name, Everref: g.Version, EverrefExit: -1}
	err := g.runRepo(ctx, name, res)
	res.DurationMS = time.Since(start).Milliseconds()
	if err == nil && len(res.AddFailed) > 0 {
		names := make([]string, 0, len(res.AddFailed))
		for b := range res.AddFailed {
			names = append(names, b)
		}
		sort.Strings(names)
		err = fmt.Errorf("%d branch(es) could not be protected: %s", len(names), strings.Join(names, ", "))
		res.Output = tail(lastLines(res.addOutput, 40), 4000)
	}
	if err != nil {
		res.Error = err.Error()
		var re *RunError
		if errors.As(err, &re) {
			res.Output = tail(lastLines(re.Output, 40), 4000)
		}
		if nerr := g.notify(ctx, res); nerr != nil {
			res.Notify = nerr.Error()
		}
	} else {
		res.OK = true
	}
	if jerr := g.appendJournal(res); jerr != nil {
		res.OK = false
		res.Error = strings.TrimPrefix(res.Error+"; ", "; ") + "backup.jsonl: " + jerr.Error()
	}
	return res
}

func (g *Guard) runRepo(ctx context.Context, name string, res *Result) (err error) {
	repo, err := LoadRepo(g.Defaults.Dir, name)
	if err != nil {
		return err
	}
	unlock, err := lock(g.Defaults.StateDir, name)
	if errors.Is(err, ErrLocked) {
		res.Skipped = true
		return fmt.Errorf("skipped: another backup-guard run holds %s", lockPath(g.Defaults.StateDir, name))
	}
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, g.Defaults.Timeout)
	defer cancel()
	defer func() {
		// A killed process only reports "exit -1"; say why it was killed.
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timeout %s (defaults.yaml) exceeded, killed: %w", g.Defaults.Timeout, err)
		}
	}()

	remote, err := newRemote(repo)
	if err != nil {
		return err
	}
	env := remote.Git().Environ()
	ev := &Everref{Bin: g.Everref, Env: env}
	bridge, err := ensureBridge(ctx, g.Defaults.StateDir, repo, ev)
	if err != nil {
		return err
	}

	refs, err := remote.ListRefs(ctx)
	if err != nil {
		return fmt.Errorf("list remote refs: %w", err)
	}
	var want []string
	for ref := range refs.Refs {
		b, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok {
			continue
		}
		if repo.Excluded(b) {
			res.Excluded++
			continue
		}
		want = append(want, b)
	}
	sort.Strings(want)
	res.Branches = len(want)
	if len(want) == 0 {
		return errors.New("the remote has no branches (or none outside exclude_branches); refusing to treat that as a backup")
	}

	protected, err := protectedBranches(ctx, bridge)
	if err != nil {
		return err
	}
	var missing []string
	for _, b := range want {
		if !protected[b] {
			missing = append(missing, b)
		}
	}
	res.Added, res.AddFailed, res.addOutput = addBranches(ctx, ev, bridge, missing)

	out, err := ev.Run(ctx, bridge, "run", "--all")
	countEvents(out, res)
	if err != nil {
		var re *RunError
		if errors.As(err, &re) {
			res.EverrefExit = re.ExitCode
		}
		return err
	}
	res.EverrefExit = 0
	return nil
}

func newRemote(repo *Repo) (*gitx.Remote, error) {
	return gitx.NewRemote(&gitx.Git{}, repo.Remote, repo.Credential, repo.CredentialUsername, repo.KnownHosts)
}
