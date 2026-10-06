package backupguard

// gomutants survivor and coverage tests for internal/backupguard.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestREQ_BG_097_CheckConfigReport(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	cred := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(cred, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	// reachable local remote, credential readable by others, no notify.command
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n  version: 1.0.0\n", map[string]string{
		"a": "remote: " + bare + "\n",
		"b": "remote: ssh://git@example.invalid/b.git\ncredential: " + cred + "\n",
	})
	var out strings.Builder
	if err := CheckConfig(ctx, dir, false, &out); err != nil {
		t.Fatalf("check-config: %v\n%s", err, &out)
	}
	d, err := LoadDefaults(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"everref: " + bin + " (",
		"note: notify.command is not set; nobody is warned about failed runs\n",
		"note: repos/b: credential " + cred + " is readable by group or others\n",
		"repo a: " + bare + " -> " + BackupPath(d.StateDir, "a") + "\n",
		"configuration ok\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
	if strings.Contains(out.String(), "remote reachable") {
		t.Errorf("remote checked without --remote:\n%s", &out)
	}
	// with --remote: the local remote is reachable, the ssh one is a problem
	out.Reset()
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: 1.0.0\n", map[string]string{"a": "remote: " + bare + "\n"})
	if err := CheckConfig(ctx, dir, true, &out); err != nil || !strings.Contains(out.String(), "repo a: remote reachable\n") {
		t.Fatalf("check-config --remote: %v\n%s", err, &out)
	}
	out.Reset()
	gone := filepath.Join(t.TempDir(), "gone.git")
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: 1.0.0\n", map[string]string{"a": "remote: " + gone + "\n"})
	err = CheckConfig(ctx, dir, true, &out)
	if err == nil || err.Error() != "1 problem(s) in the configuration" || !strings.Contains(out.String(), "problem: repos/a: remote: ") {
		t.Fatalf("unreachable remote: %v\n%s", err, &out)
	}
}

func TestREQ_BG_097_CheckConfigProblems(t *testing.T) {
	bin, _ := fakeEverref(t)
	cred := filepath.Join(t.TempDir(), "missing-key")
	hosts := filepath.Join(t.TempDir(), "missing-hosts")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n  version: 1.0.0\nnotify:\n  command: [/nonexistent/notify-me]\n", map[string]string{
		"b": "remote: ssh://git@example.invalid/b.git\ncredential: " + cred + "\nknown_hosts: " + hosts + "\n",
	})
	var out strings.Builder
	err := CheckConfig(context.Background(), dir, false, &out)
	if err == nil || err.Error() != "3 problem(s) in the configuration" {
		t.Fatalf("err = %v\n%s", err, &out)
	}
	for _, want := range []string{"problem: notify.command: ", "problem: repos/b: credential: ", "problem: repos/b: known_hosts: "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, &out)
		}
	}
	if strings.Contains(out.String(), "configuration ok") || strings.Contains(out.String(), "notify.command is not set") {
		t.Errorf("output:\n%s", &out)
	}
}

func TestMutantsTruncateAndRunErrorUnwrap(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 3, "abc"},
		{"abcd", 3, "abc…"},
		{"aé", 2, "a…"},
		{"éé", 1, "…"},
	} {
		if got := truncate(c.s, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
	ev := &Everref{Bin: "/bin/false"}
	_, err := ev.Run(context.Background(), t.TempDir(), "x")
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Errorf("RunError does not unwrap to the exec error: %v", err)
	}
}

