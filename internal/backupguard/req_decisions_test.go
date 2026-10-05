package backupguard

// Requirement tests for the Backup Guard decisions of 2026-10-06
// (docs/backup-guard.md, "Requirements" and "Decided"): a failed preflight is
// recorded and warned, the preflight loads every backup.yaml, only the major
// version of git-everref is checked, a locked repo is skipped instead of
// waited for, notify.command paths are relative to the configuration
// directory, and a failed add carries everref's output.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
)

// notifyScript writes an executable at dir/rel that stores the warning it
// gets on stdin; it returns the file the warning lands in.
func notifyScript(t *testing.T, dir, rel string) string {
	t.Helper()
	warning := filepath.Join(t.TempDir(), "warning.json")
	testutil.WriteFiles(t, dir, map[string]string{rel: "#!/bin/sh\ncat > " + warning + "\n"})
	if err := os.Chmod(filepath.Join(dir, rel), 0o755); err != nil {
		t.Fatal(err)
	}
	return warning
}

func readWarning(t *testing.T, path string) Warning {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no warning: %v", err)
	}
	var w Warning
	if err := json.Unmarshal(data, &w); err != nil {
		t.Fatalf("warning %s: %v", data, err)
	}
	return w
}

func missingEverref(t *testing.T) string {
	return filepath.Join(t.TempDir(), "missing", "git-everref")
}

func stateEntries(t *testing.T, stateDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestREQ_BG_001_FailedPreflightSetsUpNoRepo(t *testing.T) {
	dir := writeConfig(t, "everref:\n  path: "+missingEverref(t)+"\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if _, _, err := PreflightRun(context.Background(), dir, nil); err == nil {
		t.Fatal("preflight passed without everref")
	}
	// Only the failure record exists: no lock, bridge or backup repository.
	if got := stateEntries(t, filepath.Join(dir, "state")); strings.Join(got, ",") != "backup.jsonl" {
		t.Fatalf("state after a failed preflight: %v", got)
	}
}

func TestREQ_BG_015_FailedPreflightIsRecordedAndWarned(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{"r": "remote: /srv/git/r.git\n"})
	warning := notifyScript(t, dir, "notify.sh")
	testutil.WriteFiles(t, dir, map[string]string{DefaultsFile: "everref:\n  path: " + missingEverref(t) + "\nnotify:\n  command: [" + filepath.Join(dir, "notify.sh") + "]\n"})
	_, _, err := PreflightRun(context.Background(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "everref not found") {
		t.Fatalf("preflight: %v", err)
	}
	j := readJournal(t, filepath.Join(dir, "state"))
	if len(j) != 1 || j[0].OK || !j[0].Preflight || j[0].Repo != "" || j[0].EverrefExit != -1 ||
		!strings.HasPrefix(j[0].Error, "preflight: ") || !strings.Contains(j[0].Error, "everref not found") {
		t.Fatalf("journal: %+v", j)
	}
	w := readWarning(t, warning)
	if w.Kind != "backup_failed" || w.Guard != "backup" || !w.Preflight || w.Repo != "" || w.EverrefExit != -1 || !strings.Contains(w.Error, "everref not found") {
		t.Fatalf("warning: %+v", w)
	}
}

func TestREQ_BG_105_UnloadableDefaultsRecordNothing(t *testing.T) {
	dir := writeConfig(t, "timeout: soon\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if _, _, err := PreflightRun(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); err == nil {
		t.Fatal("a defaults.yaml that doesn't load created state")
	}
}

func TestREQ_BG_106_UnwritableStateDirStillFails(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{"r": "remote: /srv/git/r.git\n"})
	blocker := filepath.Join(dir, "blocker")
	testutil.WriteFiles(t, dir, map[string]string{
		"blocker":    "a file, so state_dir below it can't be created\n",
		DefaultsFile: "everref:\n  path: " + missingEverref(t) + "\nstate_dir: " + filepath.Join(blocker, "state") + "\n",
	})
	_, _, err := PreflightRun(context.Background(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "everref not found") || !strings.Contains(err.Error(), "could not be recorded") {
		t.Fatalf("preflight with an unwritable state_dir: %v", err)
	}
}

func TestREQ_BG_037_InvalidBackupYamlFailsPreflight(t *testing.T) {
	bin, _ := fakeEverref(t)
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{
		"good": "remote: /srv/git/r.git\n",
		"bad":  "remote: /srv/git/r.git\nrules: {}\n",
	})
	_, _, err := PreflightRun(context.Background(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("repos", "bad", RepoFile)) {
		t.Fatalf("preflight with an invalid backup.yaml: %v", err)
	}
	if got := stateEntries(t, filepath.Join(dir, "state")); strings.Join(got, ",") != "backup.jsonl" {
		t.Fatalf("a repo ran despite the failed preflight: %v", got)
	}
}

func TestREQ_BG_107_InvalidRepoFolderNameFailsPreflight(t *testing.T) {
	bin, _ := fakeEverref(t)
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{
		"good": "remote: /srv/git/r.git\n",
		"Bad":  "remote: /srv/git/r.git\n",
	})
	if _, _, err := Preflight(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), `invalid repo name "Bad"`) {
		t.Fatalf("preflight with an invalid folder name: %v", err)
	}
}

