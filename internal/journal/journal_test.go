package journal

import (
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/rules"
)

func TestAppendRead(t *testing.T) {
	j := Open(t.TempDir())
	if es, err := j.Read(); err != nil || len(es) != 0 {
		t.Fatalf("empty journal: %v %v", es, err)
	}
	for _, e := range []Entry{{Event: EventPush, ID: "1", Repo: "r"}, {Event: EventApprove, Repo: "r", Ref: "refs/heads/x", SHA: "abc"}} {
		if err := j.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	es, err := j.Read()
	if err != nil || len(es) != 2 || es[1].SHA != "abc" || es[0].Time.IsZero() {
		t.Fatalf("read back: %+v %v", es, err)
	}
}

func TestApprovals(t *testing.T) {
	es := []Entry{
		{Event: EventPush, Repo: "r", Verdict: "red"},
		{Event: EventApprove, Repo: "r", Ref: "refs/heads/x", SHA: "aaa"},
		{Event: EventApprove, Repo: "other", Ref: "refs/heads/y", SHA: "bbb"},
	}
	if !OpenApproval(es, "r", "refs/heads/x", "aaa") {
		t.Fatal("approval not open")
	}
	if OpenApproval(es, "r", "refs/heads/y", "bbb") || OpenApproval(es, "r", "refs/heads/x", "aab") {
		t.Fatal("approval matched another repo or SHA")
	}
	es = append(es, Entry{Event: EventPush, Repo: "r", Forwarded: true, Approved: []RefSHA{{"refs/heads/x", "aaa"}}})
	if OpenApproval(es, "r", "refs/heads/x", "aaa") {
		t.Fatal("used approval still open")
	}
}

func TestStreakAndCounts(t *testing.T) {
	now := time.Now()
	es := []Entry{
		{Time: now.Add(-48 * time.Hour), Event: EventPush, Agent: "a", Repo: "r", Verdict: "yellow"},
		{Time: now.Add(-time.Hour), Event: EventPush, Agent: "a", Repo: "r", Verdict: "yellow"},
		{Time: now.Add(-time.Minute), Event: EventPush, Agent: "a", Repo: "s", Verdict: "yellow"},
	}
	if n := YellowCount(es, "r", now, 24*time.Hour); n != 1 {
		t.Fatalf("yellow count %d", n)
	}
	if n := PushCount(es, "a", now, 2*time.Hour); n != 2 {
		t.Fatalf("push count %d", n)
	}
	es = append(es, Entry{Event: EventStreak, Repo: "r"})
	if !StreakActive(es, "r") || StreakActive(es, "s") {
		t.Fatal("streak state wrong")
	}
	es = append(es, Entry{Event: EventReset, Repo: "r"})
	if StreakActive(es, "r") || YellowCount(es, "r", now, 24*time.Hour) != 0 {
		t.Fatal("reset did not end the streak")
	}
}

func TestApprovalLeaseAndRemoteMovedInvalidation(t *testing.T) {
	const (
		repo = "r"
		ref  = "refs/heads/main"
		sha  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		old  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	es := []Entry{
		{Event: EventPush, Repo: repo, Verdict: "red", Updates: []rules.Update{{Ref: ref, Old: old, New: sha}}},
		{Event: EventApprove, Repo: repo, Ref: ref, SHA: sha, Old: old},
	}
	got, ok := ApprovalLease(es, repo, ref, sha)
	if !ok || got != old {
		t.Fatalf("ApprovalLease = %q, %v; want %q, true", got, ok, old)
	}
	if !OpenApproval(es, repo, ref, sha) {
		t.Fatal("approval should be open")
	}

	es = append(es, Entry{
		Event: EventPush, Repo: repo, Verdict: "red", Reason: ReasonRemoteMoved,
		Updates: []rules.Update{{Ref: ref, New: sha}},
	})
	if OpenApproval(es, repo, ref, sha) {
		t.Fatal("ReasonRemoteMoved should invalidate the approval")
	}
	if _, ok := ApprovalLease(es, repo, ref, sha); ok {
		t.Fatal("ApprovalLease should report closed after remote-moved")
	}

	es2 := []Entry{{Event: EventApprove, Repo: repo, Ref: ref, SHA: sha}}
	got, ok = ApprovalLease(es2, repo, ref, sha)
	if !ok || got != "" {
		t.Fatalf("manual ApprovalLease = %q, %v; want empty, true", got, ok)
	}
}
