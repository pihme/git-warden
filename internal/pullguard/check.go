package pullguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CheckConfig checks the configuration directory without backing anything
// up: the preflight of a run, every repo's file, credential and
// known_hosts, notify.command, and with remote also ls-remote per repo.
func CheckConfig(ctx context.Context, configDir string, remote bool, out io.Writer) error {
	g, repos, err := Preflight(ctx, configDir, out)
	if err != nil {
		return err
	}
	d := g.Defaults
	fmt.Fprintf(out, "everref: %s (%s)\n", g.Everref, g.Version)
	if d.EverrefVersion == "" {
		fmt.Fprintln(out, "note: everref.version is not set; any installed everref version is accepted")
	}
	var problems []string
	if len(d.NotifyCommand) == 0 {
		fmt.Fprintln(out, "note: notify.command is not set; nobody is warned about failed runs")
	} else if _, err := exec.LookPath(d.NotifyCommand[0]); err != nil {
		problems = append(problems, fmt.Sprintf("notify.command: %v", err))
	}
	for _, name := range repos {
		r, err := LoadRepo(d.Dir, name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		for what, p := range map[string]string{"credential": r.Credential, "known_hosts": r.KnownHosts} {
			if p == "" {
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				problems = append(problems, fmt.Sprintf("repos/%s: %s: %v", name, what, err))
			} else if what == "credential" && st.Mode().Perm()&0o077 != 0 {
				fmt.Fprintf(out, "note: repos/%s: credential %s is readable by group or others\n", name, p)
			}
		}
		if remote {
			if err := checkRemote(ctx, d, r); err != nil {
				problems = append(problems, fmt.Sprintf("repos/%s: remote: %v", name, err))
			} else {
				fmt.Fprintf(out, "repo %s: remote reachable\n", name)
			}
		}
		fmt.Fprintf(out, "repo %s: %s -> %s\n", name, r.Remote, BackupPath(d.StateDir, name))
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(out, "problem:", p)
		}
		return errors.New(strings.TrimSpace(fmt.Sprintf("%d problem(s) in the configuration", len(problems))))
	}
	fmt.Fprintln(out, "configuration ok")
	return nil
}

func checkRemote(ctx context.Context, d *Defaults, r *Repo) error {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	rm, err := newRemote(r)
	if err != nil {
		return err
	}
	_, err = rm.ListRefs(ctx)
	return err
}