func TestREQ_BG_108_PreflightReportsEveryProblem(t *testing.T) {
	dir := writeConfig(t, "everref:\n  path: "+missingEverref(t)+"\n", map[string]string{
		"Bad":   "remote: /srv/git/r.git\n",
		"nocfg": "remote: git@example.com:o/r.git\n",
	})
	_, _, err := Preflight(context.Background(), dir, nil)
	if err == nil {
		t.Fatal("preflight passed")
	}
	for _, want := range []string{"3 problems", "everref not found", `invalid repo name "Bad"`, "needs a credential"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error lacks %q: %v", want, err)
		}
	}
}

func TestREQ_BG_012_OnlyTheMajorVersionIsCompared(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	for _, c := range []struct {
		want, got string
		ok        bool
	}{
		{"", "v1.0.0", true},
		{"1.0.0", "v1.4.2", true},
		{"v1.0.0", "v1.9.0", true},
		{"1", "v1.0.1-rc1", true},
		{"2", "v2.1.0", true},
		{"1.0.0", "v2.0.0", false},
		{"2.0.0", "v1.0.0", false},
		{"1.0.0", "v0.9.0", false},
		{"1.0.0", "banana", false},
	} {
		t.Setenv("FAKE_EVERREF_VERSION", c.got)
		_, _, err := FindEverref(ctx, bin, c.want)
		if (err == nil) != c.ok {
			t.Errorf("everref.version %q, installed %q: err = %v", c.want, c.got, err)
		}
	}
	// Through the preflight, with everref.version from defaults.yaml.
	t.Setenv("FAKE_EVERREF_VERSION", "v1.3.0")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n  version: v1.0.0\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if g, _, err := Preflight(ctx, dir, nil); err != nil || g.Version != "v1.3.0" {
		t.Fatalf("v1.3.0 against v1.0.0: %v", err)
	}
}

func TestREQ_BG_013_EverrefVersionDefaultsTo100(t *testing.T) {
	d, err := LoadDefaults(writeConfig(t, "", nil))
	if err != nil || d.EverrefVersion != "1.0.0" {
		t.Fatalf("default everref.version: %+v %v", d, err)
	}
	bin, _ := fakeEverref(t)
	t.Setenv("FAKE_EVERREF_VERSION", "v2.0.0")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if _, _, err := Preflight(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "requires major version 1") {
		t.Fatalf("v2.0.0 without everref.version: %v", err)
	}
}

