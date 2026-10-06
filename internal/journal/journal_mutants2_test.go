package journal

// Second round of gomutants survivor tests for internal/journal.

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/rules"
)

// linkedJournal returns a journal whose push.jsonl is a symlink to target.
func linkedJournal(t *testing.T, target string) *Journal {
	t.Helper()
	if _, err := os.Stat(target); err != nil {
		t.Skipf("%s not available: %v", target, err)
	}
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "push.jsonl")); err != nil {
		t.Fatal(err)
	}
	return Open(dir)
}

// Append returns the error of the step that failed (fail closed).
func TestMutantsAppendReportsOpenWriteAndSyncErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "push.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Open(dir).Append(Entry{Event: EventPush, Repo: "r"}); !errors.Is(err, syscall.EISDIR) {
		t.Errorf("journal is a directory: %v, want EISDIR from open", err)
	}
	if err := linkedJournal(t, "/dev/full").Append(Entry{Event: EventPush, Repo: "r"}); !errors.Is(err, syscall.ENOSPC) {
		t.Errorf("write to /dev/full: %v, want ENOSPC", err)
	}
	// /dev/null takes the write but cannot be synced: the entry is not durable.
	if err := linkedJournal(t, "/dev/null").Append(Entry{Event: EventPush, Repo: "r"}); err == nil {
		t.Error("fsync failure on the journal was swallowed")
	}
}

func writeJournal(t *testing.T, lines ...string) *Journal {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "push.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Open(dir)
}

func TestMutantsReadKeepsJSONError(t *testing.T) {
	_, err := writeJournal(t, `{"event":"push"}`, `{nope`).Read()
	var se *json.SyntaxError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), ":2:") {
		t.Fatalf("err = %v, want a wrapped *json.SyntaxError on line 2", err)
	}
}

// Lines up to just under 64 MiB are read; longer ones fail closed.
func TestMutantsReadLineLimit(t *testing.T) {
	const max = 64 * 1024 * 1024
	line := func(n int) string {
		head, tail := `{"event":"push","repo":"r","error":"`, `"}`
		return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
	}
	es, err := writeJournal(t, line(max-16*1024)).Read()
	if err != nil || len(es) != 1 || es[0].Repo != "r" {
		t.Fatalf("line just under 64 MiB: %d entries, %v", len(es), err)
	}
	if _, err := writeJournal(t, line(max+16*1024)).Read(); !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("line over 64 MiB: %v, want bufio.ErrTooLong", err)
	}
}

func TestMutantsYellowCountOnlyPushes(t *testing.T) {
	now := time.Now()
	es := []Entry{
		{Event: EventPush, Repo: "r", Verdict: "yellow", Time: now},
		{Event: EventStreak, Repo: "r", Verdict: "yellow", Time: now},
	}
	if n := YellowCount(es, "r", now, time.Hour); n != 1 {
		t.Fatalf("YellowCount = %d, want 1 (a streak entry is not a push)", n)
	}
}

func TestMutantsLastPushFirstAndLastEntry(t *testing.T) {
	if e := LastPush([]Entry{{Event: EventPush, ID: "only", Agent: "a"}}, "a"); e == nil || e.ID != "only" {
		t.Fatalf("single entry: %v", e)
	}
	es := []Entry{{Event: EventPush, ID: "1", Agent: "a"}, {Event: EventPush, ID: "2", Agent: "a"}}
	if e := LastPush(es, "a"); e == nil || e.ID != "2" {
		t.Fatalf("last entry: %v", e)
	}
}

func TestMutantsOpenApprovalMatchesRefAndSHA(t *testing.T) {
	appr := Entry{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA}
	for name, c := range map[string]struct {
		es   []Entry
		want bool
	}{
		"remote moved on another ref": {[]Entry{appr, {Event: EventPush, Repo: "r", Reason: ReasonRemoteMoved,
			Updates: []rules.Update{{Ref: "refs/heads/x", New: shaA}}}}, true},
		"non-forwarded push before the approval": {[]Entry{{Event: EventPush, Repo: "r"}, appr}, true},
		"forwarded other ref": {[]Entry{appr, {Event: EventPush, Repo: "r", Forwarded: true,
			Approved: []RefSHA{{Ref: "refs/heads/x", SHA: shaA}}}}, true},
		"forwarded other sha": {[]Entry{appr, {Event: EventPush, Repo: "r", Forwarded: true,
			Approved: []RefSHA{{Ref: mainRef, SHA: shaB}}}}, true},
		"forwarded same": {[]Entry{appr, {Event: EventPush, Repo: "r", Forwarded: true,
			Approved: []RefSHA{{Ref: mainRef, SHA: shaA}}}}, false},
	} {
		if got := OpenApproval(c.es, "r", mainRef, shaA); got != c.want {
			t.Errorf("%s: OpenApproval = %v, want %v", name, got, c.want)
		}
	}
}

func TestMutantsApprovalLeaseOnlyFromApproveOfThatRef(t *testing.T) {
	es := []Entry{
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaA, Old: "lease"},
		{Event: EventApprove, Repo: "r", Ref: "refs/heads/x", SHA: shaA, Old: "other-ref"},
		{Event: EventReset, Repo: "r", Ref: mainRef, SHA: shaA, Old: "not-an-approval"},
	}
	if old, ok := ApprovalLease(es, "r", mainRef, shaA); !ok || old != "lease" {
		t.Fatalf("lease = %q, %v; want lease", old, ok)
	}
}

func TestMutantsFindRejectedEdges(t *testing.T) {
	// a 4-digit prefix is long enough
	if e, _ := FindRejected([]Entry{rejected("1", mainRef, "", shaA)}, "r", mainRef, "abcd"); e == nil {
		t.Error("4-digit prefix found nothing")
	}
	// a later update of the same push still matches
	two := Entry{Event: EventPush, ID: "2", Repo: "r", Updates: []rules.Update{{Ref: "refs/heads/x", New: shaB}, {Ref: mainRef, New: shaA}}}
	if e, full := FindRejected([]Entry{two}, "r", mainRef, "abcd1"); e == nil || full != shaA {
		t.Errorf("second update: %v %q", e, full)
	}
	// only push entries count
	reset := Entry{Event: EventReset, Repo: "r", Updates: []rules.Update{{Ref: mainRef, New: shaB}}}
	if e, _ := FindRejected([]Entry{rejected("1", mainRef, "", shaA), reset}, "r", mainRef, "abcd"); e == nil {
		t.Error("a non-push entry made the prefix ambiguous")
	}
	if AmbiguousRejectedPrefix([]Entry{rejected("1", mainRef, "", shaA), reset}, "r", mainRef, "abcd") {
		t.Error("a non-push entry counted as a rejected SHA")
	}
	// a SHA exactly as long as the prefix counts
	es := []Entry{rejected("1", mainRef, "", "abcd1234"), rejected("2", mainRef, "", "abcd12345678")}
	if !AmbiguousRejectedPrefix(es, "r", mainRef, "abcd1234") {
		t.Error("SHA equal to the prefix was not counted")
	}
}