func TestMutantsCreateOnceErrorPaths(t *testing.T) {
	base := t.TempDir()
	// a build error removes the temporary directory and is returned
	boom := errors.New("boom")
	if err := createOnce(base, filepath.Join(base, "r"), "HEAD", func(string) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("build error: %v", err)
	}
	// a failed rename (the target appeared meanwhile) removes the temporary directory
	target := filepath.Join(base, "r")
	err := createOnce(base, target, "HEAD", func(tmp string) error {
		if err := os.MkdirAll(filepath.Join(target, "sub"), 0o700); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		t.Error("rename onto a non-empty directory succeeded")
	}
	os.RemoveAll(target)
	if left, _ := filepath.Glob(filepath.Join(base, setupPrefix+"*")); len(left) != 0 {
		t.Errorf("setup leftovers: %v", left)
	}
	// a path below a regular file cannot be checked
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := createOnce(base, filepath.Join(file, "r"), "HEAD", func(string) error { return nil }); err == nil {
		t.Error("dir below a file accepted")
	}
	// a read-only base: neither the empty leftover dir nor a temp dir can be made
	ro := t.TempDir()
	if err := os.Mkdir(filepath.Join(ro, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o700)
	called := false
	build := func(string) error { called = true; return nil }
	if err := createOnce(ro, filepath.Join(ro, "empty"), "HEAD", build); !errors.Is(err, os.ErrPermission) {
		t.Errorf("empty dir in read-only base: %v", err)
	}
	if err := createOnce(ro, filepath.Join(ro, "new"), "HEAD", build); !errors.Is(err, os.ErrPermission) {
		t.Errorf("temp dir in read-only base: %v", err)
	}
	if called {
		t.Error("build ran although setup failed")
	}
}

func TestMutantsSetupLeftoverThatCannotBeRemoved(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, setupPrefix+"bridge-1", "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(sub, 0o700)
	if err := removeSetupLeftovers(base); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v", err)
	}
}

func TestMutantsLockAndJournalErrors(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "locks"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(state, "r"); err == nil || errors.Is(err, ErrLocked) {
		t.Errorf("locks is a file: %v", err)
	}
	state = t.TempDir()
	if err := os.MkdirAll(lockPath(state, "r"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(state, "r"); err == nil || errors.Is(err, ErrLocked) {
		t.Errorf("lock path is a directory: %v", err)
	}
	if _, err := os.Stat("/dev/full"); err == nil {
		state = t.TempDir()
		if err := os.Symlink("/dev/full", filepath.Join(state, "backup.jsonl")); err != nil {
			t.Fatal(err)
		}
		g := &Guard{Defaults: &Defaults{StateDir: state}}
		if err := g.appendJournal(&Result{}); err == nil {
			t.Error("write error on backup.jsonl swallowed")
		}
	}
}

func TestMutantsLoadErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, DefaultsFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefaults(dir); err == nil {
		t.Error("defaults.yaml as a directory accepted")
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "repos"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if names, err := Repos(dir); err == nil {
		t.Errorf("repos as a file: %v", names)
	}
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "repos", "r", RepoFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepo(dir, "r"); err == nil || errors.Is(err, ErrUnknownRepo) {
		t.Errorf("backup.yaml as a directory: %v", err)
	}
}

func TestREQ_BG_015_PreflightRunOutcomes(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	g, repos, err := PreflightRun(ctx, dir, nil)
	if err != nil || g == nil || len(repos) != 1 || repos[0] != "r" {
		t.Fatalf("PreflightRun: %v %v %v", g, repos, err)
	}
	// a failed preflight whose warning cannot be delivered records that too
	notify, _ := qaNotifier(t, "exit 3")
	dir = writeConfig(t, "everref:\n  path: "+missingEverref(t)+"\nnotify:\n  command: ["+notify+"]\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if _, _, err := PreflightRun(ctx, dir, nil); err == nil {
		t.Fatal("preflight without everref passed")
	}
	d, _ := LoadDefaults(dir)
	if j := readJournal(t, d.StateDir); len(j) != 1 || !strings.Contains(j[0].Notify, "notify.command") {
		t.Fatalf("journal: %+v", j)
	}
	// repos/ that cannot be read is a preflight problem
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n", nil)
	if err := os.WriteFile(filepath.Join(dir, "repos"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Preflight(ctx, dir, nil); err == nil || strings.Contains(err.Error(), "no repos configured") {
		t.Fatalf("unreadable repos/: %v", err)
	}
}

func TestMutantsRunRepoSetupFailures(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{
		"r": "remote: " + bare + "\n",
	})
	g, _ := qaGuard(t, dir)
	if res := g.RunRepo(ctx, "nope"); res.OK || !strings.Contains(res.Error, "nope") {
		t.Errorf("unknown repo: %+v", res)
	}
	// a healthy run, then break the bridge in ways the next run must handle
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("first run: %+v", res)
	}
	bridge := BridgePath(g.Defaults.StateDir, "r")
	if _, err := qaGitIn(t, bridge, "remote", "remove", "backup"); err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("run after the backup remote was removed: %+v", res)
	}
	if got, _ := qaGitIn(t, bridge, "remote", "get-url", "backup"); strings.TrimSpace(got) != BackupPath(g.Defaults.StateDir, "r") {
		t.Errorf("backup remote not restored: %q", got)
	}
	if _, err := qaGitIn(t, bridge, "remote", "set-url", "backup", "/elsewhere.git"); err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); res.OK || !strings.Contains(res.Error, "refusing to continue") {
		t.Errorf("wrong backup remote: %+v", res)
	}
	if _, err := qaGitIn(t, bridge, "remote", "remove", "origin"); err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); res.OK {
		t.Errorf("bridge without origin: %+v", res)
	}
	// a lock that cannot be taken (locks/ is a file) fails the repo
	locks := filepath.Join(g.Defaults.StateDir, "locks")
	if err := os.RemoveAll(locks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(locks, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if res := g.RunRepo(ctx, "r"); res.OK || res.Skipped {
		t.Errorf("lock failure: %+v", res)
	}
}
