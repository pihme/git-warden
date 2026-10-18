package config

// Requirement and edge tests for the configuration (docs/push-guard-rules.md,
// "Configuration"; docs/push-guard.md). See docs/push-guard-testspec.md.

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/testutil"
)

const repoOK = "remote: /srv/git/x.git\n"

// Verdict colours order red > yellow > green; anything else ranks as green.
func TestVerdictColorOrder(t *testing.T) {
	for _, c := range []struct {
		a, b Color
		want bool
	}{
		{Red, Yellow, true}, {Yellow, Green, true}, {Red, Green, true},
		{Yellow, Red, false}, {Green, Yellow, false}, {Red, Red, false},
		{Color("purple"), Green, false}, {Yellow, Color(""), true},
		{Green, Color("purple"), false}, {Red, Color("purple"), true},
	} {
		if got := c.a.Worse(c.b); got != c.want {
			t.Errorf("%q.Worse(%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Load without a repo reads only the wall; Repos lists valid repo folders.
func TestWallOnlyLoadAndRepoList(t *testing.T) {
	dir := t.TempDir()
	if repos, err := Repos(dir); err != nil || repos != nil {
		t.Fatalf("no repos folder: %v, %v", repos, err)
	}
	testutil.WriteFiles(t, dir, map[string]string{
		"defaults.yaml":           wall + "pages_branch: site\n",
		"repos/demo/warden.yaml":  repoOK,
		"repos/other/warden.yaml": repoOK,
		"repos/Bad Name/x":        "",
		"repos/file.yaml":         "",
	})
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.RepoName != "" || c.Remote != "" || c.PagesBranch != "site" || c.AgentName != "test-agent" {
		t.Errorf("wall config = repo %q remote %q pages %q agent %q", c.RepoName, c.Remote, c.PagesBranch, c.AgentName)
	}
	repos, err := Repos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repos, []string{"demo", "other"}) {
		t.Errorf("Repos = %v, want [demo other]", repos)
	}
	rc, err := LoadRepo(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(rc.Dir, "repos", "demo"); rc.RepoDir() != want {
		t.Errorf("RepoDir = %q, want %q", rc.RepoDir(), want)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Repos(filepath.Join(dir, "plain")); err == nil {
		t.Error("Repos on a file: no error")
	}
}

// A rule ID the configuration does not know is disabled and never fires.
func TestUnknownRuleIDIsDisabled(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK})
	r := c.Rule("NO-SUCH-RULE")
	if r.Enabled || r.Fires("x", true) || r.ID != "NO-SUCH-RULE" {
		t.Errorf("unknown rule = %+v", r)
	}
}

// Integer and duration limits: accepted number kinds, missing keys, wrong kinds.
func TestLimitAccessors(t *testing.T) {
	r := &Rule{ID: "X", Limits: map[string]any{
		"int": 3, "int64": int64(4), "uint64": uint64(5), "float": float64(6), "frac": 1.5,
		"str": "10m", "badstr": "soon", "num": 600,
	}}
	for k, want := range map[string]int64{"int": 3, "int64": 4, "uint64": 5, "float": 6} {
		if got, err := r.Int(k); err != nil || got != want {
			t.Errorf("Int(%s) = %d, %v; want %d", k, got, err, want)
		}
	}
	for _, k := range []string{"frac", "str", "missing"} {
		if _, err := r.Int(k); err == nil {
			t.Errorf("Int(%s): no error", k)
		}
	}
	if d, err := r.Duration("str"); err != nil || d != 10*time.Minute {
		t.Errorf("Duration(str) = %v, %v", d, err)
	}
	for _, k := range []string{"badstr", "num", "missing"} {
		if _, err := r.Duration(k); err == nil {
			t.Errorf("Duration(%s): no error", k)
		}
	}
}

// Whole-number limits written as floats or in exponent form load and read as integers.
func TestREQ_PG_032_WholeNumberLimitForms(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  SIZE-LIMIT: {max_files: 2e3, max_lines: 100.0}\n"})
	r := c.Rule("SIZE-LIMIT")
	if n, err := r.Int("max_files"); err != nil || n != 2000 {
		t.Errorf("max_files = %d, %v; want 2000", n, err)
	}
	if n, err := r.Int("max_lines"); err != nil || n != 100 {
		t.Errorf("max_lines = %d, %v; want 100", n, err)
	}
}

// A limit of the wrong kind must be rejected when the configuration loads
// (so preflight and check-config report it), not on the first push.
func TestREQ_PG_032_LimitKindCheckedOnLoad(t *testing.T) {
	for name, rule := range map[string]string{
		"duration as number": "META-FUTURE: {max_skew: 600}",
		"count as duration":  "SIZE-LIMIT: {max_files: '10m'}",
		"count as fraction":  "SIZE-LIMIT: {max_files: 1.5}",
		"count as list":      "SIZE-LIMIT: {max_files: [1]}",
		"count as bool":      "SIZE-LIMIT: {max_files: true}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
				"repos/demo/warden.yaml": repoOK + "rules:\n  " + rule + "\n"})
			if err == nil {
				t.Fatalf("%s loads; every push of the repo would then fail with internal error", rule)
			}
		})
	}
}

