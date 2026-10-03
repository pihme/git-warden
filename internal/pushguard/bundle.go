package pushguard

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/rules"
)

// writeBundle stores the new commits of a rejected push as
// <stateDir>/pending/<repo>/<id>.bundle, so a human can inspect them after the
// quarantine is gone. The bundle's refs are the pushed ref names. It returns
// "" when the push has no new objects.
//
// git bundle needs ref names, which can't be created inside the quarantine, so
// the bundle is built in a temporary repository that borrows the quarantined
// objects as alternates.
func writeBundle(ctx context.Context, g *gitx.Git, gitDir, stateDir, repo, id string, d *rules.Delta) (string, error) {
	var refs []*rules.RefDelta
	for _, rd := range d.Refs {
		if rd.Kind != rules.Delete && rd.Kind != rules.Noop {
			refs = append(refs, rd)
		}
	}
	if len(refs) == 0 || len(d.Commits) == 0 {
		return "", nil
	}
	tmp, err := os.MkdirTemp("", "push-guard-bundle-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	clean := &gitx.Git{Unset: []string{"GIT_DIR", "GIT_QUARANTINE_PATH", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_INDEX_FILE", "GIT_WORK_TREE"}}
	if _, err := clean.Run(ctx, "init", "--quiet", "--bare", tmp); err != nil {
		return "", err
	}
	var alts []string
	if q := os.Getenv("GIT_OBJECT_DIRECTORY"); q != "" {
		alts = append(alts, absUnder(gitDir, q))
	}
	for _, a := range filepath.SplitList(os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES")) {
		if a != "" {
			alts = append(alts, absUnder(gitDir, a))
		}
	}
	alts = append(alts, filepath.Join(gitDir, "objects"))
	if err := os.WriteFile(filepath.Join(tmp, "objects", "info", "alternates"), []byte(strings.Join(alts, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	tg := clean.With("GIT_DIR=" + tmp)
	args := []string{"bundle", "create", "--quiet"}
	dir := filepath.Join(stateDir, "pending", repo)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	out := filepath.Join(dir, id+".bundle")
	args = append(args, out)
	for _, rd := range refs {
		if _, err := tg.Run(ctx, "update-ref", rd.Ref, rd.New); err != nil {
			return "", err
		}
		args = append(args, rd.Ref)
	}
	if len(d.RemoteOIDs) > 0 {
		args = append(args, "--not")
		args = append(args, d.RemoteOIDs...)
	}
	if _, err := tg.Run(ctx, args...); err != nil {
		return "", err
	}
	return out, nil
}

func absUnder(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
