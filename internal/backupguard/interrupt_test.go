package backupguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperBackupRun is not a test: TestInterruptedFirstRun starts the test
// binary with BACKUP_GUARD_HELPER=<config dir> to get a Backup Guard run in a
// process of its own, which the stand-in programs then kill.
func TestHelperBackupRun(t *testing.T) {
	dir := os.Getenv("BACKUP_GUARD_HELPER")
	if dir == "" {
		t.Skip("helper process only")
	}
	ctx := context.Background()
	g, repos, err := Preflight(ctx, dir, nil)
	if err != nil {
		os.Exit(3)
	}
	if err := g.RunAll(ctx, repos); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// killer writes a stand-in for prog (git or git-everref) that behaves like
// the real one, except that the first call whose arguments contain match
// kills the Backup Guard process that started it (SIGKILL, no cleanup) and
// itself. With background set, the real program runs for a moment first, so
// the guard dies in the middle of that call.
func killer(t *testing.T, dir, prog, real, match string, background bool) {
	t.Helper()
	marker := filepath.Join(dir, prog+".killed")
	body := `exec "` + real + `" "$@"`
	kill := `touch "` + marker + `"; kill -9 $PPID; kill -9 $$`
	if background {
		kill = `touch "` + marker + `"; "` + real + `" "$@" & sleep 0.3; kill -9 $PPID; kill -9 $!; kill -9 $$`
	}
	script := "#!/bin/sh\n" +
		`case " $* " in *"` + match + `"*) [ -e "` + marker + `" ] || { ` + kill + `; }; esac` + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, prog), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestInterruptedFirstRun kills the very first run of a repo at each stage of
// its setup and of the everref run. The next run must back everything up as
// if nothing had happened: no half-made bridge or backup that fails forever,
// no lost data, and the lock of the killed process must not block it.
func TestInterruptedFirstRun(t *testing.T) {
	everref := realEverref(t)
	git := mustLook(t, "git")
	cases := []struct {
		name, prog, match string
		background        bool
	}{
		{"git init of the backup", "git", " --bare ", false},
		{"bridge created, no remotes yet", "git", " remote add origin ", false},
		{"bridge without backup remote", "git", " remote add backup ", false},
		{"before bridged tags", "git-everref", " tags ", false},
		{"before add", "git-everref", " add ", false},
		{"during add", "git-everref", " add ", true},
		{"during the first everref run", "git-everref", " run --all ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bare, w := remoteWith(t, "main", "feature")
			w.Git("tag", "v1")
			w.Git("push", "-q", "origin", "v1")
			bin := t.TempDir()
			for _, p := range []struct{ name, real string }{{"git", git}, {"git-everref", everref}} {
				if p.name == c.prog {
					killer(t, bin, p.name, p.real, c.match, c.background)
				} else if err := os.Symlink(p.real, filepath.Join(bin, p.name)); err != nil {
					t.Fatal(err)
				}
			}
			dir := writeConfig(t, "", map[string]string{"r": "remote: " + bare + "\n"})

			cmd := exec.Command(os.Args[0], "-test.run=^TestHelperBackupRun$")
			cmd.Env = append(os.Environ(), "BACKUP_GUARD_HELPER="+dir, "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if _, statErr := os.Stat(filepath.Join(bin, c.prog+".killed")); statErr != nil {
				t.Fatalf("the stand-in never killed the run (%v): %s", err, out)
			}
			if err == nil {
				t.Fatalf("the killed run reported success: %s", out)
			}

			// The next run, with the real programs, finishes the job.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			t.Setenv("PATH", bin+":"+os.Getenv("PATH")) // stand-ins pass through now
			g, repos, err := Preflight(ctx, dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := g.RunAll(ctx, repos); err != nil {
				t.Fatalf("run after the killed one: %v\n%+v", err, readJournal(t, g.Defaults.StateDir))
			}
			refs := backupRefs(t, BackupPath(g.Defaults.StateDir, "r"))
			if countPrefix(refs, "refs/heads/everref/remotes/origin/main/created_") != 1 ||
				countPrefix(refs, "refs/heads/everref/remotes/origin/feature/created_") != 1 ||
				countPrefix(refs, "refs/tags/everref/remotes/origin/tags/v1/created_") != 1 {
				t.Fatalf("backup after resuming:\n%s", strings.Join(refs, "\n"))
			}
			// and the one after that has nothing new to do
			if err := g.RunAll(ctx, repos); err != nil {
				t.Fatalf("third run: %v", err)
			}
			if j := readJournal(t, g.Defaults.StateDir); !j[len(j)-1].OK || j[len(j)-1].New != 0 || j[len(j)-1].Rewritten != 0 {
				t.Fatalf("third run journal: %+v", j[len(j)-1])
			}
			entries, _ := os.ReadDir(filepath.Join(g.Defaults.StateDir, "repos", "r"))
			for _, e := range entries {
				if e.Name() != "bridge" && e.Name() != "backup.git" {
					t.Errorf("left over in the repo's state: %s", e.Name())
				}
			}
		})
	}
}
