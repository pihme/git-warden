package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/testutil"
)

// TestREQ_BG_015_RunRecordsFailedPreflight: backup-guard run records a failed
// preflight in backup.jsonl and exits 1; check-config records nothing.
func TestREQ_BG_015_RunRecordsFailedPreflight(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":       "everref:\n  path: " + filepath.Join(dir, "missing", "git-everref") + "\n",
		"repos/r/backup.yaml": "remote: /srv/git/r.git\n",
	})
	if code, _ := pg("check-config", "--config", dir); code != 1 {
		t.Fatalf("check-config: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); err == nil {
		t.Fatal("check-config recorded the failed preflight")
	}
	code, out := pg("run", "--config", dir)
	if code != 1 || !strings.Contains(out, "preflight: everref not found") {
		t.Fatalf("run: exit %d: %s", code, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state", "backup.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 1 ||
		!strings.Contains(lines[0], `"preflight":true`) || !strings.Contains(lines[0], `"everref_exit":-1`) || strings.Contains(lines[0], `"repo"`) {
		t.Fatalf("backup.jsonl: %s", data)
	}
}

// TestREQ_BG_090_InvalidRepoConfigExitsOne: an invalid backup.yaml fails the
// preflight of run, so the command exits 1 and no repo is backed up.
func TestREQ_BG_090_InvalidRepoConfigExitsOne(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"repos/r/backup.yaml": "remote: relative/path.git\n",
	})
	if code, out := pg("run", "--config", dir); code != 1 || !strings.Contains(out, "preflight:") {
		t.Fatalf("run: exit %d: %s", code, out)
	}
}
