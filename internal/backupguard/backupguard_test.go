package backupguard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
)

func TestMain(m *testing.M) {
	cleanup, err := testutil.IsolateGit()
	if err != nil {
		panic(err)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// fakeEverref writes a stand-in for git-everref that logs its arguments.
// FAKE_EVERREF_VERSION, FAKE_EVERREF_RUN_EXIT and FAKE_EVERREF_RUN_OUT
// control it; any add naming origin/bad fails like a refused name.
func fakeEverref(t *testing.T) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "git-everref"), filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
if [ "$1" = -C ] && [ -z "$2" ]; then echo "fake everref: empty -C directory" >&2; exit 9; fi
echo "$*" >> "` + log + `"
case " $* " in *" --version "*) echo "everref version ${FAKE_EVERREF_VERSION:-v1.0.0}"; exit 0;; esac
case " $* " in *" origin/bad "*) echo "error: cannot protect origin/bad: reserved name" >&2; exit 2;; esac
case " $* " in *" run --all "*) printf '%s\n' "${FAKE_EVERREF_RUN_OUT:-}"; exit ${FAKE_EVERREF_RUN_EXIT:-0};; esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func writeConfig(t *testing.T, defaults string, repos map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{}
	if defaults != "" {
		files[DefaultsFile] = defaults
	}
	for name, content := range repos {
		files[filepath.Join("repos", name, RepoFile)] = content
	}
	testutil.WriteFiles(t, dir, files)
	return dir
}

