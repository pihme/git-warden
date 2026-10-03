package pushguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
)

// RepoPath is the guard's bare repository for repo.
func RepoPath(stateDir, repo string) string {
	return filepath.Join(stateDir, "repos", repo+".git")
}

// HookScript is the pre-receive hook installed in a guard repository.
func HookScript(binary, configDir, repo string) string {
	return "#!/bin/sh\n# Installed by push-guard. Do not edit; it is rewritten on every sync.\n" +
		"exec " + gitx.ShellQuote(binary) + " pre-receive --config " + gitx.ShellQuote(configDir) +
		" --repo " + gitx.ShellQuote(repo) + "\n"
}

// EnsureRepo creates the guard's bare repository for cfg's repo if needed and
// (re)installs the pre-receive hook pointing at binary.
func EnsureRepo(ctx context.Context, cfg *config.Config, binary string) (string, error) {
	path := RepoPath(cfg.StateDir, cfg.RepoName)
	g := &gitx.Git{Unset: []string{"GIT_DIR", "GIT_WORK_TREE"}}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		if _, err := g.Run(ctx, "init", "--quiet", "--bare", path); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	rg := g.With("GIT_DIR=" + path)
	settings := [][2]string{
		{"http.receivepack", "true"},
		{"core.hooksPath", filepath.Join(path, "hooks")},
		{"receive.fsckObjects", "true"},
		{"receive.denyDeletes", "false"},
		{"receive.denyNonFastForwards", "false"},
		{"gc.auto", "0"},
	}
	for _, kv := range settings {
		if _, err := rg.Run(ctx, "config", kv[0], kv[1]); err != nil {
			return "", err
		}
	}
	hook := filepath.Join(path, "hooks", "pre-receive")
	want := []byte(HookScript(binary, cfg.Dir, cfg.RepoName))
	if have, err := os.ReadFile(hook); err != nil || !bytes.Equal(have, want) {
		if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
			return "", err
		}
		tmp := hook + ".tmp"
		if err := os.WriteFile(tmp, want, 0o700); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, hook); err != nil {
			return "", err
		}
	}
	return path, nil
}

// Sync updates the guard repository's branches and tags from the remote, so
// agents fetch the remote's state through the guard and pushes start from it.
func Sync(ctx context.Context, cfg *config.Config, path string) error {
	g := &gitx.Git{Unset: []string{"GIT_DIR", "GIT_WORK_TREE"}}
	remote, err := gitx.NewRemote(g.With("GIT_DIR="+path), cfg.Remote, cfg.Credential, cfg.CredentialUsername, cfg.KnownHosts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	_, err = remote.Git().Run(ctx, "-c", "gc.auto=0", "fetch", "--quiet", "--prune", "--no-write-fetch-head",
		"--no-recurse-submodules", remote.URL, "+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	if err != nil {
		return fmt.Errorf("sync %s: %w", cfg.RepoName, err)
	}
	refs, err := remote.ListRefs(ctx)
	if err == nil && refs.Head != "" {
		_, err = remote.Git().Run(ctx, "symbolic-ref", "HEAD", refs.Head)
	}
	return err
}