// A count limit outside int64 (YAML uint64 wrap) or a negative count must be
// refused when the configuration loads, not turned into a wrapped negative
// limit that every push would then mis-apply.
func TestREQ_PG_032_CountLimitOutOfRange(t *testing.T) {
	for name, rule := range map[string]string{
		"uint64 above MaxInt64": "SIZE-LIMIT: {max_files: 9223372036854775808}",
		"negative count":        "SIZE-LIMIT: {max_files: -1}",
		"float above MaxInt64":  "REF-COUNT: {max_refs: 1e19}",
		"float below MinInt64":  "RATE-LIMIT: {max_pushes: -1e19}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
				"repos/demo/warden.yaml": repoOK + "rules:\n  " + rule + "\n"})
			if err == nil {
				t.Fatalf("%s loads; out-of-range count must fail at load", rule)
			}
			if !strings.Contains(err.Error(), "out of range") && !strings.Contains(err.Error(), "must not be negative") {
				t.Fatalf("err = %v; want out of range or must not be negative", err)
			}
		})
	}
}

// Negative duration limits must be refused when the configuration loads
// (REQ-PG-032 kind check). Zero is allowed (same as count limits): the
// smallest max_skew (REQ-PG-046: zero is allowed only for max_skew among durations). A negative max_age would make
// META-BACKDATED fire on every commit.
func TestREQ_PG_032_NegativeDurationRejected(t *testing.T) {
	for name, rule := range map[string]string{
		"max_skew negative": "META-FUTURE: {max_skew: '-5m'}",
		"window negative":   "RATE-LIMIT: {window: '-1h'}",
		"max_age negative":  "META-BACKDATED: {max_age: '-1s'}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
				"repos/demo/warden.yaml": repoOK + "rules:\n  " + rule + "\n"})
			if err == nil {
				t.Fatalf("%s loads; negative duration must fail at load", rule)
			}
			if !strings.Contains(err.Error(), "must not be negative") {
				t.Fatalf("err = %v; want must not be negative", err)
			}
		})
	}
	// Zero duration still loads (strictest finite bound).
	c := mustLoad(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  META-FUTURE: {max_skew: '0s'}\n"})
	if d, err := c.Rule("META-FUTURE").Duration("max_skew"); err != nil || d != 0 {
		t.Fatalf("zero max_skew = %v, %v; want 0", d, err)
	}
}

