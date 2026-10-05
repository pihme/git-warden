package backupguard

// Requirement tests for docs/backup-guard.md § Requirements (REQ-BG-*).
// One test per requirement or small group of closely related ones; the
// mapping is in docs/backup-guard-testspec.md.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
)

// qaFakeScript is a stand-in for git-everref that records its arguments and
// the credential environment it got, and behaves like everref where the
// guard looks at the result: tags sets everref-remote.backup.tags, add sets
// the protection everref would (origin/bad is refused), run --all prints
// QA_RUN_OUT / QA_RUN_LINES lines and exits QA_RUN_EXIT.
const qaFakeScript = `#!/bin/sh
echo "$*" >> "@LOG@"
env | grep -E '^(GIT_SSH_COMMAND|GIT_CONFIG_[A-Z0-9_]+|GIT_TERMINAL_PROMPT)=' >> "@ENVLOG@"
dir=$2
case " $* " in *" --version "*) echo "${QA_VERSION_OUT:-everref version v1.0.0}"; exit ${QA_VERSION_EXIT:-0};; esac
case " $* " in *" tags "*)
	if [ -n "$QA_TAGS_EXIT" ]; then printf '%s\n' "$QA_TAGS_OUT"; exit "$QA_TAGS_EXIT"; fi
	git -C "$dir" config --add everref-remote.backup.tags origin; exit $?;;
esac
case " $* " in *" add "*)
	for a in "$@"; do if [ "$a" = origin/bad ]; then echo "error: cannot protect origin/bad: reserved name" >&2; exit 2; fi; done
	for a in "$@"; do case "$a" in origin/*) git -C "$dir" config "everref.remotes/$a.remote" backup || exit 2;; esac; done
	exit 0;;
esac
case " $* " in *" run --all "*)
	if [ -n "$QA_RUN_SLEEP" ]; then exec sleep "$QA_RUN_SLEEP"; fi
	i=1; while [ "$i" -le "${QA_RUN_LINES:-0}" ]; do echo "line$i$QA_RUN_PAD"; i=$((i+1)); done
	if [ -n "$QA_RUN_OUT" ]; then printf '%s\n' "$QA_RUN_OUT"; fi
	exit ${QA_RUN_EXIT:-0};;
esac
exit 0
`

type qaFakeEverref struct{ bin, log, envlog string }

