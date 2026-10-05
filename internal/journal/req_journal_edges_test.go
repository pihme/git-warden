package journal

// Journal file handling and the queries behind approve (REQ-PG-005) and the
// rate-limit notice (REQ-PG-036); see docs/push-guard-testspec.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pihme/git-warden/internal/rules"
)

const (
	mainRef = "refs/heads/main"
	shaA    = "abcd111111111111111111111111111111111111"
	shaB    = "abcd222222222222222222222222222222222222"
)

func rejected(id, ref, old, sha string) Entry {
	return Entry{Event: EventPush, ID: id, Repo: "r", Verdict: "red",
		Updates: []rules.Update{{Ref: ref, Old: old, New: sha}}}
}

// approve with a SHA prefix finds the newest rejected push of that ref; forwarded
// pushes, other repos and other refs do not count, and prefixes need 4+ hex digits.
func TestREQ_PG_005_FindRejectedByPrefix(t *testing.T) {
	fwd := rejected("f", mainRef, "", shaB)
	fwd.Forwarded = true
	other := rejected("o", mainRef, "", shaB)
	other.Repo = "s"
	es := []Entry{
		rejected("1", mainRef, "old1", shaA),
		rejected("2", "refs/heads/x", "", shaB),
		fwd, other,
		{Event: EventApprove, Repo: "r", Ref: mainRef, SHA: shaB},
	}
	e, full := FindRejected(es, "r", mainRef, "abcd1")
	if e == nil || e.ID != "1" || full != shaA {
		t.Fatalf("prefix abcd1: %+v %q", e, full)
	}
	if e, _ := FindRejected(es, "r", mainRef, shaA); e == nil || e.ID != "1" {
		t.Errorf("full SHA: %+v", e)
	}
	for _, p := range []string{"abc", "abcd2", shaA + "0", "ffff"} {
		if e, full := FindRejected(es, "r", mainRef, p); e != nil {
			t.Errorf("prefix %q matched push %s (%s)", p, e.ID, full)
		}
	}
	if e, _ := FindRejected(es, "r", "refs/heads/x", "abcd2"); e == nil || e.ID != "2" {
		t.Errorf("other ref: %+v", e)
	}
	// the same SHA rejected twice is not ambiguous: the newest push wins
	es = append(es, rejected("3", mainRef, "old3", shaA))
	if e, _ := FindRejected(es, "r", mainRef, "abcd1"); e == nil || e.ID != "3" {
		t.Errorf("same SHA twice: %+v, want push 3", e)
	}
}

// A prefix that matches two different rejected SHAs of the ref must not pick one.
// Otherwise an agent can push a second commit with the same short prefix (7 hex
// digits take about 2^28 tries) after the one the human looked at, and the
// human's "approve r main abcd123" approves the agent's newer commit instead.
func TestREQ_PG_005_AmbiguousPrefixApprovesNothing(t *testing.T) {
	es := []Entry{rejected("1", mainRef, "", shaA), rejected("2", mainRef, "", shaB)}
	if e, full := FindRejected(es, "r", mainRef, "abcd"); e != nil {
		t.Fatalf("REQ-PG-005: ambiguous prefix abcd picked push %s (%s)", e.ID, full)
	}
}

// LastPush is the agent's newest push in any repo (used to send the rate-limit
// notice only for the first push of a limited series).
func TestREQ_PG_036_LastPush(t *testing.T) {
	if LastPush(nil, "a") != nil {
		t.Fatal("empty journal has a last push")
	}
	es := []Entry{
		{Event: EventPush, ID: "1", Agent: "a", Repo: "r"},
		{Event: EventPush, ID: "2", Agent: "a", Repo: "s", Reason: ReasonRateLimited},
		{Event: EventPush, ID: "3", Agent: "b", Repo: "r"},
		{Event: EventApprove, Agent: "a", Repo: "r"},
	}
	if e := LastPush(es, "a"); e == nil || e.ID != "2" || e.Reason != ReasonRateLimited {
		t.Errorf("LastPush(a) = %+v, want push 2", e)
	}
	if e := LastPush(es, "c"); e != nil {
		t.Errorf("LastPush(c) = %+v", e)
	}
}

// Blank lines are skipped, long lines (large findings) are read, and a broken
// line fails closed with its line number.
func TestReadLinesFailClosed(t *testing.T) {
	j := Open(t.TempDir())
	big := strings.Repeat("x", 1<<20)
	if err := j.Append(Entry{Event: EventPush, ID: "big", Repo: "r", Error: big}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(j.Path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "\n   \n")
	f.Close()
	if err := j.Append(Entry{Event: EventReset, Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	es, err := j.Read()
	if err != nil || len(es) != 2 || es[0].Error != big || es[1].Event != EventReset {
		t.Fatalf("read: %d entries, %v", len(es), err)
	}
	f, _ = os.OpenFile(j.Path, os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprint(f, "{\"event\":\"push\",\"repo\":\n")
	f.Close()
	if _, err := j.Read(); err == nil || !strings.Contains(err.Error(), "push.jsonl:5") {
		t.Errorf("broken line 5: err = %v", err)
	}
}

// An unreadable journal is an error, not an empty journal.
func TestReadUnreadableJournal(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "push.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if es, err := Open(dir).Read(); err == nil {
		t.Fatalf("journal is a directory: %d entries, no error", len(es))
	}
}

// Append creates the state directory with private permissions and fails when
// it cannot write.
func TestAppendDirectoryAndErrors(t *testing.T) {
	base := t.TempDir()
	j := Open(filepath.Join(base, "state", "sub"))
	if err := j.Append(Entry{Event: EventReset, Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(j.Path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("journal mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(filepath.Dir(j.Path)); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode = %v, %v; want 0700", st.Mode().Perm(), err)
	}
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	if err := Open(filepath.Join(file, "state")).Append(Entry{Event: EventReset}); err == nil {
		t.Error("state dir under a file: no error")
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "push.jsonl"), 0o700)
	if err := Open(dir).Append(Entry{Event: EventReset}); err == nil {
		t.Error("journal is a directory: no error")
	}
}

// Concurrent appends (several hooks at once) never interleave lines.
func TestConcurrentAppends(t *testing.T) {
	j := Open(t.TempDir())
	const n = 50
	payload := strings.Repeat("y", 64*1024)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- j.Append(Entry{Event: EventPush, ID: fmt.Sprint(i), Repo: "r", Error: payload})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	es, err := j.Read()
	if err != nil || len(es) != n {
		t.Fatalf("read %d entries, %v; want %d", len(es), err, n)
	}
	seen := map[string]bool{}
	for _, e := range es {
		if e.Error != payload {
			t.Fatalf("entry %s damaged", e.ID)
		}
		seen[e.ID] = true
	}
	if len(seen) != n {
		t.Errorf("%d distinct entries, want %d", len(seen), n)
	}
}
