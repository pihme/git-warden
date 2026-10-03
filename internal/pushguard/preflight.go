package pushguard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
)

// Ready is the result of a successful Preflight.
type Ready struct {
	Wall     *config.Config
	Git      string            // git version, e.g. "2.47"
	Repos    []string          // checked repos
	Gitleaks map[string]string // gitleaks binary -> version, for every binary a repo with CONTENT-SECRET uses
}

// Preflight checks what the Push Guard needs before it serves agents or sets
// up a guard repository, and fails closed with one error naming every
// problem:
//
//   - the wall configuration: <dir>/defaults.yaml exists and loads;
//   - git on PATH, 2.42 or newer;
//   - the repos (all of them if repos is empty): at least one is configured
//     (repos/<name>/warden.yaml), each loads, and each has at least one
//     enabled rule;
//   - gitleaks for every repo with CONTENT-SECRET enabled (the default): the
//     binary from gitleaks.path is found (on PATH or as a path) and
//     `gitleaks version` reports 8.x.
//
// The hook runs the cheap part of the same checks on every push (HookCheck).
func Preflight(ctx context.Context, configDir string, repos []string) (*Ready, error) {
	if configDir == "" {
		return nil, errors.New("preflight failed: no configuration directory (--config)")
	}
	if _, err := os.Stat(filepath.Join(configDir, "defaults.yaml")); err != nil {
		return nil, fmt.Errorf("preflight failed: no wall configuration: %w (it needs at least agent.name)", err)
	}
	wall, err := config.Load(configDir)
	if err != nil {
		return nil, fmt.Errorf("preflight failed: %w", err)
	}
	var problems []string
	ready := &Ready{Wall: wall, Gitleaks: map[string]string{}}
	if _, v, err := gitx.CheckGit(ctx); err != nil {
		problems = append(problems, err.Error())
	} else {
		ready.Git = v
	}
	if len(repos) == 0 {
		if repos, err = configuredRepos(configDir); err != nil {
			return nil, fmt.Errorf("preflight failed: %w", err)
		}
		if len(repos) == 0 {
			problems = append(problems, fmt.Sprintf("no repos configured: add %s", filepath.Join(wall.Dir, "repos", "<name>", "warden.yaml")))
		}
	}
	needGitleaks := map[string][]string{} // binary -> repos
	for _, name := range repos {
		cfg, err := config.LoadRepo(configDir, name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if err := checkRules(cfg); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		ready.Repos = append(ready.Repos, name)
		if cfg.Rule("CONTENT-SECRET").Enabled {
			needGitleaks[gitleaksBin(cfg)] = append(needGitleaks[gitleaksBin(cfg)], name)
		}
	}
	bins := make([]string, 0, len(needGitleaks))
	for b := range needGitleaks {
		bins = append(bins, b)
	}
	sort.Strings(bins)
	for _, bin := range bins {
		v, err := CheckGitleaks(ctx, bin)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%v (CONTENT-SECRET is enabled for %s; every push there would fail closed)", err, strings.Join(needGitleaks[bin], ", ")))
			continue
		}
		ready.Gitleaks[bin] = v
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("preflight failed:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return ready, nil
}

// HookCheck is the per-push part of the preflight, run by the hook before it
// looks at the push: the repo has an enabled rule, and gitleaks is found if
// CONTENT-SECRET is enabled. Any failure rejects the push as an internal
// error.
func HookCheck(cfg *config.Config) error {
	if err := checkRules(cfg); err != nil {
		return err
	}
	if cfg.Rule("CONTENT-SECRET").Enabled {
		if _, err := exec.LookPath(gitleaksBin(cfg)); err != nil {
			return fmt.Errorf("preflight: gitleaks not found (%s): %w; install gitleaks 8.x or set gitleaks.path", gitleaksBin(cfg), err)
		}
	}
	return nil
}

// CheckGitleaks finds bin and checks that `gitleaks version` reports 8.x.
func CheckGitleaks(ctx context.Context, bin string) (string, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("gitleaks not found (%s): %w; install gitleaks 8.x or set gitleaks.path", bin, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s version: %w: %s", path, err, truncate(strings.TrimSpace(string(out)), 300))
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	if !strings.HasPrefix(v, "8.") {
		return "", fmt.Errorf("%s reports version %q; the Push Guard needs gitleaks 8.x", path, truncate(v, 100))
	}
	return v, nil
}

func checkRules(cfg *config.Config) error {
	for _, r := range cfg.Rules {
		if r.Enabled {
			return nil
		}
	}
	return fmt.Errorf("repo %s: no rule is enabled; refusing to forward pushes unchecked", cfg.RepoName)
}

func gitleaksBin(cfg *config.Config) string {
	if cfg.Gitleaks == "" {
		return "gitleaks"
	}
	return cfg.Gitleaks
}

// configuredRepos lists the repo folders that have a warden.yaml.
func configuredRepos(dir string) ([]string, error) {
	names, err := config.Repos(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, "repos", n, "warden.yaml")); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

func describeGitleaks(m map[string]string) string {
	if len(m) == 0 {
		return ", gitleaks not needed (CONTENT-SECRET disabled everywhere)"
	}
	var parts []string
	for bin, v := range m {
		parts = append(parts, v+" ("+bin+")")
	}
	sort.Strings(parts)
	return ", gitleaks " + strings.Join(parts, ", ")
}
