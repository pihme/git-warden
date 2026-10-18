package pushguard

import (
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/rules"
)

func TestAdmitClassifiesEachRef(t *testing.T) {
	const repo = "demo"
	main := "refs/heads/main"
	other := "refs/heads/other"
	fresh := "refs/heads/new"
	sha := strings.Repeat("a", 40)
	tip := strings.Repeat("b", 40)
	moved := strings.Repeat("c", 40)
	entries := []journal.Entry{
		{Event: journal.EventApprove, Repo: repo, Ref: main, SHA: sha, Old: tip},
		{Event: journal.EventApprove, Repo: repo, Ref: other, SHA: sha}, // no recorded old OID
	}
	updates := []rules.Update{
		{Ref: main, New: sha},
		{Ref: other, New: sha},
		{Ref: fresh, New: sha},
	}

	got := Admit(entries, repo, updates, map[string]string{main: tip})
	want := []Admission{
		{Update: updates[0], Decision: Forward, Lease: tip},
		{Update: updates[1], Decision: Forward, Lease: gitx.ZeroOID},
		{Update: updates[2], Decision: Recheck, Lease: gitx.ZeroOID},
	}
	if len(got) != len(want) {
		t.Fatalf("Admit = %+v", got)
	}
	for i := range want {
		if got[i].Decision != want[i].Decision || got[i].Lease != want[i].Lease || got[i].Update.Ref != want[i].Update.Ref {
			t.Errorf("update %d: %+v, want %+v", i, got[i], want[i])
		}
	}

	movedTip := Admit(entries, repo, updates[:1], map[string]string{main: moved})
	if movedTip[0].Decision != Stale || movedTip[0].Lease != tip {
		t.Fatalf("moved tip: %+v", movedTip[0])
	}

	// A mixed push: the approved ref leases against the recorded OID, and the
	// re-checked ref leases against the fresh tip, not against RefDelta.Old.
	mixed := Admit(entries, repo, []rules.Update{updates[0], updates[2]}, map[string]string{main: tip, fresh: moved})
	refs := []*rules.RefDelta{
		{Update: rules.Update{Ref: main, Old: "not-the-lease", New: "n1", Kind: rules.NonFF}},
		{Update: rules.Update{Ref: fresh, Old: "also-not", New: "n2", Kind: rules.NonFF}},
	}
	args := strings.Join(ForwardArgs("/remote.git", true, refs, mixed, func(rule, ref string) bool {
		return rule == "REF-NON-FF" && ref == fresh
	}), " ")
	for _, want := range []string{
		"--force-with-lease=" + main + ":" + tip,
		"--force-with-lease=" + fresh + ":" + moved,
		"n1:" + main,
		"n2:" + fresh,
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	if strings.Contains(args, "not-the-lease") || strings.Contains(args, " --force ") {
		t.Fatalf("args %s", args)
	}
}

func TestApprovalSentence(t *testing.T) {
	const recorded = "The agent's next push of exactly this SHA to this ref is forwarded without re-checking."
	if got := ApprovalSentence("abc"); got != recorded {
		t.Fatalf("recorded lease: %q", got)
	}
	got := ApprovalSentence("")
	if !strings.HasPrefix(got, recorded) || !strings.Contains(got, "zero OID") || !strings.Contains(got, "absent") {
		t.Fatalf("empty old OID: %q", got)
	}
}
