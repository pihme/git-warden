package config

import (
	"errors"
	"os"
	"path/filepath"
	"regexp/syntax"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
	"gopkg.in/yaml.v3"
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
	// Each limit is stored on the rule, not only in the raw map.
	blob := c.Rule("CONTENT-BLOB")
	if blob.MinBase64 != 200 || blob.MinHex != 100 {
		t.Errorf("CONTENT-BLOB = base64 %d hex %d", blob.MinBase64, blob.MinHex)
	}
	if c.Rule("META-UNSIGNED").Lookback != 20 {
		t.Errorf("lookback = %d", c.Rule("META-UNSIGNED").Lookback)
	}
	if c.Rule("META-BACKDATED").MaxAge != 24*time.Hour {
		t.Errorf("max_age = %s", c.Rule("META-BACKDATED").MaxAge)
	}
	if c.Rule("META-FUTURE").MaxSkew != 10*time.Minute {
		t.Errorf("max_skew = %s", c.Rule("META-FUTURE").MaxSkew)
	}
	lim := c.Rule("SIZE-LIMIT")
	if lim.MaxFiles != 2000 || lim.MaxLines != 50000 || lim.MaxBytes != 50000000 || lim.MaxFileBytes != 10000000 {
		t.Errorf("SIZE-LIMIT = %+v", lim)
	}
	large := c.Rule("SIZE-LARGE")
	if large.MaxFiles != 200 || large.MaxLines != 5000 || large.MaxCommits != 100 || large.MaxFileBytes != 1000000 {
		t.Errorf("SIZE-LARGE = %+v", large)
	}
	del := c.Rule("SIZE-MASS-DELETE")
	if del.MaxDeletedFiles != 20 || del.MaxDeletedShare != 50 || del.MinLines != 100 {
		t.Errorf("SIZE-MASS-DELETE = %+v", del)
	}
	if c.Rule("RATE-LIMIT").MaxPushes != 30 || c.Rule("RATE-LIMIT").Window != time.Hour {
		t.Errorf("RATE-LIMIT = %+v", c.Rule("RATE-LIMIT"))
	}
	if c.Rule("RATE-YELLOW-STREAK").MaxYellow != 5 || c.Rule("RATE-YELLOW-STREAK").Window != 24*time.Hour {
		t.Errorf("RATE-YELLOW-STREAK = %+v", c.Rule("RATE-YELLOW-STREAK"))
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

	t.Run("every wall key applies", func(t *testing.T) {
		defaults := "agent:\n  name: test-agent\n  token_file: tokens/agent\n" +
			"notify:\n  command: [/bin/true, notify]\n" +
			"state_dir: var/state\n" +
			"gitleaks:\n  path: bin/gitleaks\n" +
			"forward:\n  atomic: false\n"
		c := mustLoad(t, map[string]string{
			"defaults.yaml":          defaults,
			"repos/demo/warden.yaml": "remote: /srv/git/demo.git\ndefault_branch: main\n",
		})
		if !strings.HasSuffix(c.AgentTokenFile, filepath.Join("tokens", "agent")) {
			t.Errorf("token_file: %q", c.AgentTokenFile)
		}
		if len(c.NotifyCommand) != 2 || c.NotifyCommand[0] != "/bin/true" {
			t.Errorf("notify: %v", c.NotifyCommand)
		}
		if !strings.HasSuffix(c.StateDir, filepath.Join("var", "state")) {
			t.Errorf("state_dir: %q", c.StateDir)
		}
		if !strings.HasSuffix(c.Gitleaks, filepath.Join("bin", "gitleaks")) {
			t.Errorf("gitleaks: %q", c.Gitleaks)
		}
		if c.ForwardAtomic {
			t.Error("atomic false not applied")
		}
		if c.DefaultBranch != "main" {
			t.Errorf("default_branch: %q", c.DefaultBranch)
		}
	})

	t.Run("empty block keeps lower layer", func(t *testing.T) {
		c := mustLoad(t, map[string]string{
			"defaults.yaml":          wall + "notify:\n  command: [/bin/true]\nforward: {atomic: false}\n",
			"repos/demo/warden.yaml": repoOK + "notify: {}\nforward: {}\n",
		})
		if len(c.NotifyCommand) != 1 || c.NotifyCommand[0] != "/bin/true" {
			t.Errorf("notify: {} cleared the wall's command: %v", c.NotifyCommand)
		}
		if c.ForwardAtomic {
			t.Error("forward: {} reset atomic")
		}
	})

	t.Run("defaults", func(t *testing.T) {
		c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK})
		if c.StateDir != filepath.Join(c.Dir, "state") {
			t.Errorf("StateDir=%q", c.StateDir)
		}
		if c.Timeout != 60*time.Second {
			t.Errorf("Timeout=%v", c.Timeout)
		}
	})

	// A rule listed without settings keeps what the lower layer set and does
	// not stop the rules after it (IDs are applied in sorted order).
	t.Run("rule without settings", func(t *testing.T) {
		c := mustLoad(t, map[string]string{
			"defaults.yaml":          wall + "rules:\n  REF-DELETE: {allow: ['refs/heads/agent.*']}\n",
			"repos/demo/warden.yaml": repoOK + "rules:\n  PATH-RED:\n  REF-DELETE:\n  REF-COUNT: {max_refs: 2}\n",
		})
		if !c.Rule("REF-DELETE").Allowed("refs/heads/agent-1") {
			t.Error("rule entry without settings cleared the lower layer's allow")
		}
		if n, _ := c.Rule("REF-COUNT").Int("max_refs"); n != 2 {
			t.Errorf("later rule skipped: max_refs = %d, want 2", n)
		}
	})

	t.Run("match_remove removes only the named pattern", func(t *testing.T) {
		c := mustLoad(t, map[string]string{
			"defaults.yaml":          wall + "rules:\n  PATH-YELLOW:\n    match: ['a\\.txt', 'b\\.txt', 'c\\.txt']\n",
			"repos/demo/warden.yaml": repoOK + "rules:\n  PATH-YELLOW:\n    match_remove: ['b\\.txt']\n",
		})
		r := c.Rule("PATH-YELLOW")
		if !r.Matches("a.txt") || r.Matches("b.txt") || !r.Matches("c.txt") {
			t.Errorf("match_remove wrong: a=%v b=%v c=%v", r.Matches("a.txt"), r.Matches("b.txt"), r.Matches("c.txt"))
		}
	})
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
		"ssh needs cred":     {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: git@example.invalid:o/r.git\n"}, "needs a credential (an SSH private key file)"},
		"https needs cred":   {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: https://example.invalid/o/r.git\n"}, "needs a credential (a token file)"},
		"relative local":     {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: ../x.git\n"}, "absolute path or a file:// URL"},
		"local with cred":    {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /srv/git/x.git\ncredential: k\n"}, "takes no credential"},
		"user for ssh":       {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: git@example.invalid:o/r.git\ncredential: k\ncredential_username: u\n"}, "credential_username only applies"},
		"bad user":           {map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: https://example.invalid/o/r.git\ncredential: t\ncredential_username: \"a b\"\n"}, "only letters"},
		"user in defaults":   {map[string]string{"defaults.yaml": wall + "credential_username: u\n", "repos/demo/warden.yaml": "remote: /srv/git/x.git\n"}, "belong in a repo"},
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

	t.Run("key in the wrong layer", func(t *testing.T) {
		for _, key := range []string{"remote: /x.git", "credential: k", "credential_username: u", "known_hosts: kh", "default_branch: main"} {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall + key + "\n", "repos/demo/warden.yaml": repoOK})
			if err == nil || !strings.Contains(err.Error(), "belong in a repo") {
				t.Errorf("%s: %v", key, err)
			}
		}
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK + "state_dir: x\n"})
		if err == nil || !strings.Contains(err.Error(), "belong in the wall") {
			t.Errorf("state_dir in repo: %v", err)
		}
	})

	t.Run("*_remove of an unknown pattern", func(t *testing.T) {
		for _, key := range []string{"match_remove", "allow_remove", "deny_remove"} {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
				"repos/demo/warden.yaml": repoOK + "rules:\n  PATH-RED: {" + key + ": ['nope']}\n"})
			if err == nil || errors.Unwrap(err) == nil || !strings.Contains(errors.Unwrap(err).Error(), "not found") {
				t.Errorf("%s: err = %v, want a wrapped not-found error", key, err)
			}
		}
	})

	t.Run("YAML error is kept", func(t *testing.T) {
		if _, err := loadRepo(t, map[string]string{"defaults.yaml": "agent: {\n", "repos/demo/warden.yaml": repoOK}); err == nil {
			t.Error("broken YAML loads")
		}
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall + "colour: red\n", "repos/demo/warden.yaml": repoOK})
		var te *yaml.TypeError
		if !errors.As(err, &te) {
			t.Errorf("err = %v (%T), want a wrapped *yaml.TypeError", err, err)
		}
	})

	t.Run("unsupported remote", func(t *testing.T) {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: ftp://h/x.git\n"})
		if err == nil || !strings.Contains(err.Error(), "ftp") || errors.Unwrap(err) == nil || !strings.Contains(errors.Unwrap(err).Error(), "unsupported") {
			t.Errorf("err = %v, want the CheckRemote error wrapped", err)
		}
	})

	t.Run("bad pattern names the rule and keeps the syntax error", func(t *testing.T) {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE: {allow: ['(']}\n"})
		var se *syntax.Error
		if err == nil || !strings.Contains(err.Error(), "REF-DELETE") || !errors.As(err, &se) {
			t.Errorf("err = %v, want the rule named and a wrapped *syntax.Error", err)
		}
	})

	// Read errors other than "does not exist" are returned as they are, not
	// replaced by a later validation error.
	t.Run("read error is returned as is", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "defaults.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); !errors.Is(err, syscall.EISDIR) {
			t.Errorf("defaults.yaml is a directory: %v, want EISDIR", err)
		}
		dir = t.TempDir()
		testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": wall})
		if err := os.MkdirAll(filepath.Join(dir, "repos", "demo", "warden.yaml"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRepo(dir, "demo"); !errors.Is(err, syscall.EISDIR) {
			t.Errorf("warden.yaml is a directory: %v, want EISDIR", err)
		}
	})

	// If the working directory is gone, a relative config dir cannot be
	// resolved: load returns that error instead of reading relative paths.
	t.Run("relative dir without working directory", func(t *testing.T) {
		gone := t.TempDir()
		t.Chdir(gone)
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		_, err := Load("cfg")
		if err == nil || strings.Contains(err.Error(), "agent.name") {
			t.Errorf("err = %v, want the getwd error", err)
		}
	})
}