// allow_remove and deny_remove take out a default entry; a missing one is an error.
func TestAllowDenyRemove(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall + "rules:\n  REF-DELETE: {allow: ['refs/heads/agent.*']}\n  REF-NAMESPACE: {deny: ['refs/heads/main']}\n",
		"repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE: {allow_remove: ['refs/heads/agent.*']}\n  REF-NAMESPACE: {deny_remove: ['refs/heads/main']}\n",
	})
	if c.Rule("REF-DELETE").Allowed("refs/heads/agent-x") {
		t.Error("allow_remove did not remove the allow entry")
	}
	if c.Rule("REF-NAMESPACE").Fires("refs/heads/main", false) {
		t.Error("deny_remove did not remove the deny entry")
	}
	for _, key := range []string{"allow_remove", "deny_remove"} {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
			"repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE: {" + key + ": ['nope']}\n"})
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "not found") {
			t.Errorf("%s of a missing entry: err = %v", key, err)
		}
	}
}

// A rule listed with no settings keeps its defaults.
func TestEmptyRuleEntryKeepsDefaults(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK + "rules:\n  REF-DELETE:\n"})
	r := c.Rule("REF-DELETE")
	if !r.Enabled || r.Color != Red {
		t.Errorf("REF-DELETE = enabled %v color %q, want on and red", r.Enabled, r.Color)
	}
}

// timeout defaults to 60s and must be a positive duration.
func TestTimeout(t *testing.T) {
	if c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK}); c.Timeout != 60*time.Second {
		t.Errorf("default timeout = %v", c.Timeout)
	}
	if c := mustLoad(t, map[string]string{"defaults.yaml": wall + "timeout: 5s\n", "repos/demo/warden.yaml": repoOK}); c.Timeout != 5*time.Second {
		t.Errorf("timeout 5s = %v", c.Timeout)
	}
	for _, v := range []string{"soon", "0s", "-1s", "1ns"} { // 1ns: below 1s (REQ-PG-047)
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall + "timeout: " + v + "\n", "repos/demo/warden.yaml": repoOK})
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Errorf("timeout %s: err = %v", v, err)
		}
	}
}

// Unreadable or broken configuration files are errors, not defaults.
func TestUnreadableConfigFiles(t *testing.T) {
	for name, setup := range map[string]func(dir string){
		"defaults.yaml is a directory": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "defaults.yaml"), 0o755)
			testutil.WriteFiles(t, dir, map[string]string{"repos/demo/warden.yaml": repoOK})
		},
		"warden.yaml is a directory": func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": wall})
			os.MkdirAll(filepath.Join(dir, "repos", "demo", "warden.yaml"), 0o755)
		},
		"warden.yaml is not YAML": func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: [\n"})
		},
		"defaults.yaml is not YAML": func(dir string) {
			testutil.WriteFiles(t, dir, map[string]string{"defaults.yaml": "agent: {\n", "repos/demo/warden.yaml": repoOK})
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			setup(dir)
			if _, err := LoadRepo(dir, "demo"); err == nil {
				t.Fatal("loads")
			}
		})
	}
}

// Remote settings that do not fit the remote kind are errors.
func TestRemoteSettingMismatch(t *testing.T) {
	for name, c := range map[string]struct{ repo, want string }{
		"known_hosts for https": {"remote: https://example.invalid/o/r.git\ncredential: t\nknown_hosts: kh\n", "known_hosts only applies"},
		"known_hosts for local": {"remote: /srv/git/x.git\nknown_hosts: kh\n", "known_hosts only applies"},
		"unsupported scheme":    {"remote: ftp://example.invalid/x.git\n", "ftp"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": c.repo})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	if _, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": "remote: git@example.invalid:o/r.git\ncredential: k\nknown_hosts: kh\n"}); err != nil {
		t.Errorf("known_hosts for ssh: %v", err)
	}
}

// 2^63 written as a float is out of range, not "not an integer" (the old
// check let it reach an implementation-defined float→int64 conversion).
func TestREQ_PG_032_FloatTwoPow63OutOfRange(t *testing.T) {
	r := &Rule{ID: "X", Limits: map[string]any{"k": float64(1 << 63)}}
	if n, err := r.Int("k"); err == nil || n != 0 || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("Int(2^63 as float) = %d, %v; want out of range", n, err)
	}
	if _, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  REF-COUNT: {max_refs: 9.223372036854775808e18}\n"}); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("load 2^63: err = %v; want out of range", err)
	}
}

