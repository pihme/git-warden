//go:build fuzz

package journal

// Property tests for the SHA prefix that `push-guard approve` takes
// (REQ-PG-005): against a simple model, over generated journals whose SHAs
// share long prefixes on purpose. Run with `go test -tags=fuzz ./internal/journal`.

import (
	"strings"
	"testing"

	"github.com/pihme/git-warden/internal/rules"
	"pgregory.net/rapid"
)

// sha draws a 40-hex SHA from a tiny alphabet so prefixes collide often.
var sha = rapid.Custom(func(t *rapid.T) string {
	head := rapid.StringOfN(rapid.SampledFrom([]rune("ab")), 6, 6, -1).Draw(t, "head")
	return head + strings.Repeat("0", 34)
})

var refName = rapid.SampledFrom([]string{"refs/heads/main", "refs/heads/dev", "refs/tags/v1"})
var repoName = rapid.SampledFrom([]string{"demo", "other"})

func entryGen() *rapid.Generator[Entry] {
	return rapid.Custom(func(t *rapid.T) Entry {
		e := Entry{
			Event:     rapid.SampledFrom([]string{EventPush, EventPush, EventPush, EventApprove}).Draw(t, "event"),
			Repo:      repoName.Draw(t, "repo"),
			Forwarded: rapid.Bool().Draw(t, "forwarded"),
		}
		for _, u := range rapid.SliceOfN(rapid.Custom(func(t *rapid.T) rules.Update {
			return rules.Update{Ref: refName.Draw(t, "ref"), New: sha.Draw(t, "sha")}
		}), 0, 3).Draw(t, "updates") {
			e.Updates = append(e.Updates, u)
		}
		return e
	})
}

// model returns the distinct matching SHAs and, for the newest matching entry,
// its index and SHA.
func model(entries []Entry, repo, ref, prefix string) (distinct map[string]bool, newest int, newestSHA string) {
	distinct, newest = map[string]bool{}, -1
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Repo != repo || e.Event != EventPush || e.Forwarded {
			continue
		}
		for _, u := range e.Updates {
			if u.Ref == ref && strings.HasPrefix(u.New, prefix) {
				distinct[u.New] = true
				if newest < 0 {
					newest, newestSHA = i, u.New
				}
			}
		}
	}
	return
}

func TestPropApprovePrefix(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		entries := rapid.SliceOfN(entryGen(), 0, 8).Draw(rt, "entries")
		repo, ref := repoName.Draw(rt, "qrepo"), refName.Draw(rt, "qref")
		var prefix string
		if len(entries) > 0 && rapid.Bool().Draw(rt, "fromJournal") {
			// Take a prefix of a SHA that is really in the journal.
			e := rapid.SampledFrom(entries).Draw(rt, "e")
			if len(e.Updates) > 0 {
				s := rapid.SampledFrom(e.Updates).Draw(rt, "u").New
				prefix = s[:rapid.IntRange(0, 40).Draw(rt, "n")]
			}
		} else {
			prefix = rapid.StringOfN(rapid.SampledFrom([]rune("ab0")), 0, 12, -1).Draw(rt, "prefix")
		}

		distinct, newest, newestSHA := model(entries, repo, ref, prefix)
		got, full := FindRejected(entries, repo, ref, prefix)
		amb := AmbiguousRejectedPrefix(entries, repo, ref, prefix)

		short := len(prefix) < 4
		switch {
		case short:
			if got != nil || full != "" || amb {
				rt.Fatalf("prefix %q shorter than 4 chars matched: %v %q amb=%v", prefix, got, full, amb)
			}
		case len(distinct) == 1:
			if got != &entries[newest] || full != newestSHA {
				rt.Fatalf("prefix %q: got entry %p %q, want newest entry %d with %q", prefix, got, full, newest, newestSHA)
			}
			if amb {
				rt.Fatalf("prefix %q with one SHA reported ambiguous", prefix)
			}
		default:
			if got != nil || full != "" {
				rt.Fatalf("prefix %q matching %d SHAs approved %q", prefix, len(distinct), full)
			}
			if amb != (len(distinct) > 1) {
				rt.Fatalf("prefix %q matching %d SHAs: ambiguous=%v", prefix, len(distinct), amb)
			}
		}
		// The two functions must agree: never both "found" and "ambiguous".
		if got != nil && amb {
			rt.Fatalf("prefix %q both found and ambiguous", prefix)
		}
	})
}
