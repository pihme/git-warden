package config

// Requirement and edge tests for the configuration (docs/push-guard-rules.md,
// "Configuration"; docs/push-guard.md). See docs/push-guard-testspec.md.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
)

const repoOK = "remote: /srv/git/x.git\n"

// Verdict colours order red > yellow > green; anything else ranks as green.
func TestVerdictColorOrder(t *testing.T) {
	for _, c := range []struct {
		a, b Color
		want bool
	}{
		{Red, Yellow, true}, {Yellow, Green, true}, {Red, Green, true},
		{Yellow, Red, false}, {Green, Yellow, false}, {Red, Red, false},
		{Color("purple"), Green, false}, {Yellow, Color(""), true},
	} {
		if got := c.a.Worse(c.b); got != c.want {
			t.Errorf("%q.Worse(%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Load without a repo reads only the wall; Repos lists valid repo folders.
func TestWallOnlyLoadAndRepoList(t *testing.T) {
	dir := t.TempDir()
	if repos, err := Repos(dir); err != nil || repos != nil {
		t.Fatalf("no repos folder: %v, %v", repos, err)
	}
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":           wall + "pages_branch: site\n",
		"repos/demo/warden.yaml":  repoOK,
		"repos/other/warden.yaml": repoOK,
		"repos/Bad Name/x":        "",
		"repos/file.yaml":         "",
	})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.RepoName != "" || c.Remote != "" || c.PagesBranch != "site" || c.AgentName != "test-agent" {
		t.Errorf("wall config = repo %q remote %q pages %q agent %q", c.RepoName, c.Remote, c.PagesBranch, c.AgentName)
	}
	repos, err := Repos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repos, []string{"demo", "other"}) {
		t.Errorf("Repos = %v, want [demo other]", repos)
	}
	rc, err := LoadRepo(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(rc.Dir, "repos", "demo"); rc.RepoDir() != want {
		t.Errorf("RepoDir = %q, want %q", rc.RepoDir(), want)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Repos(filepath.Join(dir, "plain")); err == nil {
		t.Error("Repos on a file: no error")
	}
}

// A rule ID the configuration does not know is disabled and never fires.
func TestUnknownRuleIDIsDisabled(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK})
	r := c.Rule("NO-SUCH-RULE")
	if r.Enabled || r.Fires("x", true) || r.ID != "NO-SUCH-RULE" {
		t.Errorf("unknown rule = %+v", r)
	}
}

// Integer and duration limits: accepted number kinds, missing keys, wrong kinds.
func TestLimitAccessors(t *testing.T) {
	r := &Rule{ID: "X", Limits: map[string]any{
		"int": 3, "int64": int64(4), "uint64": uint64(5), "float": float64(6), "frac": 1.5,
		"str": "10m", "badstr": "soon", "num": 600,
	}}
	for k, want := range map[string]int64{"int": 3, "int64": 4, "uint64": 5, "float": 6} {
		if got, err := r.Int(k); err != nil || got != want {
			t.Errorf("Int(%s) = %d, %v; want %d", k, got, err, want)
		}
	}
	for _, k := range []string{"frac", "str", "missing"} {
		if _, err := r.Int(k); err == nil {
			t.Errorf("Int(%s): no error", k)
		}
	}
	if d, err := r.Duration("str"); err != nil || d != 10*time.Minute {
		t.Errorf("Duration(str) = %v, %v", d, err)
	}
	for _, k := range []string{"badstr", "num", "missing"} {
		if _, err := r.Duration(k); err == nil {
			t.Errorf("Duration(%s): no error", k)
		}
	}
}

// Whole-number limits written as floats or in exponent form load and read as integers.
func TestREQ_PG_032_WholeNumberLimitForms(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  SIZE-LIMIT: {max_files: 2e3, max_lines: 100.0}\n"})
	r := c.Rule("SIZE-LIMIT")
	if n, err := r.Int("max_files"); err != nil || n != 2000 {
		t.Errorf("max_files = %d, %v; want 2000", n, err)
	}
	if n, err := r.Int("max_lines"); err != nil || n != 100 {
		t.Errorf("max_lines = %d, %v; want 100", n, err)
	}
}

// A limit of the wrong kind must be rejected when the configuration loads
// (so preflight and check-config report it), not on the first push.
func TestREQ_PG_032_LimitKindCheckedOnLoad(t *testing.T) {
	for name, rule := range map[string]string{
		"duration as number": "META-FUTURE: {max_skew: 600}",
		"count as duration":  "SIZE-LIMIT: {max_files: '10m'}",
		"count as fraction":  "SIZE-LIMIT: {max_files: 1.5}",
		"count as list":      "SIZE-LIMIT: {max_files: [1]}",
		"count as bool":      "SIZE-LIMIT: {max_files: true}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
				"repos/demo/warden.yaml": repoOK + "rules:\n  " + rule + "\n"})
			if err == nil {
				t.Fatalf("%s loads; every push of the repo would then fail with internal error", rule)
			}
		})
	}
}

