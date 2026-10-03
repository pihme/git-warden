package pushguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/journal"
)

var hexRE = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

func whoami() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

// Approve records a human approval of ref at sha in repo. The agent's next
// push of exactly that SHA to that ref is forwarded without re-checking, and
// an active yellow streak of the repo ends.
func Approve(configDir, repo, ref, sha string, out io.Writer) error {
	cfg, err := config.LoadRepo(configDir, repo)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(ref, "refs/") {
		ref = "refs/heads/" + ref
	}
	sha = strings.ToLower(sha)
	if !hexRE.MatchString(sha) {
		return fmt.Errorf("not a SHA: %s", sha)
	}
	jr := journal.Open(cfg.StateDir)
	unlock, err := LockRepo(cfg.StateDir, repo)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := jr.Read()
	if err != nil {
		return err
	}
	e := journal.Entry{Time: time.Now().UTC(), Event: journal.EventApprove, Repo: repo, Ref: ref, By: whoami()}
	if push, full := journal.FindRejected(entries, repo, ref, sha); push != nil {
		e.SHA, e.Push = full, push.ID
		seen := map[string]bool{}
		for _, f := range push.Findings {
			if !seen[f.Rule] {
				seen[f.Rule] = true
				e.Rules = append(e.Rules, f.Rule)
			}
		}
		sort.Strings(e.Rules)
	} else if len(sha) == 40 || len(sha) == 64 {
		e.SHA = sha
		fmt.Fprintf(out, "note: no rejected push of %s to %s found in the journal\n", sha, ref)
	} else {
		return fmt.Errorf("no rejected push of %s to %s found; give the full SHA", sha, ref)
	}
	streak := journal.StreakActive(entries, repo)
	if err := jr.Append(e); err != nil {
		return err
	}
	fmt.Fprintf(out, "approved %s %s in %s", ref, e.SHA, repo)
	if e.Push != "" {
		fmt.Fprintf(out, " (push %s, rules %s)", e.Push, strings.Join(e.Rules, ", "))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "The agent's next push of exactly this SHA to this ref is forwarded without re-checking.")
	if streak {
		fmt.Fprintln(out, "The yellow streak of this repo has ended.")
	}
	return nil
}

// ResetStreak ends a RATE-YELLOW-STREAK of repo without approving anything.
func ResetStreak(configDir, repo string, out io.Writer) error {
	cfg, err := config.LoadRepo(configDir, repo)
	if err != nil {
		return err
	}
	jr := journal.Open(cfg.StateDir)
	unlock, err := LockRepo(cfg.StateDir, repo)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := jr.Read()
	if err != nil {
		return err
	}
	active := journal.StreakActive(entries, repo)
	if err := jr.Append(journal.Entry{Time: time.Now().UTC(), Event: journal.EventReset, Repo: repo, By: whoami()}); err != nil {
		return err
	}
	if active {
		fmt.Fprintf(out, "yellow streak of %s reset\n", repo)
	} else {
		fmt.Fprintf(out, "no active yellow streak in %s; the yellow count starts from zero\n", repo)
	}
	return nil
}

// CheckConfig loads the wall and every repo and checks referenced files and
// tools. It prints one line per problem and returns an error if any.
func CheckConfig(ctx context.Context, configDir string, out io.Writer) error {
	var problems []string
	wall, err := config.Load(configDir)
	if err != nil {
		return err
	}
	exists := func(what, p string) {
		if p == "" {
			return
		}
		if _, err := os.Stat(p); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", what, err))
		}
	}
	if wall.AgentTokenFile == "" {
		fmt.Fprintln(out, "note: agent.token_file is not set; serve needs it")
	}
	exists("agent.token_file", wall.AgentTokenFile)
	if len(wall.NotifyCommand) == 0 {
		fmt.Fprintln(out, "note: notify.command is not set; nobody is warned about red pushes")
	} else if _, err := exec.LookPath(wall.NotifyCommand[0]); err != nil {
		problems = append(problems, fmt.Sprintf("notify.command: %v", err))
	}
	if _, err := exec.LookPath(wall.Gitleaks); err != nil {
		problems = append(problems, fmt.Sprintf("scanner.gitleaks: %v (every push would fail closed)", err))
	}
	repos, err := config.Repos(configDir)
	if err != nil {
		return err
	}
	for _, name := range repos {
		cfg, err := config.LoadRepo(configDir, name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		exists("repos/"+name+": credential", cfg.Credential)
		if cfg.Credential != "" {
			if st, err := os.Stat(cfg.Credential); err == nil && st.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(out, "note: repos/%s: credential %s is readable by group or others\n", name, cfg.Credential)
			}
		}
		if _, err := exec.LookPath(cfg.Gitleaks); err != nil && cfg.Gitleaks != wall.Gitleaks {
			problems = append(problems, fmt.Sprintf("repos/%s: scanner.gitleaks: %v", name, err))
		}
		fmt.Fprintf(out, "repo %s: remote %s\n", name, cfg.Remote)
	}
	if len(repos) == 0 {
		fmt.Fprintln(out, "note: no repos configured (repos/<name>/warden.yaml)")
	}
	for _, p := range problems {
		fmt.Fprintln(out, "problem: "+p)
	}
	if len(problems) > 0 {
		return errors.New("configuration has problems")
	}
	fmt.Fprintln(out, "configuration ok")
	return nil
}

// CheckRemote runs ls-remote against a repo's remote with its credential.
func CheckRemote(ctx context.Context, cfg *config.Config) error {
	remote, err := gitx.NewRemote(&gitx.Git{}, cfg.Remote, cfg.Credential)
	if err != nil {
		return err
	}
	_, err = remote.ListRefs(ctx)
	return err
}
