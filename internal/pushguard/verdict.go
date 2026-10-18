package pushguard

import (
	"fmt"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/rules"
)

// step applies the journal rules beside Evaluate. It does not write the
// journal and it does not run git. Replay does not use it.
type step struct {
	cfg     *config.Config
	entries []journal.Entry
	agent   string
	repo    string
	now     time.Time
	id      string
}

// rate reports whether this push is over RATE-LIMIT. warn is true only for
// the first limited push of a series. The push is not evaluated when limited.
func (s step) rate() (limited bool, finding rules.Finding, warn bool) {
	rule := s.cfg.Rule("RATE-LIMIT")
	if !rule.Enabled {
		return false, rules.Finding{}, false
	}
	n := journal.PushCount(s.entries, s.agent, s.now, rule.Window)
	if int64(n) < rule.MaxPushes {
		return false, rules.Finding{}, false
	}
	finding = rules.Finding{Rule: "RATE-LIMIT", Color: rule.Color,
		Message: fmt.Sprintf("%d pushes within %s (limit %d)", n+1, rule.Window, rule.MaxPushes)}
	last := journal.LastPush(s.entries, s.agent)
	warn = last == nil || last.Reason != journal.ReasonRateLimited
	return true, finding, warn
}

// streak may worsen the color and add a RATE-YELLOW-STREAK finding. line is
// the streak entry to append after the push has a verdict, or nil when the
// streak was already active or the push did not cross the limit.
func (s step) streak(findings []rules.Finding, verdict config.Color) ([]rules.Finding, config.Color, *journal.Entry, bool) {
	rule := s.cfg.Rule("RATE-YELLOW-STREAK")
	if !rule.Enabled {
		return findings, verdict, nil, false
	}
	add := func(msg string) {
		findings = append(findings, rules.Finding{Rule: rule.ID, Color: rule.Color, Message: msg})
		if rule.Color.Worse(verdict) {
			verdict = rule.Color
		}
	}
	if journal.StreakActive(s.entries, s.repo) {
		add("yellow streak active; waiting for a human to approve a SHA or reset the streak")
		return findings, verdict, nil, true
	}
	if verdict != config.Yellow {
		return findings, verdict, nil, false
	}
	n := journal.YellowCount(s.entries, s.repo, s.now, rule.Window) + 1
	if int64(n) <= rule.MaxYellow {
		return findings, verdict, nil, false
	}
	add(fmt.Sprintf("%d yellow rejections within %s (limit %d)", n, rule.Window, rule.MaxYellow))
	line := &journal.Entry{Time: s.now, Event: journal.EventStreak, ID: s.id, Agent: s.agent, Repo: s.repo}
	return findings, verdict, line, true
}
