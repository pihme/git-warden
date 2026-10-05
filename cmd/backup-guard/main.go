// Command backup-guard is the Backup Guard of Git Warden: on every run it backs up
// every branch and tag of each configured remote into an append-only backup
// repository by running git-everref, which must be installed on the host.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/pihme/git-warden/internal/backupguard"
)

var version = "dev"

const usage = `backup-guard keeps an append-only backup of every branch and tag (via git-everref).

Usage:
  backup-guard run          --config DIR [repo...]   (from a timer; all repos if none given)
  backup-guard check-config --config DIR [--remote]
  backup-guard version

Exit codes: 0 every repo backed up, 1 a repo failed or the preflight failed
(e.g. git-everref missing), 2 usage error.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet("backup-guard "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configDir := fs.String("config", "", "Backup Guard configuration directory")
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "backup-guard", version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "run":
		var pos []string
		if pos, err = parse(fs, args, configDir); err != nil {
			return usageError(stderr, err)
		}
		err = runRepos(ctx, *configDir, pos, stdout)
	case "check-config":
		remote := fs.Bool("remote", false, "also run ls-remote against every repo's remote")
		var pos []string
		if pos, err = parse(fs, args, configDir); err != nil {
			return usageError(stderr, err)
		}
		if len(pos) > 0 {
			return usageError(stderr, errors.New("check-config takes no arguments"))
		}
		err = backupguard.CheckConfig(ctx, *configDir, *remote, stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "backup-guard:", err)
		return 1
	}
	return 0
}

func usageError(stderr io.Writer, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(stderr, "backup-guard:", err)
	return 2
}

// parse parses flags that may appear before, between or after positional
// arguments, and requires --config.
func parse(fs *flag.FlagSet, args []string, configDir *string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if *configDir == "" {
		return nil, errors.New("--config is required")
	}
	return pos, nil
}

func runRepos(ctx context.Context, configDir string, only []string, out io.Writer) error {
	g, repos, err := backupguard.PreflightRun(ctx, configDir, out)
	if err != nil {
		return fmt.Errorf("preflight: %w", err)
	}
	if len(only) > 0 {
		known := map[string]bool{}
		for _, r := range repos {
			known[r] = true
		}
		for _, r := range only {
			if !known[r] {
				return fmt.Errorf("unknown repo %q (no repos/%s/%s)", r, r, backupguard.RepoFile)
			}
		}
		repos = only
	}
	return g.RunAll(ctx, repos)
}
