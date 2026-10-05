package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

func pg(args ...string) (int, string) {
	var out bytes.Buffer
	code := run(args, &out, &out)
	return code, out.String()
}

func TestUsageAndVersion(t *testing.T) {
	if code, _ := pg(); code != 2 {
		t.Errorf("no args: exit %d", code)
	}
	if code, _ := pg("frobnicate"); code != 2 {
		t.Errorf("unknown command: exit %d", code)
	}
	if code, out := pg("run"); code != 2 || !strings.Contains(out, "--config is required") {
		t.Errorf("run without --config: exit %d: %s", code, out)
	}
	if code, out := pg("version"); code != 0 || !strings.HasPrefix(out, "backup-guard ") {
		t.Errorf("version: exit %d: %s", code, out)
	}
}

func TestRunFailsClosedWithoutEverref(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":       "everref:\n  path: " + filepath.Join(dir, "missing", "git-everref") + "\n",
		"repos/r/backup.yaml": "remote: /srv/git/r.git\n",
	})
	code, out := pg("run", "--config", dir)
	if code != 1 || !strings.Contains(out, "preflight: everref not found") {
		t.Fatalf("exit %d: %s", code, out)
	}
	// Only the failure record (REQ-BG-015) is written: no repo is set up.
	for _, sub := range []string{"repos", "locks"} {
		if _, err := os.Stat(filepath.Join(dir, "state", sub)); err == nil {
			t.Fatalf("a failed preflight created state/%s", sub)
		}
	}
}

func TestRunAndCheckConfig(t *testing.T) {
	if _, err := exec.LookPath("git-everref"); err != nil {
		if os.Getenv("EVERREF_REQUIRED") == "1" {
			t.Fatal("git-everref is required (EVERREF_REQUIRED=1) but not on PATH")
		}
		t.Skip("git-everref not on PATH")
	}
	bare := testutil.Init(t, true)
	w := testutil.Init(t, false)
	w.Commit("one", map[string]string{"a.txt": "1\n"})
	w.Git("push", "-q", bare.Dir, "main")
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"repos/r/backup.yaml": "remote: " + bare.Dir + "\n"})
	if code, out := pg("check-config", "--config", dir, "--remote"); code != 0 || !strings.Contains(out, "configuration ok") {
		t.Fatalf("check-config: exit %d: %s", code, out)
	}
	if code, out := pg("run", "--config", dir, "nope"); code != 1 || !strings.Contains(out, "unknown repo") {
		t.Fatalf("unknown repo: exit %d: %s", code, out)
	}
	if code, out := pg("run", "--config", dir); code != 0 || !strings.Contains(out, "repo r: ok") {
		t.Fatalf("run: exit %d: %s", code, out)
	}
}
