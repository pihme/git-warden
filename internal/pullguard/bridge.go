package pullguard

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

func lookGit() (string, error) {
	p, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("git not found: %w", err)
	}
	return p, nil
}

// ensureBridge creates or checks the bridge clone (remote origin = the
// repo's remote, remote backup = the local backup repo) and enables bridged
// tags. A bridge whose origin points elsewhere than the configuration is an
// error: the backup of one remote is never continued with another.
func ensureBridge(ctx context.Context, stateDir string, repo *Repo, ev *Everref) (string, error) {
	base, bridge, backup := repoState(stateDir, repo.Name)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	git := &gitx.Git{Dir: bridge, Unset: []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}}
	if _, err := os.Stat(filepath.Join(backup, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if _, err := (&gitx.Git{Dir: base, Unset: git.Unset}).Run(ctx, "init", "--quiet", "--bare", backup); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(filepath.Join(bridge, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := (&gitx.Git{Dir: base, Unset: git.Unset}).Run(ctx, "init", "--quiet", bridge); err != nil {
			return "", err
		}
		if _, err := git.Run(ctx, "remote", "add", "origin", repo.Remote); err != nil {
			return "", err
		}
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
func addBranches(ctx context.Context, ev *Everref, bridge string, branches []string) (added []string, failed map[string]string) {
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
					msg = truncate(lastLines(re.Output, 1), 300)
				}
				failed[b] = msg
			} else {
				added = append(added, b)
			}
		}
	}
	return added, failed
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

// lock takes the per-repo lock in stateDir/locks.
func lock(stateDir, repo string) (func(), error) {
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

// Warning is what notify.command gets as JSON on stdin.
type Warning struct {
	Kind        string    `json:"kind"`  // "pull_failed"
	Guard       string    `json:"guard"` // "pull"
	Time        time.Time `json:"time"`
	Repo        string    `json:"repo"`
	Error       string    `json:"error"`
	EverrefExit int       `json:"everref_exit"`
	Output      string    `json:"output,omitempty"`
}

func (g *Guard) notify(ctx context.Context, res *Result) error {
	cmdline := g.Defaults.NotifyCommand
	if len(cmdline) == 0 {
		return nil
	}
	w := Warning{Kind: "pull_failed", Guard: "pull", Time: res.Time, Repo: res.Repo, Error: res.Error, EverrefExit: res.EverrefExit, Output: res.Output}
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

// appendJournal appends res as one line to stateDir/pull.jsonl.
func (g *Guard) appendJournal(res *Result) error {
	if err := os.MkdirAll(g.Defaults.StateDir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(g.Defaults.StateDir, "pull.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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
