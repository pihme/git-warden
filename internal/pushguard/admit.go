package pushguard

import (
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/rules"
)

// Decision is what the guard does with one ref of a push.
type Decision string

const (
	// Recheck means no open approval: the rules run.
	Recheck Decision = "recheck"
	// Forward means an open approval whose lease still matches the remote tip.
	Forward Decision = "forward"
	// Stale means an open approval whose lease is not the remote tip.
	Stale Decision = "stale"
)

// Admission is the classification of one update against the journal and the
// fresh remote tips. Lease is the OID a later forward puts on
// --force-with-lease: the recorded approval lease, or the fresh tip when the
// ref is re-checked. An approval with no recorded old OID has the zero OID,
// so the ref must be absent.
type Admission struct {
	Update   rules.Update
	Decision Decision
	Lease    string
}

// Admit classifies every update once. tips maps ref names to the fresh
// ls-remote OID; a missing ref is the zero OID.
func Admit(entries []journal.Entry, repo string, updates []rules.Update, tips map[string]string) []Admission {
	out := make([]Admission, 0, len(updates))
	for _, u := range updates {
		tip := tips[u.Ref]
		if tip == "" {
			tip = gitx.ZeroOID
		}
		a := Admission{Update: u, Decision: Recheck, Lease: tip}
		old, ok := journal.ApprovalLease(entries, repo, u.Ref, u.New)
		if !ok {
			out = append(out, a)
			continue
		}
		if old == "" {
			old = gitx.ZeroOID
		}
		a.Lease = old
		if tip != old {
			a.Decision = Stale
		} else {
			a.Decision = Forward
		}
		out = append(out, a)
	}
	return out
}

// ApprovalSentence is what approve tells the human. A recorded lease keeps
// the sentence REQ-PG-005 describes. An empty old OID is the zero OID.
func ApprovalSentence(old string) string {
	const next = "The agent's next push of exactly this SHA to this ref is forwarded without re-checking."
	if old == "" {
		return next + " No lease base was recorded, so the lease is the zero OID and the ref must be absent."
	}
	return next
}
