package main

// Requirement tests for the backup-guard command (docs/backup-guard.md,
// REQ-BG-*). The mapping is in docs/backup-guard-testspec.md.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/testutil"
)

// qaEverref is a minimal git-everref stand-in: it answers --version, records
// protections and bridged tags like everref, and fails run --all for any
// bridge whose path contains "failing".
const qaEverref = `#!/bin/sh
dir=$2
case " $* " in *" --version "*) echo "everref version v1.0.0"; exit 0;; esac
case " $* " in *" tags "*) git -C "$dir" config --add everref-remote.backup.tags origin; exit $?;; esac
case " $* " in *" add "*) for a in "$@"; do case "$a" in origin/*) git -C "$dir" config "everref.remotes/$a.remote" backup;; esac; done; exit 0;; esac
case " $* " in *" run --all "*) case "$dir" in *failing*) echo "error: fetch failed"; exit 1;; esac; exit 0;; esac
exit 0
`

// qaConfig makes a configuration directory with the stand-in everref and one
// local bare remote per repo name.
func qaConfig(t *testing.T, defaults string, repos ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", "git-everref")
	files := map[string]string{"bin/git-everref": qaEverref, "defaults.yaml": "everref:\n  path: bin/git-everref\n" + defaults}
	for _, name := range repos {
		bare := testutil.Init(t, true)
		w := testutil.Init(t, false)
		w.Commit("one", map[string]string{"a.txt": "1\n"})
		w.Git("push", "-q", bare.Dir, "main")
		files["repos/"+name+"/backup.yaml"] = "remote: " + bare.Dir + "\n"
	}
	testutil.WriteFiles(t, dir, files)
	os.Chmod(bin, 0o755)
	return dir
}

func qaJournalRepos(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "state", "backup.jsonl"))
	if err != nil {
		return ""
	}
	var repos []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		_, rest, _ := strings.Cut(l, `"repo":"`)
		name, _, _ := strings.Cut(rest, `"`)
		repos = append(repos, name)
	}
	return strings.Join(repos, ",")
}

func TestREQ_BG_067_068_088_RunSelection(t *testing.T) {
	dir := qaConfig(t, "", "a", "b", "c")
	if code, out := pg("run", "--config", dir, "b"); code != 0 || !strings.Contains(out, "repo b: ok") || strings.Contains(out, "repo a") {
		t.Fatalf("run b: exit %d: %s", code, out)
	}
	if got := qaJournalRepos(t, dir); got != "b" {
		t.Fatalf("journal after run b: %s", got)
	}
	// REQ-BG-068: an unknown name stops the command before any repo runs
	if code, out := pg("run", "--config", dir, "a", "nope"); code != 1 || !strings.Contains(out, `unknown repo "nope"`) {
		t.Fatalf("run a nope: exit %d: %s", code, out)
	}
	if got := qaJournalRepos(t, dir); got != "b" {
		t.Fatalf("a repo ran despite the unknown name: %s", got)
	}
	// REQ-BG-088: flags before, between and after repo names
	if code, out := pg("run", "a", "--config", dir, "c"); code != 0 {
		t.Fatalf("run a --config dir c: exit %d: %s", code, out)
	}
	if got := qaJournalRepos(t, dir); got != "b,a,c" {
		t.Fatalf("journal after run a c: %s", got)
	}
	// REQ-BG-067: no names: every repo, in order
	if code, out := pg("run", "--config", dir); code != 0 {
		t.Fatalf("run: exit %d: %s", code, out)
	}
	if got := qaJournalRepos(t, dir); got != "b,a,c,a,b,c" {
		t.Fatalf("journal after run: %s", got)
	}
}

func TestREQ_BG_087_Version(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		if code, out := pg(arg); code != 0 || out != "backup-guard "+version+"\n" {
			t.Errorf("%s: exit %d: %q", arg, code, out)
		}
	}
	for _, arg := range []string{"help", "--help", "-h"} {
		if code, out := pg(arg); code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("%s: exit %d: %q", arg, code, out)
		}
	}
}

func TestREQ_BG_089_090_ExitCodes(t *testing.T) {
	dir := qaConfig(t, "", "ok", "failing")
	if code, out := pg("run", "--config", dir, "ok"); code != 0 {
		t.Errorf("successful run: exit %d: %s", code, out)
	}
	if code, out := pg("run", "--config", dir); code != 1 || !strings.Contains(out, "repo ok: ok") || !strings.Contains(out, "backup failed for failing") {
		t.Errorf("run with a failing repo: exit %d: %s", code, out)
	}
	if code, out := pg("check-config", "--config", dir); code != 0 || !strings.Contains(out, "configuration ok") {
		t.Errorf("check-config ok: exit %d: %s", code, out)
	}
	testutil.WriteFiles(t, dir, map[string]string{"repos/bad/backup.yaml": "remote: relative.git\n"})
	if code, _ := pg("check-config", "--config", dir); code != 1 {
		t.Errorf("check-config with a problem: exit %d", code)
	}
	empty := t.TempDir()
	if code, _ := pg("run", "--config", empty); code != 1 {
		t.Errorf("failed preflight: exit %d", code)
	}
}