// REQ-PG-040, REQ-PG-041: a triggered rule fires unless the subject is
// allowed; deny fires even without a trigger.
func TestREQ_PG_040_041_FiresWithAllowDenyAndTrigger(t *testing.T) {
	allow := regexp.MustCompile(`^(?:refs/heads/ok)$`)
	deny := regexp.MustCompile(`^(?:refs/heads/no)$`)
	r := &Rule{Enabled: true, Allow: []*regexp.Regexp{allow}, Deny: []*regexp.Regexp{deny}}
	if r.Fires("refs/heads/ok", true) {
		t.Error("allowed and triggered must not fire")
	}
	if !r.Fires("refs/heads/other", true) {
		t.Error("not allowed and triggered must fire")
	}
	if r.Fires("refs/heads/other", false) {
		t.Error("not triggered must not fire without deny")
	}
	if !r.Fires("refs/heads/no", false) {
		t.Error("deny must fire without trigger")
	}
}

// REQ-PG-038: a disabled rule allows nothing.
func TestREQ_PG_038_DisabledRuleAllowsNothing(t *testing.T) {
	allow := regexp.MustCompile(`^(?:refs/heads/ok)$`)
	r := &Rule{Enabled: false, Allow: []*regexp.Regexp{allow}}
	if r.Allowed("refs/heads/ok") {
		t.Fatal("disabled rule must not be Allowed")
	}
	r.Enabled = true
	if !r.Allowed("refs/heads/ok") {
		t.Fatal("enabled rule with a matching allow must be Allowed")
	}
}

// REQ-PG-032: Int and Duration return zero and a reason for a limit that is
// missing, of the wrong kind or out of range.
func TestREQ_PG_032_LimitAccessorErrors(t *testing.T) {
	r := &Rule{ID: "R", Limits: map[string]any{
		"frac": 1.5, "zero": 0, "maxu": uint64(math.MaxInt64), "num": 5, "neg1ns": "-1ns",
		"struct": struct{}{}, "skew": "not-a-duration", "neg": "-1s",
		"big": uint64(1 << 63), "negc": int64(-1), "hif": float64(1e19), "lof": float64(-1e19),
	}}
	if n, err := r.Int("missing"); err == nil || n != 0 || !strings.Contains(err.Error(), "not set") {
		t.Errorf("Int missing: %d %v, want 0 and not set", n, err)
	}
	if d, err := r.Duration("missing"); err == nil || d != 0 || !strings.Contains(err.Error(), "not set") {
		t.Errorf("Duration missing: %v %v, want 0 and not set", d, err)
	}
	if n, err := r.Int("struct"); err == nil || n != 0 {
		t.Errorf("Int non-number: %d %v", n, err)
	}
	if d, err := r.Duration("num"); err == nil || d != 0 || !strings.Contains(err.Error(), "is not a duration") {
		t.Errorf("Duration(number): %v %v, want 0 and is not a duration", d, err)
	}
	d, err := r.Duration("skew")
	if err == nil || d != 0 {
		t.Errorf("Duration parse: %v %v", d, err)
	} else if errors.Unwrap(err) == nil {
		t.Error("Duration parse error must wrap the ParseDuration cause")
	}
	if d, err := r.Duration("neg"); err == nil || d != 0 || !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("Duration negative: %v %v", d, err)
	}
	if _, err := r.Duration("neg1ns"); err == nil {
		t.Error("Duration(-1ns) loads")
	}
	if n, err := r.Int("big"); err == nil || n != 0 || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("Int uint64 overflow: %d %v", n, err)
	}
	if n, err := r.Int("negc"); err == nil || n != 0 || !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("Int negative: %d %v", n, err)
	}
	for _, k := range []string{"hif", "lof", "frac"} {
		if n, err := r.Int(k); err == nil || n != 0 {
			t.Errorf("Int(%s) = %d, %v; want 0 and an error", k, n, err)
		}
	}
	if n, err := r.Int("zero"); err != nil || n != 0 {
		t.Errorf("Int(0) = %d, %v; zero is allowed", n, err)
	}
	if n, err := r.Int("maxu"); err != nil || n != math.MaxInt64 {
		t.Errorf("Int(MaxInt64 as uint64) = %d, %v", n, err)
	}
}

