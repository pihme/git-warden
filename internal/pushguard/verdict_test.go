package pushguard

import (
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/testutil"
)

// The verdict step reads journal entries only. No git repository is involved.
func TestVerdictStep(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml": "agent: {name: wall}\n",
		"repos/demo/warden.yaml": "remote: /nonexistent.git\nrules:\n" +
			"  RATE-LIMIT: {max_pushes: 2, window: 1h}\n" +
			"  RATE-YELLOW-STREAK: {max_yellow: 1, window: 24h}\n",
	})
	cfg, err := config.LoadRepo(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	push := func(reason string) journal.Entry {
		return journal.Entry{Time: now.Add(-time.Minute), Event: journal.EventPush, Agent: "wall", Repo: "demo", Reason: reason}
	}
	s := step{cfg: cfg, agent: "wall", repo: "demo", now: now, id: "p1"}

	s.entries = []journal.Entry{push("")}
	if limited, _, _ := s.rate(); limited {
		t.Fatal("one push is under the limit of 2")
	}

	s.entries = []journal.Entry{push(""), push("")}
	limited, _, warn := s.rate()
	if !limited || !warn {
		t.Fatalf("at the limit: limited %v warn %v", limited, warn)
	}

	s.entries = []journal.Entry{push(""), push(journal.ReasonRateLimited)}
	limited, _, warn = s.rate()
	if !limited || warn {
		t.Fatalf("second limited push: limited %v warn %v", limited, warn)
	}

	yellow := journal.Entry{Time: now.Add(-time.Minute), Event: journal.EventPush, Agent: "wall", Repo: "demo", Verdict: "yellow"}
	s.entries = []journal.Entry{yellow}
	findings, color, line, red := s.streak(nil, config.Yellow)
	if !red || line == nil || line.Event != journal.EventStreak || color != config.Red || len(findings) != 1 {
		t.Fatalf("yellow starts a streak: red %v line %v color %s findings %v", red, line, color, findings)
	}
	if journal.StreakActive(s.entries, "demo") {
		t.Fatal("streak line was written before the push had a verdict")
	}

	s.entries = append(s.entries, journal.Entry{Time: now, Event: journal.EventStreak, Repo: "demo"})
	findings, color, line, red = s.streak(nil, config.Yellow)
	if !red || line != nil || color != config.Red || len(findings) != 1 {
		t.Fatalf("active streak: red %v line %v color %s findings %v", red, line, color, findings)
	}

	before := append([]journal.Entry(nil), s.entries...)
	_, color, line, red = s.streak(nil, config.Green)
	if !red || line != nil || color != config.Red {
		t.Fatalf("green during an active streak: red %v line %v color %s", red, line, color)
	}
	if !journal.StreakActive(before, "demo") || !journal.StreakActive(s.entries, "demo") {
		t.Fatal("a green push cleared the streak")
	}
}
