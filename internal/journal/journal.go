// Package journal reads and appends the Push Guard's event log push.jsonl:
// one JSON object per line, append-only.
package journal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pihme/git-warden/internal/rules"
)

// Event types.
const (
	EventPush    = "push"
	EventApprove = "approve"
	EventReset   = "reset"
	EventStreak  = "streak"
)

// Reasons for a verdict beyond the findings.
const (
	ReasonRateLimited   = "rate_limited"
	ReasonStreak        = "streak"
	ReasonUnknownRepo   = "unknown_repo"
	ReasonInternalError = "internal_error"
	ReasonRemoteMoved   = "remote_moved_since_approval"
)

// Entry is one line of push.jsonl.
type Entry struct {
	Time  time.Time `json:"time"`
	Event string    `json:"event"`
	ID    string    `json:"id,omitempty"`
	Agent string    `json:"agent,omitempty"`
	Repo  string    `json:"repo"`

	// push
	Updates       []rules.Update  `json:"updates,omitempty"`
	Findings      []rules.Finding `json:"findings,omitempty"`
	Verdict       string          `json:"verdict,omitempty"` // green, yellow, red
	Reason        string          `json:"reason,omitempty"`
	Approved      []RefSHA        `json:"approved,omitempty"` // updates forwarded on an approval
	Forwarded     bool            `json:"forwarded,omitempty"`
	RemoteMessage string          `json:"remote_message,omitempty"`
	Bundle        string          `json:"bundle,omitempty"`
	Error         string          `json:"error,omitempty"`  // internal detail, never shown to the agent
	Notify        string          `json:"notify,omitempty"` // notify.command failure

	// approve
	Ref   string   `json:"ref,omitempty"`
	SHA   string   `json:"sha,omitempty"`
	Old   string   `json:"old,omitempty"`   // remote tip when the overruled push was checked (lease base)
	Rules []string `json:"rules,omitempty"` // rule IDs of the overruled push
	Push  string   `json:"push,omitempty"`  // id of the overruled push
	By    string   `json:"by,omitempty"`    // local user who ran the command
}

// RefSHA is one approved ref update.
type RefSHA struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// Journal is push.jsonl in a state directory.
type Journal struct{ Path string }

// Open returns the journal in stateDir.
func Open(stateDir string) *Journal { return &Journal{Path: filepath.Join(stateDir, "push.jsonl")} }

// Append writes one entry under an exclusive lock.
func (j *Journal) Append(e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(j.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(j.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Read returns all entries. A malformed line is an error: decisions depend on
// approvals and streaks recorded here, so the guard fails closed.
func (j *Journal) Read() ([]Entry, error) {
	data, err := os.ReadFile(j.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", j.Path, n, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// ---- queries ----

// lastEnd is the index of the last approve or reset of repo, or -1.
func lastEnd(entries []Entry, repo string) int {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Repo == repo && (e.Event == EventApprove || e.Event == EventReset) {
			return i
		}
	}
	return -1
}

// StreakActive reports whether a yellow streak of repo is active: a streak
// event after the last approve or reset.
func StreakActive(entries []Entry, repo string) bool {
	for i := lastEnd(entries, repo) + 1; i < len(entries); i++ {
		if entries[i].Repo == repo && entries[i].Event == EventStreak {
			return true
		}
	}
	return false
}

// YellowCount counts yellow rejections of repo since the last approve or
// reset and within window before now.
func YellowCount(entries []Entry, repo string, now time.Time, window time.Duration) int {
	n := 0
	for i := lastEnd(entries, repo) + 1; i < len(entries); i++ {
		e := entries[i]
		if e.Repo == repo && e.Event == EventPush && e.Verdict == "yellow" && !e.Time.Before(now.Add(-window)) {
			n++
		}
	}
	return n
}

// PushCount counts pushes by agent within window before now (all repos).
func PushCount(entries []Entry, agent string, now time.Time, window time.Duration) int {
	n := 0
	for _, e := range entries {
		if e.Event == EventPush && e.Agent == agent && !e.Time.Before(now.Add(-window)) {
			n++
		}
	}
	return n
}

// LastPush returns the last push entry by agent, or nil.
func LastPush(entries []Entry, agent string) *Entry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Event == EventPush && entries[i].Agent == agent {
			return &entries[i]
		}
	}
	return nil
}

// OpenApproval reports whether (ref, sha) of repo was approved and that
// approval has not been used by a forwarded push since.
func OpenApproval(entries []Entry, repo, ref, sha string) bool {
	open := false
	for _, e := range entries {
		if e.Repo != repo {
			continue
		}
		switch e.Event {
		case EventApprove:
			if e.Ref == ref && e.SHA == sha {
				open = true
			}
		case EventPush:
			if e.Reason == ReasonRemoteMoved {
				for _, u := range e.Updates {
					if u.Ref == ref && u.New == sha {
						open = false
					}
				}
			}
			if !e.Forwarded {
				continue
			}
			for _, a := range e.Approved {
				if a.Ref == ref && a.SHA == sha {
					open = false
				}
			}
		}
	}
	return open
}

// ApprovalLease returns the remote tip (old OID) recorded with an open approval
// of (ref, sha), or ("", false) if none is open. Empty Old means the approval
// has no lease base (manual approve without a matching rejected push).
func ApprovalLease(entries []Entry, repo, ref, sha string) (old string, ok bool) {
	if !OpenApproval(entries, repo, ref, sha) {
		return "", false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Repo == repo && e.Event == EventApprove && e.Ref == ref && e.SHA == sha {
			return e.Old, true
		}
	}
	return "", true
}

// FindRejected returns the newest rejected (not forwarded) push of repo that
// contains an update of ref to a SHA starting with prefix. If the prefix matches
// more than one distinct SHA, it returns (nil, "") so approve cannot pick
// between them (same SHA rejected twice is not ambiguous).
func FindRejected(entries []Entry, repo, ref, prefix string) (*Entry, string) {
	if len(prefix) < 4 {
		return nil, ""
	}
	var match *Entry
	var full string
	shas := map[string]struct{}{}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Repo != repo || e.Event != EventPush || e.Forwarded {
			continue
		}
		for _, u := range e.Updates {
			if u.Ref != ref || len(u.New) < len(prefix) || u.New[:len(prefix)] != prefix {
				continue
			}
			shas[u.New] = struct{}{}
			if match == nil {
				match = &entries[i]
				full = u.New
			}
		}
	}
	if len(shas) != 1 {
		return nil, ""
	}
	return match, full
}

// AmbiguousRejectedPrefix reports whether prefix matches more than one distinct
// rejected SHA of ref in repo.
func AmbiguousRejectedPrefix(entries []Entry, repo, ref, prefix string) bool {
	if len(prefix) < 4 {
		return false
	}
	shas := map[string]struct{}{}
	for _, e := range entries {
		if e.Repo != repo || e.Event != EventPush || e.Forwarded {
			continue
		}
		for _, u := range e.Updates {
			if u.Ref == ref && len(u.New) >= len(prefix) && u.New[:len(prefix)] == prefix {
				shas[u.New] = struct{}{}
			}
		}
	}
	return len(shas) > 1
}