// REQ-PG-032: every limit of every rule is checked on load, also after a
// duration limit or a rule with a duration limit.
func TestREQ_PG_032_EveryLimitOfARuleIsChecked(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  META-BACKDATED: {max_age: 12h}\n  REF-COUNT: {max_refs: 7}\n",
	})
	if d, err := c.Rule("META-BACKDATED").Duration("max_age"); err != nil || d != 12*time.Hour {
		t.Fatalf("max_age=%v %v", d, err)
	}
	if n, err := c.Rule("REF-COUNT").Int("max_refs"); err != nil || n != 7 {
		t.Fatalf("max_refs=%d %v", n, err)
	}
	// Limits are a map, so repeat the load to cover both iteration orders.
	for i := 0; i < 32; i++ {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall,
			"repos/demo/warden.yaml": repoOK + "rules:\n  RATE-LIMIT: {window: 1h, max_pushes: -1}\n"})
		if err == nil {
			t.Fatalf("run %d: negative max_pushes next to a duration loads", i)
		}
	}
}

// limitBound is one row of the "Limit bounds" table in
// docs/push-guard-rules.md, written out independently of the code.
type limitBound struct {
	rule, key string
	lo, hi    string // YAML values at the bounds
	below     string // just below lo; "" when the negative-limit check covers it
	above     string // just above hi
}

var limitBounds = []limitBound{
	{"REF-COUNT", "max_refs", "1", "1000", "0", "1001"},
	{"CONTENT-BLOB", "min_base64", "20", "1000000", "19", "1000001"},
	{"CONTENT-BLOB", "min_hex", "20", "1000000", "19", "1000001"},
	{"META-UNSIGNED", "lookback", "0", "1000", "-1", "1001"},
	{"META-BACKDATED", "max_age", "'1m'", "'8760h'", "'59s'", "'8760h0m1s'"},
	{"META-FUTURE", "max_skew", "'0s'", "'24h'", "'-1s'", "'24h0m1s'"},
	{"SIZE-LIMIT", "max_files", "1", "10000000", "0", "10000001"},
	{"SIZE-LIMIT", "max_lines", "1", "10000000", "0", "10000001"},
	{"SIZE-LARGE", "max_files", "1", "10000000", "0", "10000001"},
	{"SIZE-LARGE", "max_lines", "1", "10000000", "0", "10000001"},
	{"SIZE-LARGE", "max_commits", "1", "10000000", "0", "10000001"},
	{"SIZE-LIMIT", "max_bytes", "1", "10737418240", "0", "10737418241"},
	{"SIZE-LIMIT", "max_file_bytes", "1", "10737418240", "0", "10737418241"},
	{"SIZE-LARGE", "max_file_bytes", "1", "10737418240", "0", "10737418241"},
	{"SIZE-MASS-DELETE", "max_deleted_files", "1", "10000000", "0", "10000001"},
	{"SIZE-MASS-DELETE", "max_deleted_share", "1", "100", "0", "101"},
	{"SIZE-MASS-DELETE", "min_lines", "0", "1000000", "-1", "1000001"},
	{"RATE-LIMIT", "max_pushes", "1", "10000", "0", "10001"},
	{"RATE-LIMIT", "window", "'1m'", "'168h'", "'59s'", "'168h0m1s'"},
	{"RATE-YELLOW-STREAK", "max_yellow", "1", "1000", "0", "1001"},
	{"RATE-YELLOW-STREAK", "window", "'1m'", "'720h'", "'59s'", "'720h0m1s'"},
}