func TestREQ_BG_091_UsageErrors(t *testing.T) {
	dir := qaConfig(t, "", "a")
	for _, args := range [][]string{
		{}, {"frobnicate"}, {"run"}, {"check-config"}, {"run", "a"},
		{"run", "--config", dir, "--frob"}, {"check-config", "--config", dir, "--remot"},
		{"check-config", "--config", dir, "a"}, {"run", "--config"},
	} {
		if code, out := pg(args...); code != 2 {
			t.Errorf("%v: exit %d: %s", args, code, out)
		}
	}
	if got := qaJournalRepos(t, dir); got != "" {
		t.Errorf("a usage error ran repos: %s", got)
	}
}

func TestREQ_BG_092_093_CheckConfigPreflightNoState(t *testing.T) {
	dir := qaConfig(t, "", "a")
	if code, out := pg("check-config", "--config", dir); code != 0 {
		t.Fatalf("check-config: exit %d: %s", code, out)
	}
	if code, out := pg("check-config", "--config", dir, "--remote"); code != 0 || !strings.Contains(out, "repo a: remote reachable") {
		t.Fatalf("check-config --remote: exit %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); err == nil {
		t.Fatal("check-config created state") // REQ-BG-093
	}
	testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": "everref:\n  path: bin/missing\n"})
	if code, out := pg("check-config", "--config", dir); code != 1 || !strings.Contains(out, "everref not found") {
		t.Fatalf("check-config without everref: exit %d: %s", code, out)
	}
}

func TestREQ_BG_094_CheckConfigReportsEveryBrokenBackupYaml(t *testing.T) {
	// Since 2026-10-06 a backup.yaml that doesn't load fails the preflight
	// (REQ-BG-037), which reports every problem at once (REQ-BG-108).
	dir := qaConfig(t, "", "good")
	testutil.WriteFiles(t, dir, map[string]string{
		"repos/broken/backup.yaml": "remote: /srv/r.git\nrules: {}\n",
		"repos/worse/backup.yaml":  "remote: [\n",
	})
	code, out := pg("check-config", "--config", dir)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"repos/broken/backup.yaml", "repos/worse/backup.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "configuration ok") {
		t.Errorf("broken backup.yaml and configuration ok:\n%s", out)
	}
}

func TestREQ_BG_095_096_099_CheckConfigProblems(t *testing.T) {
	dir := qaConfig(t, "notify:\n  command: [no-such-notifier-qa]\n", "good")
	testutil.WriteFiles(t, dir, map[string]string{
		"repos/ssh/backup.yaml": "remote: git@example.invalid:o/r.git\ncredential: keys/missing\nknown_hosts: kh/missing\n",
	})
	code, out := pg("check-config", "--config", dir)
	if code != 1 || !strings.Contains(out, "3 problem(s)") {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{
		"problem: notify.command",         // REQ-BG-096
		"problem: repos/ssh: credential",  // REQ-BG-095
		"problem: repos/ssh: known_hosts", // REQ-BG-095
		"repo good: ",                     // the good repo is still listed
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "configuration ok") {
		t.Errorf("problems and configuration ok:\n%s", out)
	}
}

func TestREQ_BG_097_CheckConfigNotes(t *testing.T) {
	dir := qaConfig(t, "")
	testutil.WriteFiles(t, dir, map[string]string{
		"repos/ssh/backup.yaml": "remote: git@example.invalid:o/r.git\ncredential: key\nknown_hosts: kh\n",
		"key":                   "not really a key\n",
		"kh":                    "example.invalid ssh-ed25519 AAAA\n",
	})
	os.Chmod(filepath.Join(dir, "key"), 0o644)
	code, out := pg("check-config", "--config", dir)
	if code != 0 || !strings.Contains(out, "configuration ok") {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"note: everref.version is not set", "note: notify.command is not set", "note: repos/ssh: credential " + filepath.Join(dir, "key") + " is readable by group or others"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestREQ_BG_098_CheckConfigRemote(t *testing.T) {
	dir := qaConfig(t, "timeout: 30s\n", "good")
	testutil.WriteFiles(t, dir, map[string]string{"repos/gone/backup.yaml": "remote: " + filepath.Join(t.TempDir(), "gone.git") + "\n"})
	if code, out := pg("check-config", "--config", dir); code != 0 {
		t.Fatalf("without --remote the missing remote is no problem: exit %d: %s", code, out)
	}
	code, out := pg("check-config", "--config", dir, "--remote")
	if code != 1 || !strings.Contains(out, "problem: repos/gone: remote:") || !strings.Contains(out, "repo good: remote reachable") {
		t.Fatalf("exit %d: %s", code, out)
	}
}
