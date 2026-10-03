package pushguard

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/rules"
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

func TestForwardArgsNeverForce(t *testing.T) {
	refs := []*rules.RefDelta{
		{Update: rules.Update{Ref: "refs/heads/main", Old: "a1", New: "b1", Kind: rules.FF}},
		{Update: rules.Update{Ref: "refs/heads/agent/x", Old: "a2", New: "b2", Kind: rules.NonFF}},
		{Update: rules.Update{Ref: "refs/heads/other", Old: "a3", New: "b3", Kind: rules.NonFF}},
		{Update: rules.Update{Ref: "refs/heads/gone", Old: "a4", New: gitx.ZeroOID, Kind: rules.Delete}},
		{Update: rules.Update{Ref: "refs/heads/same", Old: "a5", New: "a5", Kind: rules.Noop}},
	}
	allow := func(rule, ref string) bool { return rule == "REF-NON-FF" && ref == "refs/heads/agent/x" }
	got := strings.Join(ForwardArgs("/remote.git", true, refs, allow), " ")
	want := "push --porcelain --atomic --force-with-lease=refs/heads/agent/x:a2 /remote.git " +
		"b1:refs/heads/main b2:refs/heads/agent/x b3:refs/heads/other :refs/heads/gone"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if ForwardArgs("/r.git", true, refs[4:], allow) != nil {
		t.Fatal("noop push produced arguments")
	}
}

func TestReplay(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":          "agent: {name: replay}\n",
		"repos/demo/warden.yaml": "remote: /nonexistent.git\n",
	})
	repo := testutil.Init(t, false)
	repo.Commit("initial", map[string]string{"README.md": "x\n"})
	repo.Commit("add workflow", map[string]string{".github/workflows/ci.yml": "on: push\n"})
	repo.Commit("add deps", map[string]string{"package.json": "{}\n"})
	var out strings.Builder
	rp := &Replay{ConfigDir: dir, Repo: "demo", GitDir: repo.Dir, Branch: "main", SkipScanner: true, Out: &out}
	if err := rp.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"3 steps: 1 green, 1 yellow, 1 red", "PATH-RED", "PATH-YELLOW", "add workflow"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("replay output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestRemoteMessageHidesURL(t *testing.T) {
	msg, rejected := remoteMessage("/srv/remote.git",
		"To /srv/remote.git\n!\tabc:refs/heads/main\t[remote rejected] (pre-receive hook declined)\nDone\n",
		"remote: protected\nerror: failed to push some refs to '/srv/remote.git'\n")
	if !rejected || strings.Contains(msg, "/srv/remote.git") || !strings.Contains(msg, "remote: protected") {
		t.Fatalf("%v %q", rejected, msg)
	}
}
