package config

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
)

const wall = "agent:\n  name: test-agent\n"

func loadRepo(t *testing.T, files map[string]string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, files)
	return LoadRepo(dir, "demo")
}

func mustLoad(t *testing.T, files map[string]string) *Config {
	t.Helper()
	c, err := loadRepo(t, files)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func patterns(r *Rule) []string {
	var out []string
	for _, re := range r.Match {
		out = append(out, re.String())
	}
	return out
}

func TestBuiltinDefaults(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /srv/git/demo.git\n"})
	if c.AgentName != "test-agent" || c.PagesBranch != "gh-pages" || c.Timeout != 60*time.Second || !c.ForwardAtomic {
		t.Fatalf("unexpected config: %+v", c)
	}
	if r := c.Rule("REF-NON-FF"); !r.Enabled || r.Color != Red {
		t.Fatalf("REF-NON-FF: %+v", r)
	}
	if n, _ := c.Rule("REF-COUNT").Int("max_refs"); n != 10 {
		t.Fatalf("max_refs = %d", n)
	}
	if d, _ := c.Rule("META-BACKDATED").Duration("max_age"); d != 24*time.Hour {
		t.Fatalf("max_age = %v", d)
	}
	if !c.Rule("PATH-RED").Matches(".github/workflows/ci.yml") || !c.Rule("PATH-RED").Matches("docs/agents.md") {
		t.Fatal("PATH-RED defaults do not match")
	}
}

func TestMergeLayers(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml": wall + `
timeout: 30s
rules:
  REF-DELETE:
    allow: ['refs/heads/agent.*']
  REF-COUNT: {max_refs: 5}
  PATH-YELLOW:
    match: ['extra\.cfg']
`,
		"repos/demo/warden.yaml": `
remote: /srv/git/demo.git
default_branch: refs/heads/trunk
timeout: 5s
rules:
  REF-DELETE:
    allow: ['refs/heads/tmp/.*']
  REF-COUNT: {max_refs: 3}
  MODE-SYMLINK: {color: red}
  META-UNSIGNED: {enabled: false}
  PATH-YELLOW:
    match_remove: ['extra\.cfg', '(.*/)?Makefile']
`,
	})
	if c.Timeout != 5*time.Second || c.DefaultBranch != "trunk" {
		t.Fatalf("scalars not overridden: %v %q", c.Timeout, c.DefaultBranch)
	}
	del := c.Rule("REF-DELETE")
	if len(del.Allow) != 2 || !del.Allowed("refs/heads/agent-1") || !del.Allowed("refs/heads/tmp/x") {
		t.Fatalf("allow lists not appended: %v", del.Allow)
	}
	if n, _ := c.Rule("REF-COUNT").Int("max_refs"); n != 3 {
		t.Fatalf("limit not overridden: %d", n)
	}
	if c.Rule("MODE-SYMLINK").Color != Red || c.Rule("META-UNSIGNED").Enabled {
		t.Fatal("color or enabled not overridden")
	}
	py := c.Rule("PATH-YELLOW")
	if py.Matches("extra.cfg") || py.Matches("Makefile") || !py.Matches("package.json") {
		t.Fatalf("match_remove failed: %v", patterns(py))
	}
}

func TestAnchoring(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall,
		"repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-DELETE:\n    allow: ['refs/heads/agent.*']\n",
	})
	r := c.Rule("REF-DELETE")
	if r.Allowed("refs/heads/main-agent-x") || r.Allowed("xrefs/heads/agent") {
		t.Fatal("pattern matched partially")
	}
	if !r.Allowed("refs/heads/agent-x") {
		t.Fatal("pattern did not match the whole subject")
	}
	if c.Rule("PATH-YELLOW").Matches("node_modules/x/package.json.bak") {
		t.Fatal("PATH-YELLOW matched a suffix")
	}
}

func TestDenyWins(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml": wall,
		"repos/demo/warden.yaml": `remote: /x.git
rules:
  REF-DELETE:
    allow: ['refs/heads/.*']
    deny: ['refs/heads/main']
  REF-NAMESPACE:
    deny: ['refs/heads/main']
`,
	})
	del := c.Rule("REF-DELETE")
	if !del.Fires("refs/heads/main", true) || del.Fires("refs/heads/x", true) {
		t.Fatal("deny does not win over allow")
	}
	if del.Allowed("refs/heads/main") {
		t.Fatal("denied subject reported as allowed")
	}
	if !c.Rule("REF-NAMESPACE").Fires("refs/heads/main", false) {
		t.Fatal("deny does not fire without trigger")
	}
}

func TestDisabledNeverFires(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall,
		"repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-NAMESPACE: {enabled: false, deny: ['.*']}\n",
	})
	if c.Rule("REF-NAMESPACE").Fires("refs/notes/x", true) {
		t.Fatal("disabled rule fired")
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"unknown repo":       {map[string]string{"defaults.yaml": wall}, "unknown repo"},
		"missing agent":      {map[string]string{"repos/demo/warden.yaml": "remote: /x.git\n"}, "agent.name"},
		"missing remote":     {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "rules: {}\n"}, "remote is required"},
		"ssh needs cred":     {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: git@example.invalid:o/r.git\n"}, "credential is required"},
		"https needs cred":   {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: https://example.invalid/o/r.git\n"}, "credential is required"},
		"remote in defaults": {map[string]string{"defaults.yaml": wall + "remote: /x.git\n", "repos/demo/warden.yaml": "remote: /x.git\n"}, "belong in a repo"},
		"agent in repo":      {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nagent: {name: x}\n"}, "belong in the wall"},
		"unknown rule":       {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-TYPO: {}\n"}, "unknown rule"},
		"unknown key":        {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-DELETE: {alow: [x]}\n"}, "unknown key alow"},
		"unknown top key":    {map[string]string{"defaults.yaml": wall + "colour: red\n", "repos/demo/warden.yaml": "remote: /x.git\n"}, "colour"},
		"bad color":          {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-DELETE: {color: green}\n"}, "color must be"},
		"bad regex":          {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-DELETE: {allow: ['(']}\n"}, "REF-DELETE"},
		"remove missing":     {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  PATH-RED: {match_remove: ['nope']}\n"}, "not found"},
		"bad duration":       {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  META-FUTURE: {max_skew: soon}\n"}, "max_skew"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, tc.files)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestLocalRemoteWithoutCredential(t *testing.T) {
	for _, remote := range []string{"/srv/git/x.git", "file:///srv/git/x.git", "../x.git"} {
		if _, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: " + remote + "\n"}); err != nil {
			t.Errorf("%s: %v", remote, err)
		}
	}
}

func TestRemoteKind(t *testing.T) {
	cases := map[string]Kind{
		"git@github.com:o/r.git":    KindSSH,
		"ssh://git@host:22/o/r.git": KindSSH,
		"host:o/r.git":              KindSSH,
		"https://host/o/r.git":      KindHTTPS,
		"/srv/git/r.git":            KindLocal,
		"file:///srv/git/r.git":     KindLocal,
		"relative/dir":              KindLocal,
	}
	for in, want := range cases {
		got, err := RemoteKind(in)
		if err != nil || got != want {
			t.Errorf("%s: got %v %v, want %v", in, got, err, want)
		}
	}
	if _, err := RemoteKind("git://host/r.git"); err == nil {
		t.Error("git:// accepted")
	}
}

func TestRepoNames(t *testing.T) {
	for _, ok := range []string{"hermetarium", "a.b-c_d", "x1"} {
		if !ValidRepoName(ok) {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", ".git", "Upper", "a/b", "a b"} {
		if ValidRepoName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := LoadRepo(t.TempDir(), "../etc"); !errors.Is(err, ErrUnknownRepo) {
		t.Errorf("path traversal: %v", err)
	}
}