func TestLocalRemoteWithoutCredential(t *testing.T) {
	for _, remote := range []string{"/srv/git/x.git", "file:///srv/git/x.git"} {
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
	for in, want := range map[string]Kind{
		"./rel":               KindLocal,
		"../rel":              KindLocal,
		"git+ssh://host/o/r":  KindSSH,
		"ssh+git://host/o/r":  KindSSH,
		"http://host/o/r.git": KindHTTPS,
		"just-a-name":         KindLocal,
	} {
		got, err := RemoteKind(in)
		if err != nil || got != want {
			t.Errorf("%s: got %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"git://host/r.git", "ftp://h/x"} {
		if k, err := RemoteKind(bad); err == nil || k != 0 {
			t.Errorf("RemoteKind(%s) = %v, %v; want 0 and an error", bad, k, err)
		}
	}

	t.Run("CheckRemote", func(t *testing.T) {
		if err := CheckRemote("https://h/o/r.git", "tok", "bad user", ""); err == nil || !strings.Contains(err.Error(), "credential_username") {
			t.Errorf("bad user: %v", err)
		}
		if err := CheckRemote("https://h/o/r.git", "tok", "ok_user", ""); err != nil {
			t.Errorf("good user: %v", err)
		}
		if err := CheckRemote("git://host/r.git", "", "", ""); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("unsupported kind: %v", err)
		}
	})
}

func TestResolveProgram(t *testing.T) {
	dir := "/cfg"
	for _, c := range []struct{ in, want string }{
		{"", ""}, {"/abs", "/abs"}, {"rel", filepath.Join(dir, "rel")},
	} {
		if got := resolve(dir, c.in); got != c.want {
			t.Errorf("resolve(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, c := range []struct{ in, want string }{
		{"", ""}, {"gitleaks", "gitleaks"},
		{"bin/gitleaks", filepath.Join(dir, "bin", "gitleaks")}, {"/usr/bin/gitleaks", "/usr/bin/gitleaks"},
	} {
		if got := ResolveProgram(dir, c.in); got != c.want {
			t.Errorf("ResolveProgram(%q) = %q, want %q", c.in, got, c.want)
		}
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
	// missing and invalid names are ErrUnknownRepo, even with a folder of that name
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": wall, "repos/Bad/warden.yaml": repoOK})
	for _, name := range []string{"no-such-repo", "Bad Name", "Bad"} {
		if _, err := LoadRepo(dir, name); !errors.Is(err, ErrUnknownRepo) {
			t.Errorf("LoadRepo(%q) = %v, want ErrUnknownRepo", name, err)
		}
	}
}

func TestExamplesLoad(t *testing.T) {
	c, err := LoadRepo("../../examples/warden", "hermetarium")
	if err != nil {
		t.Fatal(err)
	}
	if c.AgentName != "hermit-1" || c.Credential != "/etc/warden/keys/hermetarium" || !c.Rule("REF-NAMESPACE").Fires("refs/heads/main", false) {
		t.Fatalf("unexpected example config: %+v", c)
	}
}

func TestFmtDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{time.Second, "1s"},
		{time.Minute, "1m"},
		{10 * time.Minute, "10m"},
		{time.Hour, "1h"},
		{8760 * time.Hour, "8760h"},
		{0, "0s"},
		{90 * time.Second, "1m30s"},
	}
	for _, c := range cases {
		if got := fmtDur(c.d); got != c.want {
			t.Errorf("fmtDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
