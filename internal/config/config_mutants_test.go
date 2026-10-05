package config

// Mutation-kill tests for internal/config (gomutants survivors). Kept in a
// separate file so QA fuzz/rapid work does not collide.

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMutantsColorRankUnknownTiedWithGreen(t *testing.T) {
	unknown := Color("purple")
	if Green.Worse(unknown) || unknown.Worse(Green) {
		t.Fatalf("green and unknown must rank equal: %v %v", Green.Worse(unknown), unknown.Worse(Green))
	}
	if !Red.Worse(Yellow) || !Yellow.Worse(Green) || !Red.Worse(Green) {
		t.Fatal("red > yellow > green broken")
	}
}

func TestMutantsFiresAllowAndTrigger(t *testing.T) {
	allow := regexp.MustCompile(`^(?:refs/heads/ok)$`)
	deny := regexp.MustCompile(`^(?:refs/heads/no)$`)
	r := &Rule{Enabled: true, Allow: []*regexp.Regexp{allow}, Deny: []*regexp.Regexp{deny}}
	if r.Fires("refs/heads/ok", true) {
		t.Fatal("allowed+triggered must not fire")
	}
	if !r.Fires("refs/heads/other", true) {
		t.Fatal("non-allowed+triggered must fire")
	}
	if r.Fires("refs/heads/other", false) {
		t.Fatal("non-trigger must not fire without deny")
	}
	if !r.Fires("refs/heads/no", false) {
		t.Fatal("deny must fire without trigger")
	}
}

func TestMutantsAllowedRequiresEnabled(t *testing.T) {
	allow := regexp.MustCompile(`^(?:refs/heads/ok)$`)
	r := &Rule{Enabled: false, Allow: []*regexp.Regexp{allow}}
	if r.Allowed("refs/heads/ok") {
		t.Fatal("disabled rule must not be Allowed")
	}
	r.Enabled = true
	if !r.Allowed("refs/heads/ok") {
		t.Fatal("enabled+allow must be Allowed")
	}
}

func TestMutantsIntDurationErrorReturns(t *testing.T) {
	r := &Rule{ID: "R", Limits: map[string]any{}}
	if n, err := r.Int("missing"); err == nil || n != 0 {
		t.Fatalf("Int missing: %d %v", n, err)
	}
	if d, err := r.Duration("missing"); err == nil || d != 0 {
		t.Fatalf("Duration missing: %v %v", d, err)
	}
	r.Limits["bad"] = struct{}{}
	if n, err := r.Int("bad"); err == nil || n != 0 {
		t.Fatalf("Int non-int: %d %v", n, err)
	}
	r.Limits["bad"] = 42
	if d, err := r.Duration("bad"); err == nil || d != 0 {
		t.Fatalf("Duration non-string: %v %v", d, err)
	}
	r.Limits["skew"] = "not-a-duration"
	d, err := r.Duration("skew")
	if err == nil || d != 0 {
		t.Fatalf("Duration parse: %v %v", d, err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("Duration parse must wrap ParseDuration cause (%w)")
	}
	r.Limits["neg"] = "-1s"
	if d, err := r.Duration("neg"); err == nil || d != 0 || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("Duration negative: %v %v", d, err)
	}
	r.Limits["big"] = uint64(1 << 63)
	if n, err := r.Int("big"); err == nil || n != 0 || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("Int uint64 overflow: %d %v", n, err)
	}
	r.Limits["negc"] = int64(-1)
	if n, err := r.Int("negc"); err == nil || n != 0 || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("Int negative: %d %v", n, err)
	}
	r.Limits["hif"] = float64(1e19)
	if n, err := r.Int("hif"); err == nil || n != 0 {
		t.Fatalf("Int float high: %d %v", n, err)
	}
	r.Limits["lof"] = float64(-1e19)
	if n, err := r.Int("lof"); err == nil || n != 0 {
		t.Fatalf("Int float low: %d %v", n, err)
	}
}

