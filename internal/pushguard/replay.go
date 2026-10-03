package pushguard

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/pihme/git-warden/internal/config"
	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/rules"
)

// Replay runs the rules over existing history: every first-parent step of
// branch is treated as one push (the time of the push is the commit's
// committer time). Nothing is forwarded and nothing is written to the
// journal. RATE-* rules are not replayed.
type Replay struct {
	ConfigDir   string
	Repo        string
	GitDir      string
	Branch      string
	SkipScanner bool
	Verbose     bool // print every finding, not only the summary per step
	Out         io.Writer
}

type replayCount struct{ red, yellow, steps int }

// Run executes the replay.
func (rp *Replay) Run(ctx context.Context) error {
	cfg, err := config.LoadRepo(rp.ConfigDir, rp.Repo)
	if err != nil {
		return err
	}
	g := &gitx.Git{Dir: rp.GitDir, Unset: []string{"GIT_DIR", "GIT_WORK_TREE"}}
	branch := rp.Branch
	if branch == "" {
		branch = "main"
	}
	ref := "refs/heads/" + strings.TrimPrefix(branch, "refs/heads/")
	steps, err := g.Lines(ctx, "rev-list", "--first-parent", "--reverse", ref)
	if err != nil {
		return err
	}
	var sc *rules.Scanner
	if !rp.SkipScanner {
		if sc, err = rules.NewScanner(cfg, rp.GitDir); err != nil {
			return err
		}
		defer sc.Close()
	}
	counts := map[string]*replayCount{}
	verdicts := map[config.Color]int{}
	prev := ""
	for _, oid := range steps {
		raw, err := g.Run(ctx, "cat-file", "commit", oid)
		if err != nil {
			return err
		}
		c, err := rules.ParseCommit(oid, raw)
		if err != nil {
			return err
		}
		remote := map[string]string{}
		if prev != "" {
			remote[ref] = prev
		}
		in := rules.Input{
			Updates:       []rules.Update{{Ref: ref, New: oid}},
			Remote:        remote,
			DefaultBranch: ref,
			Now:           c.Committer,
		}
		d, err := rules.Normalize(ctx, g, in)
		if err != nil {
			return fmt.Errorf("step %s: %w", oid, err)
		}
		res, err := rules.Evaluate(ctx, g, cfg, d, rules.Options{Scanner: sc, SkipScanner: rp.SkipScanner})
		if err != nil {
			return fmt.Errorf("step %s: %w", oid, err)
		}
		v := rules.Verdict(res.Findings)
		verdicts[v]++
		hit := map[string]bool{}
		for _, f := range res.Findings {
			rc := counts[f.Rule]
			if rc == nil {
				rc = &replayCount{}
				counts[f.Rule] = rc
			}
			if f.Color == config.Red {
				rc.red++
			} else {
				rc.yellow++
			}
			if !hit[f.Rule] {
				hit[f.Rule] = true
				rc.steps++
			}
		}
		if v != config.Green {
			subject, _, _ := strings.Cut(c.Message, "\n")
			fmt.Fprintf(rp.Out, "%s %-6s %s\n", oid[:12], v, truncate(subject, 72))
			if rp.Verbose {
				for _, f := range res.Findings {
					fmt.Fprintf(rp.Out, "    %s\n", f.String())
				}
			} else {
				ids := make([]string, 0, len(hit))
				for id := range hit {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				fmt.Fprintf(rp.Out, "    %s\n", strings.Join(ids, " "))
			}
		}
		prev = oid
	}
	fmt.Fprintf(rp.Out, "\n%d steps: %d green, %d yellow, %d red\n", len(steps), verdicts[config.Green], verdicts[config.Yellow], verdicts[config.Red])
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fmt.Fprintf(rp.Out, "%-22s %6s %6s %6s\n", "rule", "steps", "red", "yellow")
	for _, id := range ids {
		c := counts[id]
		fmt.Fprintf(rp.Out, "%-22s %6d %6d %6d\n", id, c.steps, c.red, c.yellow)
	}
	return nil
}
