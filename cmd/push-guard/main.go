// Command push-guard is the Push Guard of Git Warden: agents push to it, it
// checks every push with deterministic rules and forwards only green pushes
// to the real remote.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/pushguard"
)

var version = "dev"

const usage = `push-guard checks agents' pushes and forwards only green ones.

Usage:
  push-guard serve        --config DIR --listen ADDR
  push-guard pre-receive  --config DIR --repo NAME      (run by the Git hook)
  push-guard init-repo    --config DIR <repo>
  push-guard approve      --config DIR <repo> <ref> <sha>
  push-guard reset-streak --config DIR <repo>
  push-guard check-config --config DIR [--remote]
  push-guard replay       --config DIR <repo> --git-dir PATH [--branch main] [--skip-scanner] [-v]
  push-guard version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet("push-guard "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configDir := fs.String("config", "", "configuration directory on the wall")
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "push-guard", version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "pre-receive":
		repo := fs.String("repo", "", "repo name")
		if _, err = parse(fs, args, 0, configDir); err != nil {
			break
		}
		wd, _ := os.Getwd()
		h := &pushguard.Hook{ConfigDir: *configDir, Repo: *repo, GitDir: wd, In: stdin, Out: stderr}
		return h.Run(ctx)
	case "serve":
		listen := fs.String("listen", "127.0.0.1:8418", "address to listen on")
		if _, err = parse(fs, args, 0, configDir); err != nil {
			break
		}
		err = serve(ctx, *configDir, *listen, stderr)
	case "init-repo":
		var pos []string
		if pos, err = parse(fs, args, 1, configDir); err != nil {
			break
		}
		err = initRepo(ctx, *configDir, pos[0], stdout)
	case "approve":
		var pos []string
		if pos, err = parse(fs, args, 3, configDir); err != nil {
			break
		}
		err = pushguard.Approve(*configDir, pos[0], pos[1], pos[2], stdout)
	case "reset-streak":
		var pos []string
		if pos, err = parse(fs, args, 1, configDir); err != nil {
			break
		}
		err = pushguard.ResetStreak(*configDir, pos[0], stdout)
	case "check-config":
		remote := fs.Bool("remote", false, "also run ls-remote against every repo's remote")
		if _, err = parse(fs, args, 0, configDir); err != nil {
			break
		}
		err = checkConfig(ctx, *configDir, *remote, stdout)
	case "replay":
		gitDir := fs.String("git-dir", "", "repository with the history to replay")
		branch := fs.String("branch", "main", "branch to replay")
		skip := fs.Bool("skip-scanner", false, "skip CONTENT-SECRET (gitleaks)")
		verbose := fs.Bool("v", false, "print every finding")
		var pos []string
		if pos, err = parse(fs, args, 1, configDir); err != nil {
			break
		}
		if *gitDir == "" {
			err = errors.New("--git-dir is required")
			break
		}
		rp := &pushguard.Replay{ConfigDir: *configDir, Repo: pos[0], GitDir: *gitDir, Branch: *branch,
			SkipScanner: *skip, Verbose: *verbose, Out: stdout}
		err = rp.Run(ctx)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "push-guard:", err)
		}
		return 1
	}
	return 0
}

// parse parses flags that may appear before, between or after exactly n
// positional arguments, and requires --config.
func parse(fs *flag.FlagSet, args []string, n int, configDir *string) ([]string, error) {
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
	if len(pos) != n {
		return nil, fmt.Errorf("%s: expected %d argument(s), got %d", fs.Name(), n, len(pos))
	}
	if *configDir == "" {
		return nil, errors.New("--config is required")
	}
	return pos, nil
}

func self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func initRepo(ctx context.Context, configDir, repo string, out io.Writer) error {
	cfg, err := config.LoadRepo(configDir, repo)
	if err != nil {
		return err
	}
	if _, err := pushguard.Preflight(ctx, configDir, []string{repo}); err != nil {
		return err
	}
	exe, err := self()
	if err != nil {
		return err
	}
	path, err := pushguard.EnsureRepo(ctx, cfg, exe)
	if err != nil {
		return err
	}
	unlock, err := pushguard.LockRepo(cfg.StateDir, repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := pushguard.Sync(ctx, cfg, path); err != nil {
		return err
	}
	fmt.Fprintf(out, "guard repository ready: %s\n", path)
	return nil
}

func checkConfig(ctx context.Context, configDir string, remote bool, out io.Writer) error {
	if err := pushguard.CheckConfig(ctx, configDir, out); err != nil {
		return err
	}
	if !remote {
		return nil
	}
	repos, err := config.Repos(configDir)
	if err != nil {
		return err
	}
	failed := false
	for _, name := range repos {
		cfg, err := config.LoadRepo(configDir, name)
		if err == nil {
			cctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
			err = pushguard.CheckRemote(cctx, cfg)
			cancel()
		}
		if err != nil {
			failed = true
			fmt.Fprintf(out, "problem: repo %s: remote: %v\n", name, err)
		} else {
			fmt.Fprintf(out, "repo %s: remote reachable\n", name)
		}
	}
	if failed {
		return errors.New("some remotes are not reachable")
	}
	return nil
}

func serve(ctx context.Context, configDir, listen string, logw io.Writer) error {
	exe, err := self()
	if err != nil {
		return err
	}
	s, err := pushguard.NewServer(configDir, exe)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: listen, Handler: s, ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	fmt.Fprintf(logw, "push-guard %s: preflight ok (%s); serving on %s\n", version, s.Summary, listen)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
