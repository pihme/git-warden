package journal

// Requirement tests for the journal queries behind RATE-LIMIT (REQ-PG-036) and
// RATE-YELLOW-STREAK (REQ-PG-037); see docs/push-guard-testspec.md.

import (
	"testing"
	"time"
)

func TestREQ_PG_036_PushCountWindow(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	w := time.Hour
	es := []Entry{
		{Time: now.Add(-w - time.Nanosecond), Event: EventPush, Agent: "a", Repo: "r"}, // just outside
		{Time: now.Add(-w), Event: EventPush, Agent: "a", Repo: "r"},                   // exactly on the edge counts
		{Time: now.Add(-time.Minute), Event: EventPush, Agent: "a", Repo: "other"},     // all repos count
		{Time: now.Add(-time.Minute), Event: EventPush, Agent: "b", Repo: "r"},         // other agent
		{Time: now.Add(-time.Minute), Event: EventApprove, Agent: "a", Repo: "r"},      // not a push
		{Time: now.Add(-time.Minute), Event: EventReset, Agent: "a", Repo: "r"},        // a reset does not clear the rate
		{Time: now, Event: EventPush, Agent: "a", Repo: "r", Verdict: "red"},           // rejected pushes count
	}
	if n := PushCount(es, "a", now, w); n != 3 {
		t.Fatalf("REQ-PG-036: PushCount = %d, want 3", n)
	}
	if n := PushCount(es, "b", now, w); n != 1 {
		t.Fatalf("REQ-PG-036: PushCount(b) = %d, want 1", n)
	}
}

func TestREQ_PG_037_YellowCountWindowAndScope(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	w := 24 * time.Hour
	es := []Entry{
		{Time: now.Add(-w - time.Nanosecond), Event: EventPush, Agent: "a", Repo: "r", Verdict: "yellow"},
		{Time: now.Add(-w), Event: EventPush, Agent: "a", Repo: "r", Verdict: "yellow"},
		{Time: now.Add(-time.Hour), Event: EventPush, Agent: "a", Repo: "r", Verdict: "red"},
		{Time: now.Add(-time.Hour), Event: EventPush, Agent: "a", Repo: "r", Verdict: "yellow"},
		{Time: now.Add(-time.Hour), Event: EventPush, Agent: "a", Repo: "s", Verdict: "yellow"},
		{Time: now.Add(-time.Minute), Event: EventPush, Agent: "a", Repo: "r", Verdict: "green", Forwarded: true},
		{Time: now, Event: EventPush, Agent: "a", Repo: "r", Verdict: "yellow"},
	}
	// edge, -1h and now; red, green, the other repo and the old one do not count,
	// and the green push in between does not reset the count
	if n := YellowCount(es, "r", now, w); n != 3 {
		t.Fatalf("REQ-PG-037: YellowCount = %d, want 3", n)
	}
	// an approve or reset in another repo changes nothing
	es = append(es, Entry{Time: now, Event: EventApprove, Repo: "s"}, Entry{Time: now, Event: EventReset, Repo: "s"})
	if n := YellowCount(es, "r", now, w); n != 3 {
		t.Fatalf("REQ-PG-037: other repo's approve/reset changed the count to %d", n)
	}
	// an approve in this repo starts from zero
	es = append(es, Entry{Time: now, Event: EventApprove, Repo: "r"})
	if n := YellowCount(es, "r", now, w); n != 0 {
		t.Fatalf("REQ-PG-037: YellowCount after approve = %d", n)
	}
}

func TestREQ_PG_037_StreakEndsOnlyByApproveOrReset(t *testing.T) {
	now := time.Now()
	es := []Entry{
		{Time: now, Event: EventStreak, Agent: "a", Repo: "r"},
		{Time: now, Event: EventPush, Agent: "a", Repo: "r", Verdict: "green", Forwarded: true},
		{Time: now, Event: EventApprove, Repo: "s"},
		{Time: now, Event: EventReset, Repo: "s"},
	}
	if !StreakActive(es, "r") {
		t.Fatal("REQ-PG-037: a green push or another repo's approve/reset ended the streak")
	}
	if StreakActive(es, "s") {
		t.Fatal("REQ-PG-037: streak leaked into another repo")
	}
	approved := append(append([]Entry{}, es...), Entry{Time: now, Event: EventApprove, Repo: "r", Ref: "refs/heads/main", SHA: "abc"})
	if StreakActive(approved, "r") {
		t.Fatal("REQ-PG-037: approve did not end the streak")
	}
	reset := append(append([]Entry{}, es...), Entry{Time: now, Event: EventReset, Repo: "r"})
	if StreakActive(reset, "r") {
		t.Fatal("REQ-PG-037: reset did not end the streak")
	}
	// a new streak after an approve is active again
	again := append(approved, Entry{Time: now, Event: EventStreak, Agent: "a", Repo: "r"})
	if !StreakActive(again, "r") {
		t.Fatal("REQ-PG-037: new streak after approve not active")
	}
}