// allow_remove and deny_remove take out a default entry; a missing one is an error.
func TestAllowDenyRemove(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall + "rules:\n  REF-DELETE: {allow: ['refs/heads/agent.*']}\n  REF-NAMESPACE: {deny: ['refs/heads/main']}\n",
		"repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE: {allow_remove: ['refs/heads/agent.*']}\n  REF-NAMESPACE: {deny_remove: ['refs/heads/main']}\n",
	})
	if c.Rule("REF-DELETE").Allowed("refs/heads/agent-x") {
		t.Error("allow_remove did not remove the allow entry")
	}
	if c.Rule("REF-NAMESPACE").Fires("refs/heads/main", false) {
		t.Error("deny_remove did not remove the deny entry")
	}
	for _, key := range []string{"allow_remove", "deny_remove"} {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
			"repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE: {" + key + ": ['nope']}\n"})
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s of a missing entry: err = %v", key, err)
		}
	}
}

// A rule listed with no settings keeps its defaults.
func TestEmptyRuleEntryKeepsDefaults(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE:\n"})
	r := c.Rule("REF-DELETE")
	if !r.Enabled || r.Color != Red {
		t.Errorf("REF-DELETE = enabled %v color %q, want on and red", r.Enabled, r.Color)
	}
}

// timeout defaults to 60s and must be a positive duration.
func TestTimeout(t *testing.T) {
	if c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK}); c.Timeout != 60*time.Second {
		t.Errorf("default timeout = %v", c.Timeout)
	}
	if c := mustLoad(t, map[string]string{"defaults.yaml": wall + "timeout: 5s\n", "repos/demo/warden.yaml": repoOK}); c.Timeout != 5*time.Second {
		t.Errorf("timeout 5s = %v", c.Timeout)
	}
	for _, v := range []string{"soon", "0s", "-1s"} {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall + "timeout: " + v + "\n", "repos/demo/warden.yaml": repoOK})
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("timeout %s: err = %v", v, err)
		}
	}
}

// Unreadable or broken configuration files are errors, not defaults.
func TestUnreadableConfigFiles(t *testing.T) {
	for name, setup := range map[string]func(dir string){
		"defaults.yaml is a directory": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "defaults.yaml"), 0o755)
			testutil.WriteFiles(t, dir, map[string]string{"repos/demo/warden.yaml": repoOK})
		},
		"warden.yaml is a directory": func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": wall})
			os.MkdirAll(filepath.Join(dir, "repos", "demo", "warden.yaml"), 0o755)
		},
		"warden.yaml is not YAML": func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: [\n"})
		},
		"defaults.yaml is not YAML": func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": "agent: {\n", "repos/demo/warden.yaml": repoOK})
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setup(dir)
			if _, err := LoadRepo(dir, "demo"); err == nil {
				t.Fatal("loads")
			}
		})
	}
}

// Remote settings that do not fit the remote kind are errors.
func TestRemoteSettingMismatch(t *testing.T) {
	for name, c := range map[string]struct{ repo, want string }{
		"known_hosts for https": {"remote: https://example.invalid/o/r.git\ncredential: t\nknown_hosts: kh\n", "known_hosts only applies"},
		"known_hosts for local": {"remote: /srv/git/x.git\nknown_hosts: kh\n", "known_hosts only applies"},
		"unsupported scheme":    {"remote: ftp://example.invalid/x.git\n", "ftp"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": c.repo})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	if _, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": "remote: git@example.invalid:o/r.git\ncredential: k\nknown_hosts: kh\n"}); err != nil {
		t.Errorf("known_hosts for ssh: %v", err)
	}
}
