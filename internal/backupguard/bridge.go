package backupguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/pihme/git-warden/internal/gitx"
)

// Paths of one repo's state.
func repoState(stateDir, name string) (base, bridge, backup string) {
	base = filepath.Join(stateDir, "repos", name)
	return base, filepath.Join(base, "bridge"), filepath.Join(base, "backup.git")
}

// BackupPath is the append-only backup repository of a repo.
func BackupPath(stateDir, name string) string {
	_, _, backup := repoState(stateDir, name)
	return backup
}

// BridgePath is the bridge clone everref runs in (for everref status,
// log and restore by hand).
func BridgePath(stateDir, name string) string {
	_, bridge, _ := repoState(stateDir, name)
	return bridge
}

// ensureBridge creates or checks the bridge clone (remote origin = the
// repo's remote, remote backup = the local backup repo) and enables bridged
// tags. A bridge whose origin points elsewhere than the configuration is an
// error: the backup of one remote is never continued with another.
//
// The backup repository and the bridge are each set up in a temporary
// directory next to their final place and renamed into it only when
// complete, so a run killed during the setup leaves no half-made repository
// that would fail every later run; the next run removes the leftovers and
// starts the setup again. Everything after that (remotes, configuration,
// bridged tags, everref add and run) is checked and redone on every run.
func ensureBridge(ctx context.Context, stateDir string, repo *Repo, ev *Everref) (string, error) {
	base, bridge, backup := repoState(stateDir, repo.Name)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	if err := removeSetupLeftovers(base); err != nil {
		return "", err
	}
	unset := []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}
	git := &gitx.Git{Dir: bridge, Unset: unset}
	if err := createOnce(base, backup, "HEAD", func(tmp string) error {
		_, err := (&gitx.Git{Dir: base, Unset: unset}).Run(ctx, "init", "--quiet", "--bare", tmp)
		return err
	}); err != nil {
		return "", err
	}
	if err := createOnce(base, bridge, ".git", func(tmp string) error {
		if _, err := (&gitx.Git{Dir: base, Unset: unset}).Run(ctx, "init", "--quiet", tmp); err != nil {
			return err
		}
		tg := &gitx.Git{Dir: tmp, Unset: unset}
		if _, err := tg.Run(ctx, "remote", "add", "origin", repo.Remote); err != nil {
			return err
		}
		_, err := tg.Run(ctx, "remote", "add", "backup", backup)
		return err
	}); err != nil {
		return "", err
	}
	if err := checkRemoteURL(ctx, git, "origin", repo.Remote, "remote "+repo.Remote+" in "+RepoFile); err != nil {
		return "", err
	}
	if _, err := git.Run(ctx, "remote", "get-url", "backup"); err != nil {
		if _, err := git.Run(ctx, "remote", "add", "backup", backup); err != nil {
			return "", err
		}
	}
	if err := checkRemoteURL(ctx, git, "backup", backup, "the backup repository"); err != nil {
		return "", err
	}
	// The backup remote is only pushed to; everref expects no fetch refspec.
	if _, err := git.Run(ctx, "config", "--unset-all", "remote.backup.fetch"); err != nil && gitx.ExitCode(err) != 5 {
		return "", err
	}
	for _, kv := range [][2]string{{"user.name", committerName}, {"user.email", committerEmail}, {"commit.gpgSign", "false"}} {
		if _, err := git.Run(ctx, "config", kv[0], kv[1]); err != nil {
			return "", err
		}
	}
	tags, err := git.Lines(ctx, "config", "--get-all", "everref-remote.backup.tags")
	if err != nil && gitx.ExitCode(err) != 1 {
		return "", err
	}
	has := false
	for _, t := range tags {
		has = has || t == "origin"
	}
	if !has {
		if _, err := ev.Run(ctx, bridge, "-q", "tags", "--remote", "backup", "on", "--source", "origin"); err != nil {
			return "", err
		}
	}
	return bridge, nil
}

// setupPrefix names the temporary directories of an unfinished setup.
const setupPrefix = ".setup-"

// createOnce makes sure dir exists and contains marker. If it doesn't, build
// builds it in a temporary directory in base, which is then renamed to dir.
// A dir without marker (left by a version that set up in place) must be
// empty; anything else is refused rather than overwritten.
func createOnce(base, dir, marker string, build func(tmp string) error) error {
	if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		if len(entries) > 0 {
			return fmt.Errorf("%s exists but is not a complete repository (no %s); move it away to set it up again", dir, marker)
		}
		if err := os.Remove(dir); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.MkdirTemp(base, setupPrefix+filepath.Base(dir)+"-")
	if err != nil {
		return err
	}
	if err := build(tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// removeSetupLeftovers removes the temporary directories of setups that a
// killed run didn't finish. They never hold backup data: the backup is only
// written after its repository has been renamed into place.
func removeSetupLeftovers(base string) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), setupPrefix) {
			if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkRemoteURL(ctx context.Context, git *gitx.Git, name, want, what string) error {
	got, err := git.Line(ctx, "remote", "get-url", name)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("bridge %s: remote %s is %s, not %s; refusing to continue this backup with another remote (move the repo's state directory away to start a new one)", git.Dir, name, got, what)
	}
	return nil
}