func TestREQ_BG_112_SourceBuildNeedsExplicitDev(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeEverref(t)
	t.Setenv("FAKE_EVERREF_VERSION", "dev")
	if _, _, err := FindEverref(ctx, bin, ""); err == nil || !strings.Contains(err.Error(), "source build") {
		t.Fatalf("dev with the default everref.version: %v", err)
	}
	if _, _, err := FindEverref(ctx, bin, "1.0.0"); err == nil {
		t.Fatal("dev accepted for everref.version 1.0.0")
	}
	if _, v, err := FindEverref(ctx, bin, "dev"); err != nil || v != "dev" {
		t.Fatalf("dev with everref.version dev: %q %v", v, err)
	}
	t.Setenv("FAKE_EVERREF_VERSION", "v1.0.0")
	if _, _, err := FindEverref(ctx, bin, "dev"); err == nil {
		t.Fatal("a release accepted for everref.version dev")
	}
}

func TestREQ_BG_113_EverrefVersionMustBeAVersionOrDev(t *testing.T) {
	for _, v := range []string{`""`, "latest", "1.0.0.0", "v1.x", "~1"} {
		if _, err := LoadDefaults(writeConfig(t, "everref:\n  version: "+v+"\n", nil)); err == nil {
			t.Errorf("everref.version %s accepted", v)
		}
	}
	for _, v := range []string{"1", "v2.3", "1.0.0", "v1.0.0", "dev"} {
		if d, err := LoadDefaults(writeConfig(t, "everref:\n  version: "+v+"\n", nil)); err != nil || d.EverrefVersion != v {
			t.Errorf("everref.version %s: %v", v, err)
		}
	}
}

func TestREQ_BG_075_LockedRepoIsSkippedWithoutWaiting(t *testing.T) {
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"r": "remote: " + bare + "\n"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(g.Defaults.StateDir, "r") // another run holds the lock
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := g.RunAll(ctx, repos); err == nil {
		t.Fatal("a locked repo counted as backed up")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the run waited %s for the lock", d)
	}
	j := readJournal(t, g.Defaults.StateDir)
	if len(j) != 1 || j[0].OK || !j[0].Skipped || !strings.Contains(j[0].Error, "another backup-guard run holds") {
		t.Fatalf("journal: %+v", j)
	}
	if _, err := os.Stat(BridgePath(g.Defaults.StateDir, "r")); err == nil {
		t.Fatal("a skipped repo was set up")
	}
	unlock()
	if res := g.RunRepo(ctx, "r"); !res.OK {
		t.Fatalf("after the lock was released: %+v", res)
	}
}

func TestREQ_BG_109_SkippedRepoIsRecordedAndWarned(t *testing.T) {
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "", map[string]string{"a": "remote: " + bare + "\n", "b": "remote: " + bare + "\n"})
	warning := notifyScript(t, dir, "notify.sh")
	testutil.WriteFiles(t, dir, map[string]string{DefaultsFile: "everref:\n  path: " + bin + "\nnotify:\n  command: [./notify.sh]\n"})
	ctx := context.Background()
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(g.Defaults.StateDir, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := g.RunAll(ctx, repos); err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("RunAll: %v", err)
	}
	j := readJournal(t, g.Defaults.StateDir)
	if len(j) != 2 || j[0].Repo != "a" || !j[0].Skipped || j[0].OK || j[1].Repo != "b" || !j[1].OK || j[1].Skipped {
		t.Fatalf("journal: %+v", j)
	}
	if w := readWarning(t, warning); w.Repo != "a" || !w.Skipped || w.Kind != "backup_failed" {
		t.Fatalf("warning: %+v", w)
	}
}

