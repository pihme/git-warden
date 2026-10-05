package journal

// Mutation-kill tests for internal/journal. Separate file from fuzz/rapid QA tests.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/rules"
)

func TestMutantsYellowCountRequiresPushEvent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	es := []Entry{
		{Event: EventApprove, Repo: "r", Time: now.Add(-2 * time.Hour)},
		{Event: EventPush, Repo: "r", Verdict: "yellow", Time: now.Add(-30 * time.Minute)},
		{Event: EventStreak, Repo: "r", Time: now.Add(-10 * time.Minute)}, // must not count
	}
	if n := YellowCount(es, "r", now, time.Hour); n != 1 {
		t.Fatalf("YellowCount=%d, want 1 (only yellow push)", n)
	}
}

func TestMutantsLastPushWalksFromEnd(t *testing.T) {
	es := []Entry{
		{Event: EventPush, ID: "1", Agent: "a"},
		{Event: EventApprove, ID: "x", Agent: "a"},
		{Event: EventPush, ID: "2", Agent: "a"},
		{Event: EventPush, ID: "3", Agent: "b"},
	}
	got := LastPush(es, "a")
	if got == nil || got.ID != "2" {
		t.Fatalf("LastPush=%v, want id 2", got)
	}
	if LastPush(es, "nobody") != nil {
		t.Fatal("unknown agent")
	}
}

func TestMutantsOpenApprovalRefAndSHA(t *testing.T) {
	es := []Entry{
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA},
		{Event: EventApprove, Repo: "r", Ref: "refs/heads/x", SHA: shaA},
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaB},
	}
	if !OpenApproval(es, "r", mainRef, shaA) {
		t.Fatal("approve main/shaA should be open")
	}
	// Wrong ref or sha must not keep a false-open from EXPRESSION_REMOVE
	if OpenApproval(es[:1], "r", "refs/heads/other", shaA) {
		t.Fatal("wrong ref reported open")
	}
	if OpenApproval(es[:1], "r", mainRef, shaB) {
		t.Fatal("wrong sha reported open")
	}
}

func TestMutantsOpenApprovalConsumedByForwardAndRemoteMoved(t *testing.T) {
	base := []Entry{{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA}}
	if !OpenApproval(base, "r", mainRef, shaA) {
		t.Fatal("setup")
	}
	fwd := Entry{Event: EventPush, Repo: "r", Forwarded: true,
		Approved: []RefSHA{{Ref: mainRef, SHA: shaA}}}
	if OpenApproval(append(append([]Entry{}, base...), fwd), "r", mainRef, shaA) {
		t.Fatal("forwarded approval must close")
	}
	// Non-forwarded push must not close (continue, not break)
	nofwd := Entry{Event: EventPush, Repo: "r", Forwarded: false,
		Approved: []RefSHA{{Ref: mainRef, SHA: shaA}}}
	if !OpenApproval(append(append([]Entry{}, base...), nofwd), "r", mainRef, shaA) {
		t.Fatal("non-forwarded push closed approval")
	}
	moved := Entry{Event: EventPush, Repo: "r", Reason: ReasonRemoteMoved,
		Updates: []rules.Update{{Ref: mainRef, New: shaA}}}
	if OpenApproval(append(append([]Entry{}, base...), moved), "r", mainRef, shaA) {
		t.Fatal("remote_moved must close matching update")
	}
	movedOther := Entry{Event: EventPush, Repo: "r", Reason: ReasonRemoteMoved,
		Updates: []rules.Update{{Ref: mainRef, New: shaB}}}
	if !OpenApproval(append(append([]Entry{}, base...), movedOther), "r", mainRef, shaA) {
		t.Fatal("remote_moved other sha must not close")
	}
}

func TestMutantsApprovalLeaseMatches(t *testing.T) {
	es := []Entry{
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA, Old: "oldA"},
		{Event: EventApprove, Repo: "other", Ref: mainRef, SHA: shaA, Old: "wrong"},
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaB, Old: "oldB"},
	}
	old, ok := ApprovalLease(es, "r", mainRef, shaA)
	if !ok || old != "oldA" {
		t.Fatalf("lease=%q %v, want oldA", old, ok)
	}
	if _, ok := ApprovalLease(es, "r", mainRef, "deadbeef"); ok {
		t.Fatal("no open approval")
	}
	// Newest matching approve wins when walking backwards
	es2 := []Entry{
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA, Old: "first"},
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA, Old: "second"},
	}
	old, ok = ApprovalLease(es2, "r", mainRef, shaA)
	if !ok || old != "second" {
		t.Fatalf("newest lease=%q %v", old, ok)
	}
}

func TestMutantsFindRejectedPrefixMinAndContinue(t *testing.T) {
	es := []Entry{
		rejected("1", mainRef, "", shaA),
		rejected("2", mainRef, "", shaB),
	}
	if e, _ := FindRejected(es, "r", mainRef, "abc"); e != nil {
		t.Fatal("prefix < 4 must miss")
	}
	// After a non-match, keep scanning (continue not break)
	es = []Entry{
		rejected("1", "refs/heads/x", "", shaA),
		rejected("2", mainRef, "", shaB),
	}
	e, full := FindRejected(es, "r", mainRef, "abcd2")
	if e == nil || e.ID != "2" || full != shaB {
		t.Fatalf("continued scan: %+v %q", e, full)
	}
	// Non-push events must be skipped
	es = []Entry{
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA},
		rejected("2", mainRef, "", shaA),
	}
	e, _ = FindRejected(es, "r", mainRef, "abcd1")
	if e == nil || e.ID != "2" {
		t.Fatalf("skipped non-push: %+v", e)
	}
}

func TestMutantsAmbiguousRejectedPrefixSkipsNonPush(t *testing.T) {
	es := []Entry{
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA},
		rejected("1", mainRef, "", shaA),
		rejected("2", mainRef, "", shaB),
	}
	if !AmbiguousRejectedPrefix(es, "r", mainRef, "abcd") {
		t.Fatal("want ambiguous across two rejected pushes")
	}
	// Short SHA vs long: len >= prefix boundary
	short := "abcd"
	es = []Entry{rejected("1", mainRef, "", short+"1111"), rejected("2", mainRef, "", short)}
	// prefix of full length of short sha: second has exact length
	if AmbiguousRejectedPrefix(es, "r", mainRef, short+"1111") {
		t.Fatal("only one SHA has that long prefix")
	}
}

func TestMutantsAppendAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	j := Open(dir)
	e := Entry{Event: EventPush, Repo: "r", Agent: "a", Verdict: "green", ID: "1",
		Updates: []rules.Update{{Ref: mainRef, New: shaA}}}
	if err := j.Append(e); err != nil {
		t.Fatal(err)
	}
	got, err := j.Read()
	if err != nil || len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("read=%v %v", got, err)
	}
	// malformed line fails closed
	if err := os.WriteFile(filepath.Join(dir, "push.jsonl"), []byte("{\"event\":\"push\"}\nNOTJSON\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Read(); err == nil {
		t.Fatal("malformed line: no error")
	}
}