// REQ-PG-046: every configurable limit loads at its minimum and maximum and
// is refused just outside, in the wall and in a repo layer. The error names
// rule, key and range; nothing is clamped.
func TestREQ_PG_046_LimitBounds(t *testing.T) {
	for _, b := range limitBounds {
		for _, layer := range []string{"wall", "repo"} {
			files := func(v string) map[string]string {
				rule := "rules:\n  " + b.rule + ": {" + b.key + ": " + v + "}\n"
				if layer == "wall" {
					return map[string]string{"defaults.yaml": wall + rule, "repos/demo/warden.yaml": repoOK}
				}
				return map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": repoOK + rule}
			}
			name := b.rule + "/" + b.key + "/" + layer
			t.Run(name, func(t *testing.T) {
				for _, v := range []string{b.lo, b.hi} {
					if _, err := loadRepo(t, files(v)); err != nil {
						t.Errorf("%s = %s refused: %v", b.key, v, err)
					}
				}
				for _, v := range []string{b.below, b.above} {
					c, err := loadRepo(t, files(v))
					if err == nil {
						r := c.Rule(b.rule)
						t.Errorf("%s = %s loads (as %v); want refused, never clamped", b.key, v, r.Limits[b.key])
						continue
					}
					if v == b.below && (strings.HasPrefix(v, "-") || strings.HasPrefix(v, "'-")) {
						continue // the negative-limit check words it differently
					}
					msg := err.Error()
					if !strings.Contains(msg, b.rule) || !strings.Contains(msg, b.key) || !strings.Contains(msg, "range") ||
						!strings.Contains(msg, strings.Trim(b.lo, "'")) {
						t.Errorf("%s = %s: err %q must name rule, key and range", b.key, v, msg)
					}
				}
			})
		}
	}
}

// REQ-PG-047: timeout from 1s to 1h.
func TestREQ_PG_047_TimeoutBounds(t *testing.T) {
	for v, want := range map[string]time.Duration{"1s": time.Second, "1h": time.Hour} {
		c, err := loadRepo(t, map[string]string{"defaults.yaml": wall + "timeout: " + v + "\n", "repos/demo/warden.yaml": repoOK})
		if err != nil || c.Timeout != want {
			t.Errorf("timeout %s: %v, %v; want %v", v, c, err, want)
		}
	}
	for _, v := range []string{"999ms", "1h0m1s", "1ns"} {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall + "timeout: " + v + "\n", "repos/demo/warden.yaml": repoOK})
		if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "1s to 1h") {
			t.Errorf("timeout %s: err = %v; want refused naming the range 1s to 1h", v, err)
		}
	}
}

// REQ-PG-004: a rule compiled without one of its required limits fails at
// load, naming the rule and the key. Omitting the key in a repo file keeps
// the built-in value, and that value is stored on the rule.
func TestREQ_PG_004_MissingLimitFailsClosed(t *testing.T) {
	for id, decls := range ruleLimits {
		for _, drop := range decls {
			limits := map[string]any{}
			for _, d := range decls {
				if d.key == drop.key {
					continue
				}
				if d.duration {
					limits[d.key] = time.Duration(d.lo).String()
				} else {
					limits[d.key] = int(d.lo)
				}
			}
			_, err := (&mergedRule{enabled: true, color: "yellow", limits: limits}).compile(id)
			if err == nil || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), drop.key) || !strings.Contains(err.Error(), "not set") {
				t.Errorf("%s without %s: %v", id, drop.key, err)
			}
		}
	}
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall,
		"repos/demo/warden.yaml": repoOK + "rules:\n  REF-COUNT: {color: red}\n",
	})
	if got := c.Rule("REF-COUNT").MaxRefs; got != 10 {
		t.Errorf("repo layer omitted max_refs: MaxRefs = %d, want 10", got)
	}
	rate := c.Rule("RATE-LIMIT")
	if rate.MaxPushes != 30 || rate.Window != time.Hour {
		t.Errorf("RATE-LIMIT parsed limits = pushes %d window %s", rate.MaxPushes, rate.Window)
	}
}