func TestMutantsMergeSideEffects(t *testing.T) {
	defaults := "agent:\n  name: test-agent\n  token_file: tokens/agent\n" +
		"notify:\n  command: [/bin/true, notify]\n" +
		"state_dir: var/state\n" +
		"gitleaks:\n  path: bin/gitleaks\n" +
		"forward:\n  atomic: false\n"
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          defaults,
		"repos/demo/warden.yaml": "remote: /srv/git/demo.git\ndefault_branch: main\n",
	})
	if !strings.HasSuffix(c.AgentTokenFile, filepath.Join("tokens", "agent")) {
		t.Fatalf("token_file: %q", c.AgentTokenFile)
	}
	if len(c.NotifyCommand) != 2 || c.NotifyCommand[0] != "/bin/true" {
		t.Fatalf("notify: %v", c.NotifyCommand)
	}
	if !strings.HasSuffix(c.StateDir, filepath.Join("var", "state")) {
		t.Fatalf("state_dir: %q", c.StateDir)
	}
	if !strings.HasSuffix(c.Gitleaks, filepath.Join("bin", "gitleaks")) {
		t.Fatalf("gitleaks: %q", c.Gitleaks)
	}
	if c.ForwardAtomic {
		t.Fatal("atomic false not applied")
	}
	if c.DefaultBranch != "main" {
		t.Fatalf("default_branch: %q", c.DefaultBranch)
	}
}

func TestMutantsNullRuleEntryContinue(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall + "rules:\n  REF-DELETE: {allow: ['refs/heads/agent.*']}\n",
		"repos/demo/warden.yaml": "remote: /srv/git/x.git\nrules:\n  REF-DELETE:\n  REF-COUNT: {max_refs: 2}\n",
	})
	if !c.Rule("REF-DELETE").Allowed("refs/heads/agent-1") {
		t.Fatal("null rule entry cleared prior allow (continue→break)")
	}
	if n, _ := c.Rule("REF-COUNT").Int("max_refs"); n != 2 {
		t.Fatalf("later rule skipped: %d", n)
	}
}

func TestMutantsMatchRemoveOnlyNamed(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall + "rules:\n  PATH-YELLOW:\n    match: ['a\\.txt', 'b\\.txt', 'c\\.txt']\n",
		"repos/demo/warden.yaml": "remote: /srv/git/x.git\nrules:\n  PATH-YELLOW:\n    match_remove: ['b\\.txt']\n",
	})
	r := c.Rule("PATH-YELLOW")
	if !r.Matches("a.txt") || r.Matches("b.txt") || !r.Matches("c.txt") {
		t.Fatalf("match_remove wrong: a=%v b=%v c=%v", r.Matches("a.txt"), r.Matches("b.txt"), r.Matches("c.txt"))
	}
}

func TestMutantsErrUnknownRepoUnwraps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "defaults.yaml"), []byte(wall), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRepo(dir, "no-such-repo")
	if !errors.Is(err, ErrUnknownRepo) {
		t.Fatalf("missing: %v", err)
	}
	_, err = LoadRepo(dir, "Bad Name")
	if !errors.Is(err, ErrUnknownRepo) {
		t.Fatalf("invalid name: %v", err)
	}
}