func qaFake(t *testing.T) qaFakeEverref {
	t.Helper()
	dir := t.TempDir()
	f := qaFakeEverref{filepath.Join(dir, "git-everref"), filepath.Join(dir, "calls.log"), filepath.Join(dir, "env.log")}
	s := strings.NewReplacer("@LOG@", f.log, "@ENVLOG@", f.envlog).Replace(qaFakeScript)
	if err := os.WriteFile(f.bin, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f qaFakeEverref) calls(t *testing.T) []string { return qaLines(t, f.log) }
func (f qaFakeEverref) env(t *testing.T) string     { return qaRead(t, f.envlog) }
func (f qaFakeEverref) config(extra string) string {
	return "everref:\n  path: " + f.bin + "\n" + extra
}
func (f qaFakeEverref) count(t *testing.T, sub string) int {
	n := 0
	for _, c := range f.calls(t) {
		if strings.Contains(" "+c+" ", sub) {
			n++
		}
	}
	return n
}

func qaRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

func qaLines(t *testing.T, path string) []string {
	var out []string
	for _, l := range strings.Split(qaRead(t, path), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func qaGuard(t *testing.T, dir string) (*Guard, []string) {
	t.Helper()
	g, repos, err := Preflight(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g, repos
}

// qaNotifier returns a notify.command that appends each warning to out.
func qaNotifier(t *testing.T, body string) (cmd, out string) {
	t.Helper()
	dir := t.TempDir()
	cmd, out = filepath.Join(dir, "notify.sh"), filepath.Join(dir, "warnings")
	if body == "" {
		body = `cat >> "` + out + `"`
	}
	if err := os.WriteFile(cmd, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd, out
}

func qaRawJournal(t *testing.T, stateDir string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range qaLines(t, filepath.Join(stateDir, "backup.jsonl")) {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("journal line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func qaFakeProgram(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func qaGitIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	return testutil.Try(dir, nil, args...)
}

// ---------- Preflight and prerequisites ----------

func TestREQ_BG_003_ConfigDirMustBeADirectory(t *testing.T) {
	f := qaFake(t)
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte(f.config("")), 0o600)
	for _, dir := range []string{filepath.Join(t.TempDir(), "missing"), file, ""} {
		if _, _, err := Preflight(context.Background(), dir, nil); err == nil {
			t.Errorf("preflight accepted configuration directory %q", dir)
		}
	}
}

func TestREQ_BG_004_BrokenDefaultsFailPreflight(t *testing.T) {
	f := qaFake(t)
	repo := map[string]string{"r": "remote: /srv/git/r.git\n"}
	for _, bad := range []string{"timeout: soon\n", "remote: /x\n", "everref:\n  path: \"\"\n", "state_dir: \"\"\n", "[unclosed\n"} {
		dir := writeConfig(t, f.config("")+bad, repo)
		if _, _, err := Preflight(context.Background(), dir, nil); err == nil {
			t.Errorf("preflight passed with defaults.yaml %q", bad)
		}
	}
}

func TestREQ_BG_005_GitMissing(t *testing.T) {
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: /srv/git/r.git\n"})
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Preflight(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "git not found") {
		t.Fatalf("preflight without git: %v", err)
	}
}

func TestREQ_BG_006_007_GitVersion(t *testing.T) {
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: /srv/git/r.git\n"})
	bin := qaFakeProgram(t, "git", `echo "$QA_GIT_VERSION"`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for out, ok := range map[string]bool{
		"git version 2.41.0":           false,
		"git version 1.99.9":           false,
		"git version 2.42.0":           true,
		"git version 2.47.3.windows.1": true,
		"git version 3.0.0":            true,
		"hello":                        false, // REQ-BG-007
		"git version x.y":              false, // REQ-BG-007
		"":                             false, // REQ-BG-007
	} {
		t.Setenv("QA_GIT_VERSION", out)
		_, _, err := Preflight(context.Background(), dir, nil)
		if (err == nil) != ok {
			t.Errorf("git version %q: preflight error %v, want ok=%v", out, err, ok)
		}
	}
}

func TestREQ_BG_008_018_EverrefLookup(t *testing.T) {
	f := qaFake(t)
	script, _ := os.ReadFile(f.bin)
	repo := map[string]string{"r": "remote: /srv/git/r.git\n"}

	// unset: git-everref on PATH
	onPath := t.TempDir()
	os.WriteFile(filepath.Join(onPath, "git-everref"), script, 0o755)
	os.WriteFile(filepath.Join(onPath, "my-everref"), script, 0o755)
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	g, _ := qaGuard(t, writeConfig(t, "", repo))
	if g.Everref != filepath.Join(onPath, "git-everref") {
		t.Errorf("default lookup found %s", g.Everref)
	}
	// a name without "/" is looked up on PATH
	dir := writeConfig(t, "everref:\n  path: my-everref\n", repo)
	if d, _ := LoadDefaults(dir); d.Everref != "my-everref" {
		t.Errorf("everref.path my-everref resolved to %s before the lookup", d.Everref)
	}
	if g, _ := qaGuard(t, dir); g.Everref != filepath.Join(onPath, "my-everref") {
		t.Errorf("my-everref found at %s", g.Everref)
	}
	// relative with "/": relative to the configuration directory, not to the working directory
	dir = writeConfig(t, "everref:\n  path: bin/git-everref\n", repo)
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(filepath.Join(dir, "bin", "git-everref"), script, 0o755)
	if g, _ := qaGuard(t, dir); g.Everref != filepath.Join(dir, "bin", "git-everref") {
		t.Errorf("relative everref.path found at %s", g.Everref)
	}
	// absolute: as is, even though another git-everref is on PATH
	if g, _ := qaGuard(t, writeConfig(t, f.config(""), repo)); g.Everref != f.bin {
		t.Errorf("absolute everref.path found at %s", g.Everref)
	}
}

func TestREQ_BG_009_EverrefNotFound(t *testing.T) {
	dir := writeConfig(t, "everref:\n  path: "+filepath.Join(t.TempDir(), "nope")+"\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	_, _, err := Preflight(context.Background(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "scripts/install-everref.sh") || !strings.Contains(err.Error(), "everref.path") {
		t.Fatalf("missing everref: %v", err)
	}
}

func TestREQ_BG_010_VersionCommandFails(t *testing.T) {
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: /srv/git/r.git\n"})
	t.Setenv("QA_VERSION_EXIT", "3")
	if _, _, err := Preflight(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "--version") {
		t.Fatalf("everref --version exiting 3: %v", err)
	}
}

func TestREQ_BG_011_VersionLineFormat(t *testing.T) {
	for out, want := range map[string]string{
		"everref version v1.0.0":                   "v1.0.0",
		"everref version v1.0.0\nbuilt 2026-01-01": "v1.0.0", // first line only
		"git-everref version dev":                  "dev",
		"x version a b":                            "b", // last field
		"everref Version v1":                       "",
		"everref version":                          "",
		"v1.0.0":                                   "",
		"\n\n":                                     "",
	} {
		if got := parseVersion(out); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", out, got, want)
		}
	}
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: /srv/git/r.git\n"})
	t.Setenv("QA_VERSION_OUT", "garbage")
	if _, _, err := Preflight(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "unexpected output") {
		t.Fatalf("unparsable --version: %v", err)
	}
}

func TestREQ_BG_014_NoRepos(t *testing.T) {
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), nil)
	os.MkdirAll(filepath.Join(dir, "repos", "empty"), 0o755)
	if _, _, err := Preflight(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "no repos configured") {
		t.Fatalf("no repos: %v", err)
	}
}

// ---------- defaults.yaml ----------

func TestREQ_BG_017_OnlyKnownDefaultsKeys(t *testing.T) {
	ok := "everref:\n  path: x\n  version: v1.0.0\nnotify:\n  command: [n]\nstate_dir: s\ntimeout: 1m\n"
	if _, err := LoadDefaults(writeConfig(t, ok, nil)); err != nil {
		t.Fatalf("all known keys: %v", err)
	}
	for _, bad := range []string{
		"remote: /srv/git/r.git\n",  // per-repo key
		"exclude_branches: [x]\n",   // per-repo key
		"gitleaks:\n  path: x\n",    // Push Guard key
		"rules: {}\n",               // Push Guard key
		"everref:\n  versoin: v1\n", // typo
		"notify:\n  cmd: [x]\n",     // typo
		"statedir: s\n",             // typo
	} {
		if _, err := LoadDefaults(writeConfig(t, bad, nil)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestREQ_BG_019_021_EmptyValues(t *testing.T) {
	for _, bad := range []string{"everref:\n  path: \"\"\n", "state_dir: \"\"\n", "state_dir: ''\n"} {
		if _, err := LoadDefaults(writeConfig(t, bad, nil)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestREQ_BG_020_RelativeStateDir(t *testing.T) {
	dir := writeConfig(t, "state_dir: var/state\n", nil)
	d, err := LoadDefaults(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.StateDir != filepath.Join(dir, "var", "state") {
		t.Fatalf("state_dir resolved to %s", d.StateDir)
	}
}

func TestREQ_BG_022_Timeout(t *testing.T) {
	for v, want := range map[string]time.Duration{"30m": 30 * time.Minute, "1h30m": 90 * time.Minute, "1s": time.Second, "1ns": 1} {
		d, err := LoadDefaults(writeConfig(t, "timeout: "+v+"\n", nil))
		if err != nil || d.Timeout != want {
			t.Errorf("timeout %s: %v %v", v, d, err)
		}
	}
	for _, bad := range []string{"0s", "0", "-1m", "30", "soon", "''", "[1m]"} {
		if _, err := LoadDefaults(writeConfig(t, "timeout: "+bad+"\n", nil)); err == nil {
			t.Errorf("accepted timeout %s", bad)
		}
	}
}

// ---------- backup.yaml ----------

func TestREQ_BG_023_RepoNeedsBackupYAML(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{"a": "remote: /srv/a.git\n"})
	testutil.WriteFiles(t, dir, map[string]string{"repos/c/push.yaml": "x: 1\n", "repos/file.txt": "x\n"})
	os.MkdirAll(filepath.Join(dir, "repos", "b"), 0o755)
	repos, err := Repos(dir)
	if err != nil || strings.Join(repos, ",") != "a" {
		t.Fatalf("Repos = %v, %v", repos, err)
	}
	if _, err := LoadRepo(dir, "b"); !errors.Is(err, ErrUnknownRepo) {
		t.Errorf("repo b without backup.yaml: %v", err)
	}
}

func TestREQ_BG_024_RepoNames(t *testing.T) {
	good := []string{"a", "a.b_c-1", "0", "r..x"}
	repos := map[string]string{}
	for _, n := range good {
		repos[n] = "remote: /srv/r.git\n"
	}
	dir := writeConfig(t, "", repos)
	for _, n := range good {
		if _, err := LoadRepo(dir, n); err != nil {
			t.Errorf("valid name %q: %v", n, err)
		}
	}
	for _, n := range []string{"A", "Repo", ".hidden", ".", "..", "a b", "a/b", "../a", "", "ä", "a:b"} {
		if _, err := LoadRepo(dir, n); err == nil || !strings.Contains(err.Error(), "invalid repo name") {
			t.Errorf("name %q: %v", n, err)
		}
	}
}

func qaLoadRepo(t *testing.T, content string) (*Repo, error) {
	t.Helper()
	dir := writeConfig(t, "", map[string]string{"r": content})
	return LoadRepo(dir, "r")
}

func TestREQ_BG_025_OnlyKnownRepoKeys(t *testing.T) {
	if _, err := qaLoadRepo(t, "remote: git@h:o/r.git\ncredential: k\nknown_hosts: kh\nexclude_branches: [x]\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := qaLoadRepo(t, "remote: https://h/o/r.git\ncredential: t\ncredential_username: u\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"state_dir: s\n", "timeout: 1m\n", "rules: {}\n", "everref:\n  path: x\n", "exclude_branch: [x]\n", "remotes: [x]\n"} {
		if _, err := qaLoadRepo(t, "remote: /srv/r.git\n"+bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestREQ_BG_026_RemoteRequired(t *testing.T) {
	for _, c := range []string{"", "exclude_branches: [x]\n", "remote: ''\n"} {
		if _, err := qaLoadRepo(t, c); err == nil || !strings.Contains(err.Error(), "remote is required") {
			t.Errorf("backup.yaml %q: %v", c, err)
		}
	}
}

func TestREQ_BG_027_RemoteKinds(t *testing.T) {
	for _, c := range []struct {
		remote, extra string
		ok            bool
	}{
		{"ssh://git@h/o/r.git", "credential: k\n", true},
		{"git+ssh://git@h/o/r.git", "credential: k\n", true},
		{"ssh+git://git@h/o/r.git", "credential: k\n", true},
		{"git@h:o/r.git", "credential: k\n", true},
		{"h:o/r.git", "credential: k\n", true},
		{"https://h/o/r.git", "credential: t\n", true},
		{"http://h/o/r.git", "credential: t\n", true},
		{"/srv/git/r.git", "", true},
		{"file:///srv/git/r.git", "", true},
		{"ftp://h/o/r.git", "credential: t\n", false},
		{"git://h/o/r.git", "", false},
		{"git://h/o/r.git", "credential: t\n", false},
		{"s3://bucket/r", "credential: t\n", false},
	} {
		_, err := qaLoadRepo(t, "remote: '"+c.remote+"'\n"+c.extra)
		if (err == nil) != c.ok {
			t.Errorf("remote %s (%q): %v, want ok=%v", c.remote, c.extra, err, c.ok)
		}
	}
}

func TestREQ_BG_028_RelativeLocalRemote(t *testing.T) {
	for _, r := range []string{"r.git", "./r.git", "../r.git", "srv/git/r.git"} {
		if _, err := qaLoadRepo(t, "remote: "+r+"\n"); err == nil {
			t.Errorf("accepted relative remote %s", r)
		}
	}
}

func TestREQ_BG_029_RemoteNeedsCredential(t *testing.T) {
	for _, r := range []string{"git@h:o/r.git", "ssh://git@h/o/r.git", "https://h/o/r.git", "http://h/o/r.git"} {
		if _, err := qaLoadRepo(t, "remote: "+r+"\n"); err == nil {
			t.Errorf("accepted %s without credential", r)
		}
	}
}

func TestREQ_BG_030_LocalRemoteNoCredential(t *testing.T) {
	for _, r := range []string{"/srv/git/r.git", "file:///srv/git/r.git"} {
		if _, err := qaLoadRepo(t, "remote: "+r+"\ncredential: k\n"); err == nil {
			t.Errorf("accepted %s with credential", r)
		}
	}
}

func TestREQ_BG_031_UsernameOnlyForHTTPS(t *testing.T) {
	for _, c := range []string{
		"remote: git@h:o/r.git\ncredential: k\ncredential_username: u\n",
		"remote: ssh://git@h/o/r.git\ncredential: k\ncredential_username: u\n",
		"remote: /srv/git/r.git\ncredential_username: u\n",
	} {
		if _, err := qaLoadRepo(t, c); err == nil {
			t.Errorf("accepted %q", c)
		}
	}
	if _, err := qaLoadRepo(t, "remote: http://h/o/r.git\ncredential: t\ncredential_username: u\n"); err != nil {
		t.Errorf("http with credential_username: %v", err)
	}
}

func TestREQ_BG_032_UsernameCharset(t *testing.T) {
	for u, ok := range map[string]bool{
		"u":                      true,
		"gitlab+deploy-token-1":  true,
		"a.b_c+d@e-f":            true,
		"Z9":                     true,
		strings.Repeat("a", 128): true,
		strings.Repeat("a", 129): false,
		"a b":                    false,
		"a:b":                    false,
		"a/b":                    false,
		"a'b":                    false,
		"a$b":                    false,
		"ä":                      false,
	} {
		_, err := qaLoadRepo(t, "remote: https://h/o/r.git\ncredential: t\ncredential_username: \""+u+"\"\n")
		if (err == nil) != ok {
			t.Errorf("credential_username %q: %v, want ok=%v", u, err, ok)
		}
	}
}

func TestREQ_BG_033_KnownHostsOnlyForSSH(t *testing.T) {
	for _, c := range []string{
		"remote: https://h/o/r.git\ncredential: t\nknown_hosts: kh\n",
		"remote: /srv/git/r.git\nknown_hosts: kh\n",
		"remote: file:///srv/git/r.git\nknown_hosts: kh\n",
	} {
		if _, err := qaLoadRepo(t, c); err == nil {
			t.Errorf("accepted %q", c)
		}
	}
}

func TestREQ_BG_034_RelativeCredentialPaths(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{
		"rel": "remote: git@h:o/r.git\ncredential: keys/id\nknown_hosts: ssh/kh\n",
		"abs": "remote: git@h:o/r.git\ncredential: /etc/k\nknown_hosts: /etc/kh\n",
	})
	r, err := LoadRepo(dir, "rel")
	if err != nil || r.Credential != filepath.Join(dir, "keys", "id") || r.KnownHosts != filepath.Join(dir, "ssh", "kh") {
		t.Fatalf("relative: %+v %v", r, err)
	}
	r, err = LoadRepo(dir, "abs")
	if err != nil || r.Credential != "/etc/k" || r.KnownHosts != "/etc/kh" {
		t.Fatalf("absolute: %+v %v", r, err)
	}
}

func TestREQ_BG_035_ExcludeAnchored(t *testing.T) {
	r, err := qaLoadRepo(t, "remote: /srv/r.git\nexclude_branches: ['feat|fix', 'tmp/.*', 'main']\n")
	if err != nil {
		t.Fatal(err)
	}
	for b, want := range map[string]bool{
		"feat": true, "fix": true, "feature": false, "prefix": false, "my-feat": false,
		"tmp/x": true, "tmp/": true, "a/tmp/x": false, "tmp": false,
		"main": true, "refs/heads/main": false, "main2": false,
	} {
		if r.Excluded(b) != want {
			t.Errorf("Excluded(%q) = %v, want %v", b, !want, want)
		}
	}
}

func TestREQ_BG_036_InvalidExcludeRegexp(t *testing.T) {
	for _, p := range []string{"(", "[a-", "*x", "a{2,1}"} {
		if _, err := qaLoadRepo(t, "remote: /srv/r.git\nexclude_branches: ['"+p+"']\n"); err == nil || !strings.Contains(err.Error(), "exclude_branches") {
			t.Errorf("pattern %q: %v", p, err)
		}
	}
}

// ---------- Credentials ----------

func TestREQ_BG_038_039_042_SSHCommand(t *testing.T) {
	sshLog := filepath.Join(t.TempDir(), "ssh.log")
	ssh := qaFakeProgram(t, "ssh", `echo "$*" >> "`+sshLog+`"; echo "fake ssh: no network" >&2; exit 255`)
	t.Setenv("PATH", ssh+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := "-F none -i '%s' -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes"
	for _, c := range []struct {
		name, extra string
		kh          bool
	}{{"with known_hosts", "known_hosts: ssh/known_hosts\n", true}, {"without known_hosts", "", false}} {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(sshLog)
			f := qaFake(t)
			dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: git@example.invalid:o/r.git\ncredential: keys/id\n" + c.extra})
			g, _ := qaGuard(t, dir)
			res := g.RunRepo(context.Background(), "r")
			if res.OK || !strings.Contains(res.Error, "list remote refs") {
				t.Fatalf("run: %+v", res)
			}
			want := "GIT_SSH_COMMAND=ssh " + fmt.Sprintf(base, filepath.Join(dir, "keys", "id"))
			khOpt := "-o UserKnownHostsFile='" + filepath.Join(dir, "ssh", "known_hosts") + "' -o GlobalKnownHostsFile=/dev/null"
			if c.kh {
				want += " " + khOpt
			}
			// REQ-BG-042: everref got the same environment ...
			env := f.env(t)
			found := false
			for _, l := range strings.Split(env, "\n") {
				if strings.HasPrefix(l, "GIT_SSH_COMMAND=") {
					found = true
					if l != want {
						t.Errorf("everref GIT_SSH_COMMAND\n got %s\nwant %s", l, want)
					}
				}
			}
			if !found {
				t.Fatalf("everref got no GIT_SSH_COMMAND:\n%s", env)
			}
			// ... as the guard's own git (ls-remote through the fake ssh)
			sshArgs := qaRead(t, sshLog)
			if !strings.Contains(sshArgs, "-F none -i "+filepath.Join(dir, "keys", "id")+" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes") {
				t.Errorf("ls-remote ssh arguments: %s", sshArgs)
			}
			if c.kh != strings.Contains(sshArgs, "UserKnownHostsFile="+filepath.Join(dir, "ssh", "known_hosts")) ||
				c.kh != strings.Contains(sshArgs, "GlobalKnownHostsFile=/dev/null") {
				t.Errorf("known_hosts options (want %v): %s", c.kh, sshArgs)
			}
		})
	}
}

func TestREQ_BG_040_041_HTTPSHelper(t *testing.T) {
	const token = "qa-synthetic-token-0815"
	for _, c := range []struct{ extra, user string }{{"", "x-access-token"}, {"credential_username: gitlab+deploy-token-1\n", "gitlab+deploy-token-1"}} {
		f := qaFake(t)
		dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: https://127.0.0.1:1/o/r.git\ncredential: token\n" + c.extra})
		os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600)
		g, _ := qaGuard(t, dir)
		res := g.RunRepo(context.Background(), "r")
		if res.OK {
			t.Fatalf("run against a closed port succeeded: %+v", res)
		}
		env := f.env(t)
		for _, want := range []string{
			"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=\n", "GIT_CONFIG_KEY_1=credential.helper",
			"username=%s\\n' '" + c.user + "'", "cat '" + filepath.Join(dir, "token") + "'", "GIT_TERMINAL_PROMPT=0",
		} {
			if !strings.Contains(env+"\n", want) {
				t.Errorf("everref environment lacks %q:\n%s", want, env)
			}
		}
		// REQ-BG-041: the token itself is nowhere
		for what, s := range map[string]string{
			"everref environment":   env,
			"everref command lines": qaRead(t, f.log),
			"bridge config":         qaRead(t, filepath.Join(BridgePath(g.Defaults.StateDir, "r"), ".git", "config")),
			"backup config":         qaRead(t, filepath.Join(BackupPath(g.Defaults.StateDir, "r"), "config")),
			"backup.jsonl":          qaRead(t, filepath.Join(g.Defaults.StateDir, "backup.jsonl")),
			"error":                 res.Error + res.Output,
		} {
			if strings.Contains(s, token) {
				t.Errorf("token in %s", what)
			}
		}
	}
}

// ---------- Branches and tags ----------

func TestREQ_BG_043_LsRemoteFailureFails(t *testing.T) {
	f := qaFake(t)
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + filepath.Join(t.TempDir(), "gone.git") + "\n"})
	g, _ := qaGuard(t, dir)
	res := g.RunRepo(context.Background(), "r")
	if res.OK || !strings.Contains(res.Error, "list remote refs") || res.EverrefExit != -1 {
		t.Fatalf("result: %+v", res)
	}
	if f.count(t, " add ") != 0 || f.count(t, " run ") != 0 {
		t.Fatalf("everref called after a failed ls-remote: %v", f.calls(t))
	}
}

func TestREQ_BG_044_OnlyHeadsAreBranches(t *testing.T) {
	f := qaFake(t)
	bare, w := remoteWith(t, "main")
	w.Git("tag", "v1")
	w.Git("push", "-q", "origin", "v1", "HEAD:refs/pull/1/head", "HEAD:refs/notes/commits", "HEAD:refs/merge-requests/2/head")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	res := g.RunRepo(context.Background(), "r")
	if !res.OK || res.Branches != 1 || strings.Join(res.Added, ",") != "main" {
		t.Fatalf("result: %+v", res)
	}
	for _, c := range f.calls(t) {
		if strings.Contains(c, " add ") && strings.Contains(c, "pull") || strings.Contains(c, "notes") || strings.Contains(c, "merge-requests") || strings.Contains(c, "origin/v1") {
			t.Errorf("non-branch ref protected: %s", c)
		}
	}
}

func TestREQ_BG_045_AddOnFirstSight(t *testing.T) {
	f := qaFake(t)
	bare, w := remoteWith(t, "main", "dev", "tmp/x")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\nexclude_branches: ['tmp/.*']\n"})
	g, _ := qaGuard(t, dir)
	ctx := context.Background()
	if res := g.RunRepo(ctx, "r"); !res.OK || strings.Join(res.Added, ",") != "dev,main" {
		t.Fatalf("first run: %+v", res)
	}
	if !strings.Contains(strings.Join(f.calls(t), "\n"), "-q add origin/dev origin/main --remote backup") {
		t.Fatalf("add call: %v", f.calls(t))
	}
	if res := g.RunRepo(ctx, "r"); !res.OK || len(res.Added) != 0 || f.count(t, " add ") != 1 {
		t.Fatalf("second run re-added: %+v %v", res, f.calls(t))
	}
	w.Git("branch", "late")
	w.Git("push", "-q", "origin", "late")
	if res := g.RunRepo(ctx, "r"); !res.OK || strings.Join(res.Added, ",") != "late" {
		t.Fatalf("third run: %+v", res)
	}
	if last := f.calls(t); !strings.Contains(strings.Join(last, "\n"), "-q add origin/late --remote backup") {
		t.Fatalf("calls: %v", last)
	}
}

func TestREQ_BG_046_AddBatchesOf100(t *testing.T) {
	f := qaFake(t)
	bare, w := remoteWith(t, "main")
	head := w.Head()
	var in strings.Builder
	for i := 0; i < 229; i++ {
		fmt.Fprintf(&in, "create refs/heads/b%03d %s\n", i, head)
	}
	cmd := exec.Command("git", "update-ref", "--stdin")
	cmd.Dir, cmd.Stdin = bare, strings.NewReader(in.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	res := g.RunRepo(context.Background(), "r")
	if !res.OK || len(res.Added) != 230 {
		t.Fatalf("result: ok=%v added=%d err=%s", res.OK, len(res.Added), res.Error)
	}
	var sizes []int
	for _, c := range f.calls(t) {
		if strings.Contains(c, " add ") {
			sizes = append(sizes, strings.Count(c, " origin/"))
		}
	}
	if fmt.Sprint(sizes) != "[100 100 30]" {
		t.Fatalf("add call sizes: %v", sizes)
	}
}

// TestREQ_BG_050_051_ProtectionsStay: a branch deleted upstream and a branch
// excluded later both stay protected; the excluded one keeps being recorded.
func TestREQ_BG_050_051_ProtectionsStay(t *testing.T) {
	realEverref(t)
	ctx := context.Background()
	bare, w := remoteWith(t, "main", "feature", "keep")
	cfg := func(extra string) string {
		return writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n" + extra})
	}
	dir := cfg("")
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	time.Sleep(1100 * time.Millisecond)
	w.Git("push", "-q", "origin", ":feature")
	w.Commit("two", map[string]string{"b.txt": "2\n"})
	w.Git("push", "-q", "origin", "main")
	newMain := w.Head()
	testutil.WriteFiles(t, dir, map[string]string{"repos/r/backup.yaml": "remote: " + bare + "\nexclude_branches: ['main']\n"})
	res := g.RunRepo(ctx, "r")
	if !res.OK || res.Excluded != 1 || res.Branches != 1 || res.Deleted != 1 {
		t.Fatalf("second run: %+v", res)
	}
	bridge := BridgePath(g.Defaults.StateDir, "r")
	for _, b := range []string{"feature", "main"} {
		if _, err := qaGitIn(t, bridge, "config", "everref.remotes/origin/"+b+".remote"); err != nil {
			t.Errorf("protection of %s removed: %v", b, err)
		}
	}
	refs := backupRefs(t, BackupPath(g.Defaults.StateDir, "r"))
	if countPrefix(refs, "refs/heads/everref/remotes/origin/feature/deleted_") != 1 {
		t.Errorf("no tombstone for feature:\n%s", strings.Join(refs, "\n"))
	}
	recorded := false
	for _, r := range refs {
		if strings.HasPrefix(r, "refs/heads/everref/remotes/origin/main/created_") && testutil.Run(t, BackupPath(g.Defaults.StateDir, "r"), "rev-parse", r) == newMain {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("excluded main's new commit %s not recorded:\n%s", newMain, strings.Join(refs, "\n"))
	}
}

func TestREQ_BG_052_TagsNotExcludable(t *testing.T) {
	realEverref(t)
	bare, w := remoteWith(t, "main")
	w.Git("tag", "v1")
	w.Git("tag", "release-1")
	w.Git("push", "-q", "origin", "v1", "release-1")
	dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\nexclude_branches: ['v1', 'release-.*', 'tags/.*']\n"})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(context.Background(), "r"); !res.OK || res.Excluded != 0 {
		t.Fatalf("run: %+v", res)
	}
	refs := backupRefs(t, BackupPath(g.Defaults.StateDir, "r"))
	for _, tag := range []string{"v1", "release-1"} {
		if countPrefix(refs, "refs/tags/everref/remotes/origin/tags/"+tag+"/created_") != 1 {
			t.Errorf("tag %s not recorded:\n%s", tag, strings.Join(refs, "\n"))
		}
	}
}

func TestREQ_BG_053_BridgedTagsOnOnce(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if res := g.RunRepo(ctx, "r"); !res.OK {
			t.Fatalf("run %d: %+v", i, res)
		}
	}
	if n := f.count(t, " -q tags --remote backup on --source origin "); n != 1 {
		t.Fatalf("tags on called %d times in 3 runs: %v", n, f.calls(t))
	}
	bridge := BridgePath(g.Defaults.StateDir, "r")
	testutil.Run(t, bridge, "config", "--unset-all", "everref-remote.backup.tags")
	testutil.Run(t, bridge, "config", "--add", "everref-remote.backup.tags", "upstream")
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("run after the setting was lost: %+v", res)
	}
	if n := f.count(t, " tags --remote backup on --source origin "); n != 2 {
		t.Fatalf("tags on not redone when origin was missing: %d", n)
	}
}

func TestREQ_BG_054_NoBranchesFails(t *testing.T) {
	f := qaFake(t)
	bare := testutil.Init(t, true)
	w := testutil.Init(t, false)
	w.Commit("one", map[string]string{"a": "1\n"})
	w.Git("tag", "v1")
	w.Git("push", "-q", bare.Dir, "v1")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare.Dir + "\n"})
	g, _ := qaGuard(t, dir)
	res := g.RunRepo(context.Background(), "r")
	if res.OK || res.Branches != 0 || res.EverrefExit != -1 || !strings.Contains(res.Error, "no branches") {
		t.Fatalf("tag-only remote: %+v", res)
	}
	if f.count(t, " run --all ") != 0 {
		t.Fatal("everref run started for a remote without branches")
	}
}

// ---------- Setup and state ----------

func TestREQ_BG_059_060_061_BridgeLayout(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config("state_dir: st\n"), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(context.Background(), "r"); !res.OK {
		t.Fatalf("run: %+v", res)
	}
	bridge, backup := filepath.Join(dir, "st", "repos", "r", "bridge"), filepath.Join(dir, "st", "repos", "r", "backup.git")
	if BridgePath(g.Defaults.StateDir, "r") != bridge || BackupPath(g.Defaults.StateDir, "r") != backup {
		t.Fatalf("paths: %s %s", BridgePath(g.Defaults.StateDir, "r"), BackupPath(g.Defaults.StateDir, "r"))
	}
	if got := testutil.Run(t, bridge, "rev-parse", "--is-bare-repository"); got != "false" {
		t.Errorf("bridge bare: %s", got)
	}
	if got := testutil.Run(t, backup, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Errorf("backup bare: %s", got)
	}
	for key, want := range map[string]string{
		"remote.origin.url": bare, "remote.backup.url": backup,
		"user.name": "git-warden backup-guard", "user.email": "backup-guard@localhost", "commit.gpgsign": "false",
	} {
		if got := testutil.Run(t, bridge, "config", "--local", key); got != want {
			t.Errorf("bridge %s = %q, want %q", key, got, want)
		}
	}
	if out, err := qaGitIn(t, bridge, "config", "--get-all", "remote.backup.fetch"); err == nil {
		t.Errorf("backup remote has a fetch refspec: %s", out)
	}
}

func TestREQ_BG_063_SetupInTempDirs(t *testing.T) {
	f := qaFake(t)
	real := mustLook(t, "git")
	log := filepath.Join(t.TempDir(), "git.log")
	wrap := qaFakeProgram(t, "git", `echo "$PWD|$*" >> "`+log+`"; exec "`+real+`" "$@"`)
	bare, _ := remoteWith(t, "main")
	t.Setenv("PATH", wrap+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(context.Background(), "r"); !res.OK {
		t.Fatalf("run: %+v", res)
	}
	base := filepath.Join(g.Defaults.StateDir, "repos", "r")
	var initBackup, initBridge, addOrigin, addBackup bool
	for _, l := range qaLines(t, log) {
		pwd, args, _ := strings.Cut(l, "|")
		switch {
		case strings.HasPrefix(args, "init --quiet --bare "+filepath.Join(base, ".setup-backup.git-")):
			initBackup = true
		case strings.HasPrefix(args, "init --quiet "+filepath.Join(base, ".setup-bridge-")):
			initBridge = true
		case strings.HasPrefix(args, "remote add origin "):
			addOrigin = strings.HasPrefix(pwd, filepath.Join(base, ".setup-bridge-"))
		case strings.HasPrefix(args, "remote add backup "):
			addBackup = strings.HasPrefix(pwd, filepath.Join(base, ".setup-bridge-"))
		}
	}
	if !initBackup || !initBridge || !addOrigin || !addBackup {
		t.Fatalf("setup not in .setup-* directories (backup %v, bridge %v, origin %v, backup remote %v):\n%s",
			initBackup, initBridge, addOrigin, addBackup, qaRead(t, log))
	}
}

func TestREQ_BG_064_LeftoversRemoved(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	base := filepath.Join(g.Defaults.StateDir, "repos", "r")
	plant := func() {
		testutil.WriteFiles(t, base, map[string]string{".setup-bridge-123/.git/HEAD": "x\n", ".setup-backup.git-9/HEAD": "x\n", ".setup-file": "x\n"})
	}
	plant() // before the first run
	if res := g.RunRepo(context.Background(), "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	plant()
	if res := g.RunRepo(context.Background(), "r"); !res.OK {
		t.Fatalf("second run: %+v", res)
	}
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if e.Name() != "bridge" && e.Name() != "backup.git" {
			t.Errorf("left over: %s", e.Name())
		}
	}
}

func TestREQ_BG_065_IncompleteDirs(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	for _, name := range []string{"bridge", "backup.git"} {
		t.Run(name+" not empty", func(t *testing.T) {
			dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
			g, _ := qaGuard(t, dir)
			base := filepath.Join(g.Defaults.StateDir, "repos", "r")
			testutil.WriteFiles(t, base, map[string]string{name + "/precious.txt": "keep me\n"})
			res := g.RunRepo(context.Background(), "r")
			if res.OK || !strings.Contains(res.Error, "not a complete repository") {
				t.Fatalf("run: %+v", res)
			}
			if qaRead(t, filepath.Join(base, name, "precious.txt")) != "keep me\n" {
				t.Fatal("the incomplete directory was changed")
			}
		})
		t.Run(name+" empty", func(t *testing.T) {
			dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
			g, _ := qaGuard(t, dir)
			base := filepath.Join(g.Defaults.StateDir, "repos", "r")
			os.MkdirAll(filepath.Join(base, name), 0o700)
			if res := g.RunRepo(context.Background(), "r"); !res.OK {
				t.Fatalf("run: %+v", res)
			}
		})
	}
}

// TestREQ_BG_066_RefsOnlyGrow: after force push, branch deletion and a moved
// tag, every ref of the first run is still there and the second run only
// fast-forwarded it.
func TestREQ_BG_066_RefsOnlyGrow(t *testing.T) {
	realEverref(t)
	ctx := context.Background()
	bare, w := remoteWith(t, "main", "feature")
	w.Git("tag", "v1")
	w.Git("push", "-q", "origin", "v1")
	dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	backup := BackupPath(g.Defaults.StateDir, "r")
	snapshot := func() map[string]string {
		m := map[string]string{}
		for _, l := range strings.Split(testutil.Run(t, backup, "for-each-ref", "--format=%(refname) %(objectname)"), "\n") {
			if r, o, ok := strings.Cut(l, " "); ok {
				m[r] = o
			}
		}
		return m
	}
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	before := snapshot()
	time.Sleep(1100 * time.Millisecond)
	w.Git("commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
	w.Git("push", "-q", "-f", "origin", "main")
	w.Git("push", "-q", "origin", ":feature")
	w.Git("tag", "-f", "v1", "HEAD")
	w.Git("push", "-q", "-f", "origin", "v1")
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("second run: %+v", res)
	}
	after := snapshot()
	if len(after) <= len(before) {
		t.Fatalf("backup did not grow: %d -> %d refs", len(before), len(after))
	}
	for ref, old := range before {
		now, ok := after[ref]
		if !ok {
			t.Errorf("ref deleted: %s", ref)
			continue
		}
		if now != old {
			if _, err := qaGitIn(t, backup, "merge-base", "--is-ancestor", old, now); err != nil {
				t.Errorf("ref %s moved from %s to %s, not a fast-forward", ref, old, now)
			}
		}
	}
}

// ---------- Run ----------

func TestREQ_BG_069_FailedRepoDoesNotStopOthers(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{
		"a": "remote: " + filepath.Join(t.TempDir(), "gone.git") + "\n",
		"b": "remote: " + bare + "\n",
	})
	g, repos := qaGuard(t, dir)
	err := g.RunAll(context.Background(), repos)
	if err == nil || !strings.Contains(err.Error(), "backup failed for a") || strings.Contains(err.Error(), "b") && strings.Contains(err.Error(), ", b") {
		t.Fatalf("RunAll: %v", err)
	}
	j := readJournal(t, g.Defaults.StateDir)
	if len(j) != 2 || j[0].Repo != "a" || j[0].OK || j[1].Repo != "b" || !j[1].OK {
		t.Fatalf("journal: %+v", j)
	}
}

func TestREQ_BG_070_072_RunAllWithoutTrigger(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main", "dev")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	for i := 0; i < 2; i++ {
		if res := g.RunRepo(context.Background(), "r"); !res.OK {
			t.Fatalf("run: %+v", res)
		}
	}
	if n := f.count(t, " -C "+BridgePath(g.Defaults.StateDir, "r")+" run --all "); n != 2 {
		t.Fatalf("run --all in the bridge %d times: %v", n, f.calls(t))
	}
	for _, c := range f.calls(t) {
		if strings.Contains(c, "--trigger") || strings.Contains(c, "--schedule") || strings.Contains(c, " trigger") || strings.Contains(c, " schedule") {
			t.Errorf("everref call installs a trigger or schedule: %s", c)
		}
	}
}

func TestREQ_BG_073_ExampleTimer(t *testing.T) {
	timer, err := os.ReadFile(filepath.Join("..", "..", "examples", "backup", "systemd", "backup-guard.timer"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^OnUnitActiveSec=15min$`).Match(timer) && !regexp.MustCompile(`(?m)^OnCalendar=\*:0/15$`).Match(timer) {
		t.Errorf("timer does not fire every 15 minutes:\n%s", timer)
	}
	service, err := os.ReadFile(filepath.Join("..", "..", "examples", "backup", "systemd", "backup-guard.service"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^ExecStart=\S*backup-guard run --config \S+$`).Match(service) || !strings.Contains(string(service), "Type=oneshot") {
		t.Errorf("service does not run backup-guard run once:\n%s", service)
	}
}

func TestREQ_BG_074_TimeoutKillsEverref(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config("timeout: 3s\n"), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	t.Setenv("QA_RUN_SLEEP", "120")
	start := time.Now()
	res := g.RunRepo(context.Background(), "r")
	if res.OK || res.Added == nil {
		t.Fatalf("run: %+v", res)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("run took %v with timeout 3s", d)
	}
}

// TestHelperQALock is not a test: TestREQ_BG_076 starts the test binary with
// QA_LOCK_HELPER=<state dir> to hold a repo lock in a process of its own.
func TestHelperQALock(t *testing.T) {
	dir := os.Getenv("QA_LOCK_HELPER")
	if dir == "" {
		t.Skip("helper process only")
	}
	if _, err := lock(dir, "r"); err != nil {
		os.Exit(3)
	}
	fmt.Println("locked")
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestREQ_BG_076_LockReleasedOnKill(t *testing.T) {
	state := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperQALock$")
	cmd.Env = append(os.Environ(), "QA_LOCK_HELPER="+state)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "locked" {
		cmd.Process.Kill()
		t.Fatalf("helper: %q", line)
	}
	fd, err := os.Open(filepath.Join(state, "locks", "r.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("lock not held by the helper")
	}
	cmd.Process.Signal(syscall.SIGKILL)
	cmd.Wait()
	done := make(chan error, 1)
	go func() {
		unlock, err := lock(state, "r")
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lock of a killed process still blocks")
	}
}

// ---------- State ----------

func TestREQ_BG_078_079_JournalLine(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main", "tmp")
	dir := writeConfig(t, f.config(""), map[string]string{
		"r":    "remote: " + bare + "\nexclude_branches: [tmp]\n",
		"none": "remote: " + bare + "\nexclude_branches: ['.*']\n",
	})
	g, _ := qaGuard(t, dir)
	ctx := context.Background()
	g.RunRepo(ctx, "r") // ok
	t.Setenv("QA_RUN_EXIT", "1")
	t.Setenv("QA_RUN_OUT", "error: [origin/main] skipped")
	g.RunRepo(ctx, "r")    // everref run fails
	g.RunRepo(ctx, "none") // fails before run
	j := qaRawJournal(t, g.Defaults.StateDir)
	if len(j) != 3 {
		t.Fatalf("%d journal lines for 3 runs", len(j))
	}
	for i, m := range j {
		for _, k := range []string{"time", "guard", "repo", "ok", "everref", "branches", "everref_exit", "duration_ms"} {
			if _, ok := m[k]; !ok {
				t.Errorf("line %d lacks %q: %v", i, k, m)
			}
		}
		ts, _ := m["time"].(string)
		if tm, err := time.Parse(time.RFC3339Nano, ts); err != nil || !strings.HasSuffix(ts, "Z") || tm.Location() != time.UTC {
			t.Errorf("line %d time %q is not UTC", i, ts)
		}
		if m["guard"] != "backup" || m["everref"] != "v1.0.0" {
			t.Errorf("line %d: %v", i, m)
		}
	}
	if j[0]["ok"] != true || j[0]["everref_exit"] != 0.0 || j[0]["branches"] != 1.0 || j[0]["excluded"] != 1.0 || j[0]["error"] != nil {
		t.Errorf("ok line: %v", j[0])
	}
	if j[1]["ok"] != false || j[1]["everref_exit"] != 1.0 || j[1]["error"] == nil || !strings.Contains(fmt.Sprint(j[1]["output"]), "skipped") {
		t.Errorf("failed line: %v", j[1])
	}
	if j[2]["ok"] != false || j[2]["everref_exit"] != -1.0 || j[2]["branches"] != 0.0 || j[2]["excluded"] != 2.0 {
		t.Errorf("no-branch line: %v", j[2])
	}
}

// TestREQ_BG_081_OutputTail: output is the tail of everref's output. With
// long lines the 4000-byte cap must still keep the end, where everref
// prints the error.
func TestREQ_BG_081_OutputTail(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	ctx := context.Background()
	t.Setenv("QA_RUN_EXIT", "1")

	t.Setenv("QA_RUN_LINES", "100")
	res := g.RunRepo(ctx, "r")
	lines := strings.Split(res.Output, "\n")
	if len(lines) != 40 || lines[0] != "line61" || lines[39] != "line100" {
		t.Errorf("short lines: %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}

	t.Setenv("QA_RUN_PAD", " "+strings.Repeat("x", 200))
	t.Setenv("QA_RUN_OUT", "error: the last line says what failed")
	res = g.RunRepo(ctx, "r")
	if len(res.Output) > 4000+len("…") {
		t.Errorf("output is %d bytes", len(res.Output))
	}
	if !strings.Contains(res.Output, "error: the last line says what failed") {
		t.Errorf("the end of everref's output was cut off; output ends with %q", res.Output[max(0, len(res.Output)-80):])
	}

	t.Setenv("QA_RUN_PAD", "")
	t.Setenv("QA_RUN_LINES", "0")
	t.Setenv("QA_TAGS_EXIT", "2")
	t.Setenv("QA_TAGS_OUT", "error: tags source origin refused")
	testutil.Run(t, BridgePath(g.Defaults.StateDir, "r"), "config", "--unset-all", "everref-remote.backup.tags")
	res = g.RunRepo(ctx, "r")
	if res.OK || !strings.Contains(res.Output, "tags source origin refused") {
		t.Errorf("failed tags call: %+v", res)
	}
}

func TestREQ_BG_082_JournalAppendFails(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config(""), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	os.MkdirAll(filepath.Join(g.Defaults.StateDir, "backup.jsonl"), 0o700)
	res := g.RunRepo(context.Background(), "r")
	if res.OK || !strings.Contains(res.Error, "backup.jsonl") {
		t.Fatalf("run with an unwritable backup.jsonl: %+v", res)
	}
}

// ---------- Warnings ----------

func TestREQ_BG_083_084_Warnings(t *testing.T) {
	f := qaFake(t)
	notify, out := qaNotifier(t, "")
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, f.config("notify:\n  command: ["+notify+"]\n"), map[string]string{"r": "remote: " + bare + "\n"})
	g, _ := qaGuard(t, dir)
	ctx := context.Background()
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("run: %+v", res)
	}
	if s := qaRead(t, out); s != "" {
		t.Fatalf("warning for a successful run: %s", s) // REQ-BG-084
	}
	t.Setenv("QA_RUN_EXIT", "1")
	t.Setenv("QA_RUN_OUT", "error: boom")
	res := g.RunRepo(ctx, "r")
	s := qaRead(t, out)
	if strings.Count(s, `"kind"`) != 1 {
		t.Fatalf("%d warnings for one failed run:\n%s", strings.Count(s, `"kind"`), s)
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	if m["kind"] != "backup_failed" || m["guard"] != "backup" || m["repo"] != "r" || m["error"] != res.Error ||
		m["everref_exit"] != 1.0 || !strings.Contains(fmt.Sprint(m["output"]), "boom") {
		t.Fatalf("warning: %v", m)
	}
	if ts, _ := m["time"].(string); ts == "" {
		t.Fatalf("warning without time: %v", m)
	}
}

func TestREQ_BG_085_NoNotifyCommand(t *testing.T) {
	bare, _ := remoteWith(t, "main")
	for _, extra := range []string{"", "notify:\n  command: []\n", "notify: {}\n"} {
		f := qaFake(t)
		dir := writeConfig(t, f.config(extra), map[string]string{"r": "remote: " + bare + "\n"})
		g, _ := qaGuard(t, dir)
		t.Setenv("QA_RUN_EXIT", "1")
		res := g.RunRepo(context.Background(), "r")
		if res.OK || res.Notify != "" || res.EverrefExit != 1 {
			t.Errorf("defaults %q: %+v", extra, res)
		}
	}
}

func TestREQ_BG_086_NotifyFailureRecorded(t *testing.T) {
	bare, _ := remoteWith(t, "main")
	failing, _ := qaNotifier(t, "cat >/dev/null; echo 'mail server down' >&2; exit 3")
	for _, cmd := range []string{failing, filepath.Join(t.TempDir(), "no-such-notifier")} {
		f := qaFake(t)
		dir := writeConfig(t, f.config("notify:\n  command: ["+cmd+"]\n"), map[string]string{"r": "remote: " + bare + "\n"})
		g, _ := qaGuard(t, dir)
		t.Setenv("QA_RUN_EXIT", "1")
		res := g.RunRepo(context.Background(), "r")
		if res.OK || !strings.Contains(res.Notify, "notify.command") {
			t.Errorf("notify %s: %+v", cmd, res)
		}
		if cmd == failing && !strings.Contains(res.Notify, "mail server down") {
			t.Errorf("notify output not recorded: %q", res.Notify)
		}
		if j := readJournal(t, g.Defaults.StateDir); j[len(j)-1].Notify != res.Notify {
			t.Errorf("journal notify %q", j[len(j)-1].Notify)
		}
	}
}

// ---------- Looking, restoring, provisioning ----------

func TestREQ_BG_100_101_EverrefToolsWorkAndRemoteUntouched(t *testing.T) {
	everref := realEverref(t)
	bare, w := remoteWith(t, "main", "feature")
	w.Git("tag", "v1")
	w.Git("push", "-q", "origin", "v1")
	lsRemote := func() string { return testutil.Run(t, bare, "for-each-ref", "--format=%(refname) %(objectname)") }
	before := lsRemote()
	dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n"})
	g, repos := qaGuard(t, dir)
	if err := g.RunAll(context.Background(), repos); err != nil {
		t.Fatal(err)
	}
	if after := lsRemote(); after != before {
		t.Fatalf("the remote changed:\nbefore\n%s\nafter\n%s", before, after) // REQ-BG-101
	}
	bridge := BridgePath(g.Defaults.StateDir, "r")
	for _, args := range [][]string{{"status", "--all"}, {"log", "origin/main"}, {"restore"}} {
		out, err := exec.Command(everref, append([]string{"-C", bridge}, args...)...).CombinedOutput()
		if err != nil {
			t.Errorf("everref %v in the bridge: %v\n%s", args, err, out)
		}
	}
	if after := lsRemote(); after != before {
		t.Fatalf("restore in the bridge changed the remote") // REQ-BG-101
	}
}

func TestREQ_BG_102_NoRuntimeDownloads(t *testing.T) {
	bad := regexp.MustCompile(`https?://|go (install|get|build)|"curl"|"wget"|net/http|install-everref\.sh"`)
	for _, d := range []string{".", filepath.Join("..", "..", "cmd", "backup-guard")} {
		files, _ := filepath.Glob(filepath.Join(d, "*.go"))
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			for i, l := range strings.Split(qaRead(t, file), "\n") {
				if strings.HasPrefix(strings.TrimSpace(l), "//") {
					continue
				}
				if bad.MatchString(l) {
					t.Errorf("%s:%d may download or build at runtime: %s", file, i+1, strings.TrimSpace(l))
				}
			}
		}
	}
}

// qaInstall runs scripts/install-everref.sh with a stand-in curl that serves a
// tarball holding a git-everref printing version, and optionally a sha256sum
// that accepts anything.
func qaInstall(t *testing.T, version string, acceptAnySHA bool) (prefix, out string, err error) {
	t.Helper()
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "git-everref"), []byte("#!/bin/sh\necho '"+version+"'\n"), 0o755)
	tarball := filepath.Join(work, "release.tar.gz")
	if o, err := exec.Command("tar", "-czf", tarball, "-C", work, "git-everref").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, o)
	}
	bin := qaFakeProgram(t, "curl", `while [ $# -gt 0 ]; do [ "$1" = -o ] && { cp "`+tarball+`" "$2"; exit 0; }; shift; done; exit 1`)
	if acceptAnySHA {
		os.WriteFile(filepath.Join(bin, "sha256sum"), []byte("#!/bin/sh\ncat >/dev/null\nexit 0\n"), 0o755)
	}
	prefix = filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", filepath.Join("..", "..", "scripts", "install-everref.sh"), prefix)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	o, err := cmd.CombinedOutput()
	return prefix, string(o), err
}

func TestREQ_BG_103_104_InstallScriptChecks(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skip("sha256sum not available")
	}
	// REQ-BG-103: a tarball that doesn't match the pinned SHA-256 installs nothing.
	prefix, out, err := qaInstall(t, "everref version v1.0.0", false)
	if err == nil || !strings.Contains(out, "SHA-256 mismatch") {
		t.Errorf("tampered tarball: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(prefix, "git-everref")); err == nil {
		t.Error("tampered tarball was installed")
	}
	// REQ-BG-104: a binary with the wrong version installs nothing.
	for _, v := range []string{"everref version v1.0.1", "everref version v1.0.0-dirty", "hello"} {
		prefix, out, err = qaInstall(t, v, true)
		if err == nil || !strings.Contains(out, "unexpected version output") {
			t.Errorf("version %q: %v %s", v, err, out)
		}
		if _, err := os.Stat(filepath.Join(prefix, "git-everref")); err == nil {
			t.Errorf("binary with version %q was installed", v)
		}
	}
	// control: the right version with a matching checksum is installed
	prefix, out, err = qaInstall(t, "everref version v1.0.0", true)
	if err != nil {
		t.Fatalf("matching install: %v %s", err, out)
	}
	if st, err := os.Stat(filepath.Join(prefix, "git-everref")); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("installed binary: %v %v", st, err)
	}
}
