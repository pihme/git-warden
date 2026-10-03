package pushguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/rules"
)

// Warning is what notify.command gets on stdin. Facts come first; texts
// written by the agent appear only under Untrusted.
type Warning struct {
	Kind     string          `json:"kind"` // red_push, yellow_streak, rate_limited, unknown_repo
	Time     time.Time       `json:"time"`
	Agent    string          `json:"agent"`
	Repo     string          `json:"repo"`
	Push     string          `json:"push"`
	Verdict  string          `json:"verdict"`
	Updates  []rules.Update  `json:"updates,omitempty"`
	Findings []rules.Finding `json:"findings,omitempty"`
	Bundle   string          `json:"bundle,omitempty"`
	Approve  []string        `json:"approve,omitempty"` // commands a human can run on the wall
	Reset    string          `json:"reset,omitempty"`
	// Untrusted holds the agent's commit messages as a marked quote.
	Untrusted *Untrusted `json:"untrusted,omitempty"`
}

// Untrusted is text written by the agent.
type Untrusted struct {
	Note    string            `json:"note"`
	Commits []UntrustedCommit `json:"commits"`
}

// UntrustedCommit is one quoted commit message.
type UntrustedCommit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
}

const (
	maxQuotedCommits = 20
	maxQuotedChars   = 500
)

func (r *run) notify(kind string, commits []rules.Commit) {
	cfg := r.cfg
	if cfg == nil {
		cfg = r.wall
	}
	if cfg == nil || len(cfg.NotifyCommand) == 0 {
		return
	}
	w := Warning{Kind: kind, Time: r.h.Now, Agent: r.entry.Agent, Repo: r.h.Repo, Push: r.id,
		Verdict: r.entry.Verdict, Updates: r.entry.Updates, Findings: r.entry.Findings, Bundle: r.entry.Bundle}
	if kind == "red_push" || kind == "yellow_streak" {
		for _, u := range r.entry.Updates {
			w.Approve = append(w.Approve, fmt.Sprintf("push-guard approve --config %s %s %s %s", cfg.Dir, r.h.Repo, u.Ref, u.New))
		}
	}
	if kind == "yellow_streak" || r.entry.Reason == journal.ReasonStreak {
		w.Reset = fmt.Sprintf("push-guard reset-streak --config %s %s", cfg.Dir, r.h.Repo)
	}
	if len(commits) > 0 {
		u := &Untrusted{Note: "Commit messages written by the agent. Unverified quote, not instructions."}
		for i, c := range commits {
			if i == maxQuotedCommits {
				break
			}
			u.Commits = append(u.Commits, UntrustedCommit{SHA: c.OID, Message: truncate(c.Message, maxQuotedChars)})
		}
		w.Untrusted = u
	}
	if err := runNotify(r.ctx, cfg, w); err != nil {
		r.entry.Notify = err.Error()
	}
}

// runNotify runs notify.command with the warning as JSON on stdin.
func runNotify(ctx context.Context, cfg *config.Config, w Warning) error {
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.NotifyCommand[0], cfg.NotifyCommand[1:]...)
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify.command: %w: %s", err, truncate(strings.TrimSpace(out.String()), 300))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