func TestREQ_BG_111_NotifyCommandRelativeToConfigDir(t *testing.T) {
	dir := writeConfig(t, "notify:\n  command: [bin/notify.sh, --backup]\n", nil)
	d, err := LoadDefaults(dir)
	if err != nil || strings.Join(d.NotifyCommand, " ") != filepath.Join(dir, "bin/notify.sh")+" --backup" {
		t.Fatalf("relative notify.command: %v %v", d.NotifyCommand, err)
	}
	if d, err := LoadDefaults(writeConfig(t, "notify:\n  command: [warden-notify]\n", nil)); err != nil || d.NotifyCommand[0] != "warden-notify" {
		t.Fatalf("bare notify.command: %v %v", d.NotifyCommand, err)
	}
	if _, err := LoadDefaults(writeConfig(t, "notify:\n  command: ['']\n", nil)); err == nil {
		t.Fatal("empty notify.command program accepted")
	}

	// The warning reaches a relative notify.command from any working directory.
	dir = writeConfig(t, "", map[string]string{"r": "remote: /srv/git/r.git\n"})
	warning := notifyScript(t, dir, "bin/notify.sh")
	testutil.WriteFiles(t, dir, map[string]string{DefaultsFile: "everref:\n  path: " + missingEverref(t) + "\nnotify:\n  command: [bin/notify.sh]\n"})
	t.Chdir(t.TempDir())
	if _, _, err := PreflightRun(context.Background(), dir, nil); err == nil {
		t.Fatal("preflight passed")
	}
	if w := readWarning(t, warning); !w.Preflight {
		t.Fatalf("warning: %+v", w)
	}
	if j := readJournal(t, filepath.Join(dir, "state")); len(j) != 1 || j[0].Notify != "" {
		t.Fatalf("journal: %+v", j)
	}
}

func TestREQ_BG_081_FailedAddCarriesEverrefOutput(t *testing.T) {
	bin, _ := fakeEverref(t)
	bare, _ := remoteWith(t, "main", "bad")
	dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n"})
	warning := notifyScript(t, dir, "notify.sh")
	testutil.WriteFiles(t, dir, map[string]string{DefaultsFile: "everref:\n  path: " + bin + "\nnotify:\n  command: [./notify.sh]\n"})
	ctx := context.Background()
	g, _, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := g.RunRepo(ctx, "r") // everref run succeeds; only origin/bad can't be protected
	if res.OK || res.EverrefExit != 0 || !strings.Contains(res.Output, "reserved name") {
		t.Fatalf("result: %+v", res)
	}
	if w := readWarning(t, warning); !strings.Contains(w.Output, "reserved name") {
		t.Fatalf("warning output: %+v", w)
	}
	if j := readJournal(t, g.Defaults.StateDir); !strings.Contains(j[0].Output, "reserved name") {
		t.Fatalf("journal output: %+v", j[0])
	}
}

func TestREQ_BG_097_CheckConfigNotesDefaultEverrefVersion(t *testing.T) {
	bin, _ := fakeEverref(t)
	var out strings.Builder
	dir := writeConfig(t, "everref:\n  path: "+bin+"\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if err := CheckConfig(context.Background(), dir, false, &out); err != nil {
		t.Fatalf("check-config: %v\n%s", err, &out)
	}
	if !strings.Contains(out.String(), "note: everref.version is not set; the default 1.0.0 applies") {
		t.Fatalf("no note on the default everref.version:\n%s", &out)
	}
	out.Reset()
	dir = writeConfig(t, "everref:\n  path: "+bin+"\n  version: 1.0.0\n", map[string]string{"r": "remote: /srv/git/r.git\n"})
	if err := CheckConfig(context.Background(), dir, false, &out); err != nil || strings.Contains(out.String(), "everref.version is not set") {
		t.Fatalf("check-config with everref.version set: %v\n%s", err, &out)
	}
}