// protectedBranches returns the origin branches everref already protects.
func protectedBranches(ctx context.Context, bridge string) (map[string]bool, error) {
	git := &gitx.Git{Dir: bridge}
	lines, err := git.Lines(ctx, "config", "--get-regexp", `^everref\.remotes/origin/.*\.remote$`)
	if err != nil && gitx.ExitCode(err) != 1 {
		return nil, err
	}
	out := map[string]bool{}
	for _, l := range lines {
		key, _, _ := strings.Cut(l, " ")
		b := strings.TrimSuffix(strings.TrimPrefix(key, "everref.remotes/origin/"), ".remote")
		if b != "" && b != key {
			out[b] = true
		}
	}
	return out, nil
}

// addBranches protects new branches with everref add, in chunks. A chunk
// that fails is retried branch by branch, so one name everref refuses (it
// reserves some path components) doesn't leave the others unprotected.
func addBranches(ctx context.Context, ev *Everref, bridge string, branches []string) (added []string, failed map[string]string, output string) {
	var outs strings.Builder
	add := func(bs []string) error {
		args := []string{"-q", "add"}
		for _, b := range bs {
			args = append(args, "origin/"+b)
		}
		_, err := ev.Run(ctx, bridge, append(args, "--remote", "backup")...)
		return err
	}
	for len(branches) > 0 {
		n := min(addChunk, len(branches))
		chunk := branches[:n]
		branches = branches[n:]
		if err := add(chunk); err == nil {
			added = append(added, chunk...)
			continue
		}
		for _, b := range chunk {
			if err := add([]string{b}); err != nil {
				if failed == nil {
					failed = map[string]string{}
				}
				msg := err.Error()
				var re *RunError
				if errors.As(err, &re) {
					msg = tail(lastLines(re.Output, 1), 300)
					outs.WriteString(re.Output)
					if !strings.HasSuffix(re.Output, "\n") {
						outs.WriteString("\n")
					}
				}
				failed[b] = msg
			} else {
				added = append(added, b)
			}
		}
	}
	return added, failed, outs.String()
}

// countEvents counts the events everref run reported.
func countEvents(out string, res *Result) {
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "[new lineage]"):
			res.Rewritten++
		case strings.Contains(l, "[new]"):
			res.New++
		case strings.Contains(l, "[re-tagged]"):
			res.Retagged++
		case strings.Contains(l, "[regressed]"):
			res.Regressed++
		case strings.Contains(l, "[deleted]"):
			res.Deleted++
		}
	}
}

// ErrLocked is returned by lock when another run holds the repo's lock.
var ErrLocked = errors.New("locked by another run")

func lockPath(stateDir, repo string) string {
	return filepath.Join(stateDir, "locks", repo+".lock")
}

// lock takes the per-repo lock in stateDir/locks without waiting: if another
// run holds it, lock returns ErrLocked and the repo is skipped (a failure),
// so overlapping runs never pile up behind a slow one.
func lock(stateDir, repo string) (func(), error) {
	path := lockPath(stateDir, repo)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Warning is what notify.command gets as JSON on stdin.
type Warning struct {
	Kind        string    `json:"kind"`  // "backup_failed"
	Guard       string    `json:"guard"` // "backup"
	Time        time.Time `json:"time"`
	Repo        string    `json:"repo,omitempty"`      // empty for a failed preflight
	Preflight   bool      `json:"preflight,omitempty"` // the preflight failed; no repo ran
	Skipped     bool      `json:"skipped,omitempty"`   // another run held the repo's lock
	Error       string    `json:"error"`
	EverrefExit int       `json:"everref_exit"`
	Output      string    `json:"output,omitempty"`
}

func (g *Guard) notify(ctx context.Context, res *Result) error {
	cmdline := g.Defaults.NotifyCommand
	if len(cmdline) == 0 {
		return nil
	}
	w := Warning{Kind: "backup_failed", Guard: "backup", Time: res.Time, Repo: res.Repo, Preflight: res.Preflight, Skipped: res.Skipped,
		Error: res.Error, EverrefExit: res.EverrefExit, Output: res.Output}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdline[0], cmdline[1:]...)
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify.command: %w: %s", err, truncate(strings.TrimSpace(out.String()), 300))
	}
	return nil
}

// appendJournal appends res as one line to stateDir/backup.jsonl.
func (g *Guard) appendJournal(res *Result) error {
	if err := os.MkdirAll(g.Defaults.StateDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(g.Defaults.StateDir, "backup.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// tail keeps the end of s, at most n bytes plus a leading "…": the end of
// everref's output says what failed. The cut is moved forward to a UTF-8
// boundary, and to the next line start if that keeps something.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	if i := strings.IndexByte(s[cut:], '\n'); i >= 0 && cut+i+1 < len(s) {
		cut += i + 1
	}
	return "…" + s[cut:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