func TestLoadDefaults(t *testing.T) {
	d, err := LoadDefaults(writeConfig(t, "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if d.Everref != DefaultEverref || d.Timeout != DefaultTimeout || d.EverrefVersion != DefaultEverrefVersion || filepath.Base(d.StateDir) != "state" {
		t.Fatalf("built-in defaults: %+v", d)
	}
	dir := writeConfig(t, "everref:\n  path: bin/git-everref\n  version: v1.0.0\nnotify:\n  command: [notify-me, --backup]\nstate_dir: /var/lib/backup\ntimeout: 5m\n", nil)
	d, err = LoadDefaults(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Everref != filepath.Join(dir, "bin/git-everref") || d.EverrefVersion != "v1.0.0" || d.StateDir != "/var/lib/backup" ||
		d.Timeout != 5*time.Minute || strings.Join(d.NotifyCommand, " ") != "notify-me --backup" {
		t.Fatalf("defaults: %+v", d)
	}
	for _, bad := range []string{
		"everref:\n  pth: x\n",   // unknown key
		"gitleaks:\n  path: x\n", // a Push Guard key
		"timeout: soon\n",
		"timeout: -1s\n",
		"everref:\n  path: \"\"\n",
	} {
		if _, err := LoadDefaults(writeConfig(t, bad, nil)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := LoadDefaults(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("accepted a missing configuration directory")
	}
}

func TestLoadRepo(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{
		"ok":        "remote: git@example.com:o/r.git\ncredential: deploy-key\nknown_hosts: /etc/kh\nexclude_branches: ['dependabot/.*', 'tmp']\n",
		"local":     "remote: /srv/git/r.git\n",
		"nocred":    "remote: git@example.com:o/r.git\n",
		"https":     "remote: https://example.com/o/r.git\n",
		"httpsuser": "remote: https://example.com/o/r.git\ncredential: t\ncredential_username: gitlab+deploy-token-1\n",
		"sshuser":   "remote: git@example.com:o/r.git\ncredential: k\ncredential_username: u\n",
		"khttps":    "remote: https://example.com/o/r.git\ncredential: t\nknown_hosts: kh\n",
		"relative":  "remote: ../r.git\n",
		"localcred": "remote: /srv/git/r.git\ncredential: k\n",
		"unknown":   "remote: /srv/git/r.git\nrules: {}\n",
		"noremote":  "exclude_branches: [x]\n",
		"badre":     "remote: /srv/git/r.git\nexclude_branches: ['(']\n",
	})
	r, err := LoadRepo(dir, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if r.Credential != filepath.Join(dir, "deploy-key") || r.KnownHosts != "/etc/kh" {
		t.Fatalf("paths: %+v", r)
	}
	for b, want := range map[string]bool{"dependabot/npm/x": true, "tmp": true, "tmp2": false, "main": false, "x/tmp": false} {
		if r.Excluded(b) != want {
			t.Errorf("Excluded(%q) = %v", b, !want)
		}
	}
	if _, err := LoadRepo(dir, "local"); err != nil {
		t.Fatal(err)
	}
	if r, err := LoadRepo(dir, "httpsuser"); err != nil || r.CredentialUsername != "gitlab+deploy-token-1" || r.Credential != filepath.Join(dir, "t") {
		t.Fatalf("httpsuser: %+v, %v", r, err)
	}
	for _, name := range []string{"nocred", "https", "khttps", "relative", "localcred", "unknown", "noremote", "badre", "sshuser"} {
		if _, err := LoadRepo(dir, name); err == nil {
			t.Errorf("repo %s accepted", name)
		}
	}
	if _, err := LoadRepo(dir, "absent"); !errors.Is(err, ErrUnknownRepo) {
		t.Errorf("absent repo: %v", err)
	}
	if _, err := LoadRepo(dir, "../ok"); err == nil {
		t.Error("accepted an invalid repo name")
	}
	repos, err := Repos(dir)
	if err != nil || len(repos) != 12 || repos[0] != "badre" {
		t.Fatalf("Repos = %v, %v", repos, err)
	}
}

func TestParseVersion(t *testing.T) {
	for out, want := range map[string]string{
		"everref version v1.0.0\n": "v1.0.0",
		"everref version dev":      "dev",
		"hello":                    "",
		"":                         "",
	} {
		if got := parseVersion(out); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestPreflightFailsClosed(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	repo := map[string]string{"r": "remote: /srv/git/r.git\n"}

	// everref missing
	dir := writeConfig(t, "everref:\n  path: "+filepath.Join(t.TempDir(), "git-everref")+"\n", repo)
	if _, _, err := Preflight(ctx, dir, nil); err == nil || !strings.Contains(err.Error(), "everref not found") {
		t.Fatalf("missing everref: %v", err)
	}
	// not on PATH
	t.Setenv("PATH", t.TempDir()+string(os.PathListSeparator)+filepath.Dir(mustLook(t, "git")))
	dir = writeConfig(t, "", repo)
	if _, _, err := Preflight(ctx, dir, nil); err == nil || !strings.Contains(err.Error(), "install-everref.sh") {
		t.Fatalf("everref not on PATH: %v", err)
	}
	// wrong version
	t.Setenv("FAKE_EVERREF_VERSION", "v0.9.0")
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: v1.0.0\n", repo)
	if _, _, err := Preflight(ctx, dir, nil); err == nil || !strings.Contains(err.Error(), "requires major version 1") {
		t.Fatalf("wrong version: %v", err)
	}
	// no repos
	t.Setenv("FAKE_EVERREF_VERSION", "v1.0.0")
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: v1.0.0\n", nil)
	if _, _, err := Preflight(ctx, dir, nil); err == nil || !strings.Contains(err.Error(), "no repos configured") {
		t.Fatalf("no repos: %v", err)
	}
	// all good
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: v1.0.0\n", repo)
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil || g.Version != "v1.0.0" || len(repos) != 1 {
		t.Fatalf("preflight: %v %v", repos, err)
	}
}

func mustLook(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCountEvents(t *testing.T) {
	out := `[origin/main]  created_1791023499             [new lineage] 1a2b3c4 -> backup
[origin/feat]  deleted_1791023498             [deleted] tip 5d6e7f8 -> backup
[origin/newb]  created_1791023499             [new] 9a8b7c6 -> backup
[origin/dev]   created_1791023000             [fast-forward] 1111111..2222222 -> backup
[origin/old]   created_1791023000             [regressed] 2222222..1111111 (journal only) -> backup
[tag v1]       created_1791023499             [re-tagged] 3333333 -> backup
[origin/gone]  deleted_1791020000             [deleted; already recorded] -> backup
[tags origin]  4 tag(s) up to date -> backup`
	var r Result
	countEvents(out, &r)
	if r.Rewritten != 1 || r.Deleted != 1 || r.New != 1 || r.Regressed != 1 || r.Retagged != 1 {
		t.Fatalf("counts: %+v", r)
	}
}

// remoteWith creates a bare remote with the given branches on one commit.
func remoteWith(t *testing.T, branches ...string) (bare string, work *testutil.Repo) {
	t.Helper()
	b := testutil.Init(t, true)
	w := testutil.Init(t, false)
	w.Commit("one", map[string]string{"a.txt": "1\n"})
	w.Git("remote", "add", "origin", b.Dir)
	for _, br := range branches {
		if br != "main" {
			w.Git("branch", br)
		}
		w.Git("push", "-q", "origin", br)
	}
	return b.Dir, w
}

func readJournal(t *testing.T, stateDir string) []Result {
	t.Helper()
	f, err := os.Open(filepath.Join(stateDir, "backup.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Result
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Result
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestAddRetriesSinglyAndRunFailureNotifies(t *testing.T) {
	ctx := context.Background()
	bin, log := fakeEverref(t)
	bare, _ := remoteWith(t, "main", "bad", "dev")
	notified := filepath.Join(t.TempDir(), "warning.json")
	testutil.WriteFiles(t, filepath.Dir(notified), map[string]string{"notify.sh": "#!/bin/sh\ncat > " + notified + "\n"})
	notify := filepath.Join(filepath.Dir(notified), "notify.sh")
	os.Chmod(notify, 0o755)
	dir := writeConfig(t, "everref:\n  path: "+bin+"\nnotify:\n  command: ["+notify+"]\n",
		map[string]string{"r": "remote: " + bare + "\n"})

	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_EVERREF_RUN_EXIT", "1")
	t.Setenv("FAKE_EVERREF_RUN_OUT", "error: [origin/dev] skipped on backup: fetch failed")
	res := g.RunRepo(ctx, repos[0])
	if res.OK || res.EverrefExit != 1 || !strings.Contains(res.Output, "fetch failed") {
		t.Fatalf("result: %+v", res)
	}
	if strings.Join(res.Added, ",") != "dev,main" || res.AddFailed["bad"] == "" {
		t.Fatalf("add: %v %v", res.Added, res.AddFailed)
	}
	data, err := os.ReadFile(notified)
	if err != nil {
		t.Fatal(err)
	}
	var w Warning
	if err := json.Unmarshal(data, &w); err != nil || w.Kind != "backup_failed" || w.Repo != "r" || w.EverrefExit != 1 {
		t.Fatalf("warning: %s %v", data, err)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"tags --remote backup on --source origin", "add origin/bad origin/dev origin/main --remote backup", "add origin/dev --remote backup", "run --all"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("no everref call %q in\n%s", want, calls)
		}
	}
	if j := readJournal(t, g.Defaults.StateDir); len(j) != 1 || j[0].OK {
		t.Fatalf("journal: %+v", j)
	}

	// Next run: everref succeeds, but bad still can't be protected: still a failure.
	t.Setenv("FAKE_EVERREF_RUN_EXIT", "0")
	res = g.RunRepo(ctx, repos[0])
	if res.OK || !strings.Contains(res.Error, "could not be protected: bad") {
		t.Fatalf("second run: %+v", res)
	}
}

func TestBridgeRefusesChangedRemote(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	other, _ := remoteWith(t, "main")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"r": "remote: " + bare + "\n"})
	g, _, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	testutil.WriteFiles(t, dir, map[string]string{"repos/r/backup.yaml": "remote: " + other + "\n"})
	res := g.RunRepo(ctx, "r")
	if res.OK || !strings.Contains(res.Error, "refusing to continue this backup with another remote") {
		t.Fatalf("changed remote: %+v", res)
	}
}

func TestEmptyRemoteIsAFailure(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"r": "remote: " + bare + "\nexclude_branches: ['.*']\n"})
	g, _, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); res.OK || res.Excluded != 1 {
		t.Fatalf("empty: %+v", res)
	}
}

// realEverref returns git-everref from PATH; with EVERREF_REQUIRED=1 (CI) a
// missing everref fails the test instead of skipping it.
func realEverref(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath(DefaultEverref)
	if err != nil {
		if os.Getenv("EVERREF_REQUIRED") == "1" {
			t.Fatal("git-everref is required (EVERREF_REQUIRED=1) but not on PATH")
		}
		t.Skip("git-everref not on PATH")
	}
	return p
}

func backupRefs(t *testing.T, backup string) []string {
	t.Helper()
	out := testutil.Run(t, backup, "for-each-ref", "--format=%(refname)")
	var refs []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" && !strings.Contains(l, "/journal/") {
			refs = append(refs, l)
		}
	}
	return refs
}

func countPrefix(refs []string, prefix string) int {
	n := 0
	for _, r := range refs {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// TestEndToEndWithEverref runs the real git-everref against a local remote:
// force push, branch deletion and a moved tag must all leave the old state
// in the backup.
func TestEndToEndWithEverref(t *testing.T) {
	realEverref(t)
	ctx := context.Background()
	bare, w := remoteWith(t, "main", "feature", "dependabot/x")
	w.Git("tag", "v1")
	w.Git("push", "-q", "origin", "v1")
	oldMain := w.Head()
	dir := writeConfig(t, "everref:\n  version: v1.0.0\n", map[string]string{"r": "remote: " + bare + "\nexclude_branches: ['dependabot/.*']\n"})
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RunAll(ctx, repos); err != nil {
		t.Fatal(err)
	}
	backup := BackupPath(g.Defaults.StateDir, "r")
	refs := backupRefs(t, backup)
	if countPrefix(refs, "refs/heads/everref/remotes/origin/main/created_") != 1 ||
		countPrefix(refs, "refs/heads/everref/remotes/origin/feature/created_") != 1 ||
		countPrefix(refs, "refs/tags/everref/remotes/origin/tags/v1/created_") != 1 ||
		countPrefix(refs, "refs/heads/everref/remotes/origin/dependabot/") != 0 {
		t.Fatalf("first run refs:\n%s", strings.Join(refs, "\n"))
	}

	time.Sleep(1100 * time.Millisecond) // everref names events by the second
	w.Git("commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
	w.Git("push", "-q", "-f", "origin", "main")
	w.Git("push", "-q", "origin", ":feature")
	w.Git("tag", "-f", "v1", "HEAD")
	w.Git("push", "-q", "-f", "origin", "v1")
	w.Git("branch", "late")
	w.Git("push", "-q", "origin", "late")
	if err := g.RunAll(ctx, repos); err != nil {
		t.Fatal(err)
	}
	refs = backupRefs(t, backup)
	if countPrefix(refs, "refs/heads/everref/remotes/origin/main/created_") != 2 ||
		countPrefix(refs, "refs/heads/everref/remotes/origin/feature/deleted_") != 1 ||
		countPrefix(refs, "refs/tags/everref/remotes/origin/tags/v1/created_") != 2 ||
		countPrefix(refs, "refs/heads/everref/remotes/origin/late/created_") != 1 {
		t.Fatalf("second run refs:\n%s", strings.Join(refs, "\n"))
	}
	// the overwritten main is still in the backup
	found := false
	for _, r := range refs {
		if strings.HasPrefix(r, "refs/heads/everref/remotes/origin/main/created_") && testutil.Run(t, backup, "rev-parse", r) == oldMain {
			found = true
		}
	}
	if !found {
		t.Fatalf("old main %s not preserved", oldMain)
	}
	j := readJournal(t, g.Defaults.StateDir)
	if len(j) != 2 || !j[1].OK || j[1].Rewritten != 1 || j[1].Deleted != 1 || j[1].Retagged != 1 || j[1].Excluded != 1 ||
		strings.Join(j[1].Added, ",") != "late" {
		t.Fatalf("journal: %+v", j)
	}

	// An unreachable remote is a failure and writes nothing.
	time.Sleep(1100 * time.Millisecond)
	before := len(backupRefs(t, backup))
	if err := os.Rename(bare, bare+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := g.RunAll(ctx, repos); err == nil {
		t.Fatal("run against an unreachable remote succeeded")
	}
	if after := len(backupRefs(t, backup)); after != before {
		t.Fatalf("unreachable remote changed the backup: %d -> %d refs", before, after)
	}
}

func TestExamplesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "backup")
	d, err := LoadDefaults(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.EverrefVersion != "v1.0.0" {
		t.Errorf("example pins everref %q", d.EverrefVersion)
	}
	repos, err := Repos(dir)
	if err != nil || len(repos) == 0 {
		t.Fatalf("example repos: %v %v", repos, err)
	}
	for _, r := range repos {
		if _, err := LoadRepo(dir, r); err != nil {
			t.Error(err)
		}
	}
}

// The pinned version in the install script, the example and the docs must agree.
func TestInstallScriptPin(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-everref.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nVERSION=1.0.0\n") {
		t.Error("scripts/install-everref.sh does not pin VERSION=1.0.0 (update this test with the example)")
	}
}
