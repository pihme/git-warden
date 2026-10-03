package pushguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/testutil"
)

// fakeGitleaks writes a stand-in that answers `version` with v.
func fakeGitleaks(t *testing.T, v string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gitleaks")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+v+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func wallWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, files)
	return dir
}

func TestPreflight(t *testing.T) {
	ctx := context.Background()
	good := fakeGitleaks(t, "8.30.1")
	defaults := "agent: {name: t}\ngitleaks: {path: " + good + "}\n"
	repo := "remote: /srv/git/r.git\n"

	ready, err := Preflight(ctx, wallWith(t, map[string]string{"defaults.yaml": defaults, "repos/r/warden.yaml": repo}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Repos) != 1 || ready.Gitleaks[good] != "8.30.1" || ready.Git == "" {
		t.Fatalf("ready: %+v", ready)
	}

	// CONTENT-SECRET disabled: gitleaks is not needed
	ready, err = Preflight(ctx, wallWith(t, map[string]string{
		"defaults.yaml":       "agent: {name: t}\ngitleaks: {path: /nonexistent/gitleaks}\nrules:\n  CONTENT-SECRET: {enabled: false}\n",
		"repos/r/warden.yaml": repo,
	}), nil)
	if err != nil || len(ready.Gitleaks) != 0 {
		t.Fatalf("secret scan off: %+v %v", ready, err)
	}

	allOff := allRulesOff(t)
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"no defaults.yaml": {map[string]string{"repos/r/warden.yaml": repo}, "no wall configuration"},
		"no repos":         {map[string]string{"defaults.yaml": defaults}, "no repos configured"},
		"broken repo":      {map[string]string{"defaults.yaml": defaults, "repos/r/warden.yaml": "rules: {NO-SUCH-RULE: {}}\n"}, "NO-SUCH-RULE"},
		"gitleaks missing": {map[string]string{"defaults.yaml": "agent: {name: t}\ngitleaks: {path: /nonexistent/gitleaks}\n", "repos/r/warden.yaml": repo}, "gitleaks not found"},
		"gitleaks 7":       {map[string]string{"defaults.yaml": "agent: {name: t}\ngitleaks: {path: " + fakeGitleaks(t, "7.6.1") + "}\n", "repos/r/warden.yaml": repo}, "needs gitleaks 8.x"},
		"gitleaks per repo": {map[string]string{"defaults.yaml": defaults, "repos/a/warden.yaml": repo,
			"repos/b/warden.yaml": repo + "gitleaks: {path: /nonexistent/gl-b}\n"}, "CONTENT-SECRET is enabled for b"},
		"no rule enabled": {map[string]string{"defaults.yaml": defaults, "repos/r/warden.yaml": repo + allOff}, "no rule is enabled"},
	} {
		_, err := Preflight(ctx, wallWith(t, tc.files), nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}

	// several problems come in one error
	_, err = Preflight(ctx, wallWith(t, map[string]string{
		"defaults.yaml":       "agent: {name: t}\ngitleaks: {path: /nonexistent/gitleaks}\n",
		"repos/a/warden.yaml": repo + allOff,
		"repos/b/warden.yaml": repo,
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "no rule is enabled") || !strings.Contains(err.Error(), "gitleaks not found") {
		t.Fatalf("combined: %v", err)
	}

	// only the named repo is checked
	dir := wallWith(t, map[string]string{"defaults.yaml": defaults, "repos/a/warden.yaml": repo, "repos/b/warden.yaml": repo + allOff})
	if _, err := Preflight(ctx, dir, []string{"a"}); err != nil {
		t.Fatalf("repo a only: %v", err)
	}
}

func TestHookCheck(t *testing.T) {
	dir := wallWith(t, map[string]string{
		"defaults.yaml":         "agent: {name: t}\n",
		"repos/ok/warden.yaml":  "remote: /srv/git/r.git\nrules:\n  CONTENT-SECRET: {enabled: false}\n",
		"repos/gl/warden.yaml":  "remote: /srv/git/r.git\ngitleaks: {path: /nonexistent/gitleaks}\n",
		"repos/off/warden.yaml": "remote: /srv/git/r.git\n" + allRulesOff(t),
	})
	for name, want := range map[string]string{"ok": "", "gl": "gitleaks not found", "off": "no rule is enabled"} {
		cfg, err := config.LoadRepo(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		err = HookCheck(cfg)
		if (want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
}

// allRulesOff returns a rules: block that disables every built-in rule.
func allRulesOff(t *testing.T) string {
	t.Helper()
	wall, err := config.Load(wallWith(t, map[string]string{"defaults.yaml": "agent: {name: t}\n"}))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for id := range wall.Rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("rules:\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "  %s: {enabled: false}\n", id)
	}
	return b.String()
}
