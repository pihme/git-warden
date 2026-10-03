package pushguard

import (
	"strings"

	"github.com/pihme/git-warden/internal/gitx"
	"github.com/pihme/git-warden/internal/journal"
	"github.com/pihme/git-warden/internal/rules"
)

// leaseRules maps a ref kind to the rule that must have explicitly allowed it
// before the guard forwards it with a lease.
var leaseRules = map[rules.Kind]string{
	rules.NonFF:   "REF-NON-FF",
	rules.TagMove: "REF-TAG-MOVE",
	rules.Delete:  "REF-DELETE",
}

// ForwardArgs builds the git push arguments for the checked updates. It never
// uses --force; a non-fast-forward, tag move or delete gets
// --force-with-lease=<ref>:<remote old> only when allow(rule, ref) is true.
func ForwardArgs(url string, atomic bool, refs []*rules.RefDelta, allow func(rule, ref string) bool) []string {
	opts := []string{"push", "--porcelain"}
	if atomic {
		opts = append(opts, "--atomic")
	}
	var specs []string
	for _, rd := range refs {
		if rd.Kind == rules.Noop {
			continue
		}
		if rule, ok := leaseRules[rd.Kind]; ok && allow(rule, rd.Ref) {
			opts = append(opts, "--force-with-lease="+rd.Ref+":"+rd.Old)
		}
		if rd.Kind == rules.Delete {
			specs = append(specs, ":"+rd.Ref)
		} else {
			specs = append(specs, rd.New+":"+rd.Ref)
		}
	}
	if len(specs) == 0 {
		return nil
	}
	return append(append(opts, url), specs...)
}

func (r *run) forward(remote *gitx.Remote, approved, rest *rules.Delta, res *rules.Result) (int, error) {
	isApproved := map[string]bool{}
	for _, rd := range approved.Refs {
		isApproved[rd.Ref] = true
		r.entry.Approved = append(r.entry.Approved, journal.RefSHA{Ref: rd.Ref, SHA: rd.New})
	}
	// A human approval of exactly this SHA counts as an explicit allow.
	allow := func(rule, ref string) bool { return isApproved[ref] || res.IsAllowed(rule, ref) }
	refs := append(append([]*rules.RefDelta{}, approved.Refs...), rest.Refs...)
	args := ForwardArgs(remote.URL, r.cfg.ForwardAtomic, refs, allow)
	if args == nil {
		r.entry.Forwarded = true
		return 0, nil
	}
	stdout, stderr, err := remote.Git().RunFull(r.ctx, nil, args...)
	if err == nil {
		r.entry.Forwarded = true
		r.say("push-guard: forwarded to the remote (push %s)", r.id)
		return 0, nil
	}
	msg, rejected := remoteMessage(remote.URL, string(stdout), string(stderr))
	if !rejected {
		return 1, err
	}
	r.entry.RemoteMessage = msg
	r.say("push-guard: the remote rejected the push (push %s):", r.id)
	for _, l := range gitx.SplitLines(msg) {
		r.say("  %s", l)
	}
	return 1, nil
}

// remoteMessage condenses git push --porcelain output and reports whether the
// remote rejected at least one ref. The remote URL is not passed on.
func remoteMessage(url, stdout, stderr string) (string, bool) {
	var lines []string
	rejected := false
	for _, l := range gitx.SplitLines(stdout) {
		f := strings.SplitN(l, "\t", 3)
		if len(f) == 3 && f[0] == "!" {
			rejected = true
			_, dst, _ := strings.Cut(f[1], ":")
			lines = append(lines, dst+": "+f[2])
		}
	}
	for _, l := range gitx.SplitLines(stderr) {
		lines = append(lines, strings.ReplaceAll(l, url, "<remote>"))
	}
	return strings.Join(lines, "\n"), rejected
}