func TestREQ_BG_110_CheckConfigRecordsNoPreflightFailure(t *testing.T) {
	dir := writeConfig(t, "", map[string]string{"r": "remote: /srv/git/r.git\n"})
	warning := notifyScript(t, dir, "notify.sh")
	testutil.WriteFiles(t, dir, map[string]string{DefaultsFile: "everref:\n  path: " + missingEverref(t) + "\nnotify:\n  command: [./notify.sh]\n"})
	if err := CheckConfig(context.Background(), dir, false, &strings.Builder{}); err == nil {
		t.Fatal("check-config passed without everref")
	}
	if _, err := os.Stat(filepath.Join(dir, "state")); err == nil {
		t.Error("check-config created state")
	}
	if _, err := os.Stat(warning); err == nil {
		t.Error("check-config sent a warning")
	}
}

// TestREQ_BG_051_ExcludedLaterStaysBackedUp: a branch matched by
// exclude_branches after it was protected keeps being recorded.
func TestREQ_BG_051_ExcludedLaterStaysBackedUp(t *testing.T) {
	realEverref(t)
	ctx := context.Background()
	bare, w := remoteWith(t, "main", "feature")
	dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n"})
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RunAll(ctx, repos); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir, map[string]string{"repos/r/backup.yaml": "remote: " + bare + "\nexclude_branches: ['feature']\n"})
	w.Git("checkout", "-q", "feature")
	head := w.Commit("later", map[string]string{"b.txt": "2\n"})
	w.Git("push", "-q", "origin", "feature")
	if err := g.RunAll(ctx, repos); err != nil {
		t.Fatal(err)
	}
	backup := BackupPath(g.Defaults.StateDir, "r")
	found := false
	for _, r := range backupRefs(t, backup) {
		if strings.HasPrefix(r, "refs/heads/everref/remotes/origin/feature/created_") && testutil.Run(t, backup, "rev-parse", r) == head {
			found = true
		}
	}
	if !found {
		t.Fatalf("the excluded feature branch's new tip %s is not in the backup:\n%s", head, strings.Join(backupRefs(t, backup), "\n"))
	}
	if j := readJournal(t, g.Defaults.StateDir); j[1].Excluded != 1 {
		t.Fatalf("journal: %+v", j[1])
	}
}

// TestREQ_BG_081_TailKeepsTheEnd: the output tail keeps the last bytes,
// cut at a line start (or at least a UTF-8 boundary), never the first ones.
func TestREQ_BG_081_TailKeepsTheEnd(t *testing.T) {
	if got := tail("short", 10); got != "short" {
		t.Errorf("short: %q", got)
	}
	if got := tail("aaaa\nbbbb\nerror: what failed", 22); got != "…error: what failed" {
		t.Errorf("line cut: %q", got)
	}
	long := strings.Repeat("ü", 50) // no newline: cut at a rune boundary
	got := tail(long, 11)
	if !strings.HasSuffix(long, strings.TrimPrefix(got, "…")) || got != "…"+strings.Repeat("ü", 5) {
		t.Errorf("rune cut: %q", got)
	}
	if got := tail("x\n"+strings.Repeat("y", 30), 10); got != "…"+strings.Repeat("y", 10) {
		t.Errorf("a newline before the window must not drop the window: %q", got)
	}
}

// TestREQ_BG_074_TimeoutIsNamed: a run killed by its timeout says so in the
// journal and the warning, not only "exit -1".
func TestREQ_BG_074_TimeoutIsNamed(t *testing.T) {
	f := qaFake(t)
	bare, _ := remoteWith(t, "main")
	dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n"})
	warning := notifyScript(t, dir, "notify.sh")
	testutil.WriteFiles(t, dir, map[string]string{DefaultsFile: f.config("timeout: 2s\nnotify:\n  command: [./notify.sh]\n")})
	g, _ := qaGuard(t, dir)
	t.Setenv("QA_RUN_SLEEP", "120")
	res := g.RunRepo(context.Background(), "r")
	if res.OK || !strings.Contains(res.Error, "timeout 2s (defaults.yaml) exceeded") {
		t.Fatalf("result: %+v", res)
	}
	if w := readWarning(t, warning); !strings.Contains(w.Error, "timeout 2s") {
		t.Fatalf("warning: %+v", w)
	}
}