func TestMutantsStateDirTimeoutDefaults(t *testing.T) {
	c := mustLoad(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /srv/git/x.git\n"})
	if c.StateDir != filepath.Join(c.Dir, "state") {
		t.Fatalf("StateDir=%q", c.StateDir)
	}
	if c.Timeout != 60*time.Second {
		t.Fatalf("Timeout=%v", c.Timeout)
	}
}

func TestMutantsResolveAndResolveProgram(t *testing.T) {
	dir := "/cfg"
	if got := resolve(dir, ""); got != "" {
		t.Fatalf("empty=%q", got)
	}
	if got := resolve(dir, "/abs"); got != "/abs" {
		t.Fatalf("abs=%q", got)
	}
	if got := resolve(dir, "rel"); got != filepath.Join(dir, "rel") {
		t.Fatalf("rel=%q", got)
	}
	if got := ResolveProgram(dir, ""); got != "" {
		t.Fatalf("prog empty=%q", got)
	}
	if got := ResolveProgram(dir, "gitleaks"); got != "gitleaks" {
		t.Fatalf("bare=%q", got)
	}
	if got := ResolveProgram(dir, "bin/gitleaks"); got != filepath.Join(dir, "bin", "gitleaks") {
		t.Fatalf("rel path=%q", got)
	}
	if got := ResolveProgram(dir, "/usr/bin/gitleaks"); got != "/usr/bin/gitleaks" {
		t.Fatalf("abs prog=%q", got)
	}
}

func TestMutantsCheckRemoteAndRemoteKind(t *testing.T) {
	if err := CheckRemote("https://h/o/r.git", "tok", "bad user", ""); err == nil || !strings.Contains(err.Error(), "credential_username") {
		t.Fatalf("bad user: %v", err)
	}
	if err := CheckRemote("https://h/o/r.git", "tok", "ok_user", ""); err != nil {
		t.Fatalf("good user: %v", err)
	}
	if err := CheckRemote("git://host/r.git", "", "", ""); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("kind err: %v", err)
	}
	for in, want := range map[string]Kind{
		"./rel":                 KindLocal,
		"../rel":                KindLocal,
		"git+ssh://host/o/r":    KindSSH,
		"ssh+git://host/o/r":    KindSSH,
		"http://host/o/r.git":   KindHTTPS,
		"just-a-name":           KindLocal,
		"/srv/git/r.git":        KindLocal,
		"file:///srv/git/r.git": KindLocal,
	} {
		got, err := RemoteKind(in)
		if err != nil || got != want {
			t.Errorf("%s: %v %v want %v", in, got, err, want)
		}
	}
	if _, err := RemoteKind("ftp://host/x"); err == nil {
		t.Fatal("ftp accepted")
	}
}

func TestMutantsWallRejectsRepoRemoteKeys(t *testing.T) {
	for _, key := range []string{"remote: /x.git", "credential: k", "credential_username: u", "known_hosts: kh", "default_branch: main"} {
		_, err := loadRepo(t, map[string]string{"defaults.yaml": wall + key + "\n", "repos/demo/warden.yaml": "remote: /srv/git/x.git\n"})
		if err == nil || !strings.Contains(err.Error(), "belong in a repo") {
			t.Errorf("%s: %v", key, err)
		}
	}
	_, err := loadRepo(t, map[string]string{"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /srv/git/x.git\nstate_dir: x\n"})
	if err == nil || !strings.Contains(err.Error(), "belong in the wall") {
		t.Errorf("state_dir in repo: %v", err)
	}
}

func TestMutantsRemoveErrorWraps(t *testing.T) {
	_, err := loadRepo(t, map[string]string{
		"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  PATH-RED: {match_remove: ['nope']}\n",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("%v", err)
	}
}

func TestMutantsParseLayerWraps(t *testing.T) {
	_, err := loadRepo(t, map[string]string{"defaults.yaml": "agent: {\n", "repos/demo/warden.yaml": "remote: /x.git\n"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestMutantsDurationLimitContinue(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"defaults.yaml":          wall,
		"repos/demo/warden.yaml": "remote: /x.git\nrules:\n  META-BACKDATED: {max_age: 12h}\n  REF-COUNT: {max_refs: 7}\n",
	})
	if d, err := c.Rule("META-BACKDATED").Duration("max_age"); err != nil || d != 12*time.Hour {
		t.Fatalf("max_age=%v %v", d, err)
	}
	if n, err := c.Rule("REF-COUNT").Int("max_refs"); err != nil || n != 7 {
		t.Fatalf("max_refs=%d %v", n, err)
	}
}

func TestMutantsCheckRemoteWraps(t *testing.T) {
	_, err := loadRepo(t, map[string]string{
		"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: ftp://h/x.git\n",
	})
	if err == nil || !strings.Contains(err.Error(), "ftp") {
		t.Fatalf("%v", err)
	}
}

func TestMutantsCompileBadRegexWraps(t *testing.T) {
	_, err := loadRepo(t, map[string]string{
		"defaults.yaml": wall, "repos/demo/warden.yaml": "remote: /x.git\nrules:\n  REF-DELETE: {allow: ['(']}\n",
	})
	if err == nil || !strings.Contains(err.Error(), "REF-DELETE") {
		t.Fatalf("%v", err)
	}
}
