// Package config loads the Push Guard configuration from the wall.
//
// Three layers are merged in order: the built-in defaults (defaults.yaml in
// this package, embedded), the wall's <dir>/defaults.yaml and the repo's
// <dir>/repos/<name>/warden.yaml. Scalars and limits override; the rule lists
// match, allow and deny are appended to; match_remove, allow_remove and
// deny_remove remove exact entries of the layers below.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed defaults.yaml
var builtinDefaults []byte

// Color is the level of a finding.
type Color string

const (
	Red    Color = "red"
	Yellow Color = "yellow"
	Green  Color = "green"
)

// Worse reports whether c is worse than other (red > yellow > green).
func (c Color) Worse(other Color) bool { return rank(c) > rank(other) }

func rank(c Color) int {
	switch c {
	case Red:
		return 2
	case Yellow:
		return 1
	}
	return 0
}

var repoNameRE = regexp.MustCompile(`^[a-z0-9._-]+$`)

// ValidRepoName reports whether name may be used as a repo name.
func ValidRepoName(name string) bool {
	return repoNameRE.MatchString(name) && !strings.HasPrefix(name, ".")
}

// ErrUnknownRepo is returned when the repo has no folder on the wall.
var ErrUnknownRepo = errors.New("unknown repo")

// Config is the merged configuration for one repo (or the wall only, when
// loaded with Load).
type Config struct {
	Dir      string // absolute config directory
	RepoName string // empty for the wall-only config

	AgentName      string
	AgentTokenFile string
	NotifyCommand  []string
	StateDir       string
	PagesBranch    string
	Gitleaks       string
	Timeout        time.Duration
	ForwardAtomic  bool

	Remote             string
	Credential         string
	CredentialUsername string // user name sent with an HTTPS token; empty: gitx.DefaultTokenUser
	KnownHosts         string // optional known_hosts file for SSH remotes
	DefaultBranch      string

	Rules map[string]*Rule
}

// Rule is one rule with its merged settings.
//
// Count and duration fields are the limits this rule reads, parsed and
// range-checked at load. Zero is a real value: lookback, min_lines and
// max_skew may be zero. A rule that does not declare a limit leaves that
// field zero.
type Rule struct {
	ID      string
	Enabled bool
	Color   Color
	Match   []*regexp.Regexp
	Allow   []*regexp.Regexp
	Deny    []*regexp.Regexp
	Limits  map[string]any // raw merged values; Int and Duration read these

	MaxRefs         int64
	MinBase64       int64
	MinHex          int64
	Lookback        int64
	MaxFiles        int64
	MaxLines        int64
	MaxBytes        int64
	MaxFileBytes    int64
	MaxCommits      int64
	MaxDeletedFiles int64
	MaxDeletedShare int64
	MinLines        int64
	MaxPushes       int64
	MaxYellow       int64
	MaxAge          time.Duration
	MaxSkew         time.Duration
	Window          time.Duration
}

// RepoDir is the repo's folder on the wall.
func (c *Config) RepoDir() string { return filepath.Join(c.Dir, "repos", c.RepoName) }

// Rule returns the rule with the given ID. Unknown IDs return a disabled rule;
// IDs in config files are validated on load.
func (c *Config) Rule(id string) *Rule {
	if r, ok := c.Rules[id]; ok {
		return r
	}
	return &Rule{ID: id}
}

// Fires decides one subject: deny always fires, otherwise the rule fires when
// triggered and no allow entry matches. A disabled rule never fires.
func (r *Rule) Fires(subject string, triggered bool) bool {
	if !r.Enabled {
		return false
	}
	if matchAny(r.Deny, subject) {
		return true
	}
	return triggered && !matchAny(r.Allow, subject)
}

// Allowed reports whether subject is exempted by allow (and not kept in by
// deny). Used to decide whether a force-with-lease is permitted.
func (r *Rule) Allowed(subject string) bool {
	return r.Enabled && matchAny(r.Allow, subject) && !matchAny(r.Deny, subject)
}

// Matches reports whether subject matches the rule's match list.
func (r *Rule) Matches(subject string) bool { return matchAny(r.Match, subject) }

func matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// Int returns an integer limit. Values outside int64 or below zero are
// refused: YAML may yield a uint64 bigger than MaxInt64, and a negative
// count is never meaningful for the rules that read limits.
func (r *Rule) Int(key string) (int64, error) {
	v, ok := r.Limits[key]
	if !ok {
		return 0, fmt.Errorf("rule %s: limit %s not set", r.ID, key)
	}
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case uint64:
		if x > uint64(math.MaxInt64) {
			return 0, fmt.Errorf("rule %s: limit %s is out of range: %v", r.ID, key, v)
		}
		n = int64(x)
	case float64:
		// float64(MaxInt64) rounds up to 2^63, which is already out of range.
		if x >= float64(math.MaxInt64) || x < float64(math.MinInt64) {
			return 0, fmt.Errorf("rule %s: limit %s is out of range: %v", r.ID, key, v)
		}
		if x != float64(int64(x)) {
			return 0, fmt.Errorf("rule %s: limit %s is not an integer: %v", r.ID, key, v)
		}
		n = int64(x)
	default:
		return 0, fmt.Errorf("rule %s: limit %s is not an integer: %v", r.ID, key, v)
	}
	if n < 0 {
		return 0, fmt.Errorf("rule %s: limit %s must not be negative: %d", r.ID, key, n)
	}
	return n, nil
}

// Duration returns a duration limit (e.g. 24h, 10m). Negative durations are
// refused: they are never meaningful for max_age / max_skew / window (a
// negative max_age would make META-BACKDATED fire on every commit). Zero is
// allowed, matching count limits (Int), as the strictest finite bound.
func (r *Rule) Duration(key string) (time.Duration, error) {
	v, ok := r.Limits[key]
	if !ok {
		return 0, fmt.Errorf("rule %s: limit %s not set", r.ID, key)
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("rule %s: limit %s is not a duration: %v", r.ID, key, v)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("rule %s: limit %s: %w", r.ID, key, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("rule %s: limit %s must not be negative: %s", r.ID, key, d)
	}
	return d, nil
}

// Compile anchors a pattern as ^(?:…)$ so it always matches the whole subject.
func Compile(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + pattern + `)$`)
}

// ---- raw layers ----

type rawRule struct {
	Enabled     *bool          `yaml:"enabled"`
	Color       string         `yaml:"color"`
	Match       []string       `yaml:"match"`
	Allow       []string       `yaml:"allow"`
	Deny        []string       `yaml:"deny"`
	MatchRemove []string       `yaml:"match_remove"`
	AllowRemove []string       `yaml:"allow_remove"`
	DenyRemove  []string       `yaml:"deny_remove"`
	Limits      map[string]any `yaml:",inline"`
}

type rawLayer struct {
	Agent *struct {
		Name      *string `yaml:"name"`
		TokenFile *string `yaml:"token_file"`
	} `yaml:"agent"`
	Notify *struct {
		Command []string `yaml:"command"`
	} `yaml:"notify"`
	StateDir    *string `yaml:"state_dir"`
	PagesBranch *string `yaml:"pages_branch"`
	Gitleaks    *struct {
		Path *string `yaml:"path"`
	} `yaml:"gitleaks"`
	Timeout *string `yaml:"timeout"`
	Forward *struct {
		Atomic *bool `yaml:"atomic"`
	} `yaml:"forward"`

	Remote             *string `yaml:"remote"`
	Credential         *string `yaml:"credential"`
	CredentialUsername *string `yaml:"credential_username"`
	KnownHosts         *string `yaml:"known_hosts"`
	DefaultBranch      *string `yaml:"default_branch"`

	Rules map[string]*rawRule `yaml:"rules"`
}

type mergedRule struct {
	enabled            bool
	color              string
	match, allow, deny []string
	limits             map[string]any
}

type merged struct {
	agentName, tokenFile, stateDir, pagesBranch, gitleaks, timeout string
	notify                                                         []string
	atomic                                                         bool
	remote, credential, credentialUser, knownHosts, defaultBranch  string
	rules                                                          map[string]*mergedRule
	order                                                          []string
}

func parseLayer(name string, data []byte) (*rawLayer, error) {
	var l rawLayer
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &l, nil
}

func (m *merged) apply(name string, l *rawLayer, repoLayer, builtin bool) error {
	if !repoLayer && (l.Remote != nil || l.Credential != nil || l.CredentialUsername != nil || l.KnownHosts != nil || l.DefaultBranch != nil) {
		return fmt.Errorf("%s: remote, credential, credential_username, known_hosts and default_branch belong in a repo's warden.yaml", name)
	}
	if repoLayer && (l.Agent != nil || l.StateDir != nil) {
		return fmt.Errorf("%s: agent and state_dir belong in the wall's defaults.yaml", name)
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	if l.Agent != nil {
		set(&m.agentName, l.Agent.Name)
		set(&m.tokenFile, l.Agent.TokenFile)
	}
	if l.Notify != nil && l.Notify.Command != nil {
		m.notify = l.Notify.Command
	}
	set(&m.stateDir, l.StateDir)
	set(&m.pagesBranch, l.PagesBranch)
	if l.Gitleaks != nil {
		set(&m.gitleaks, l.Gitleaks.Path)
	}
	set(&m.timeout, l.Timeout)
	if l.Forward != nil && l.Forward.Atomic != nil {
		m.atomic = *l.Forward.Atomic
	}
	set(&m.remote, l.Remote)
	set(&m.credential, l.Credential)
	set(&m.credentialUser, l.CredentialUsername)
	set(&m.knownHosts, l.KnownHosts)
	set(&m.defaultBranch, l.DefaultBranch)

	ids := make([]string, 0, len(l.Rules))
	for id := range l.Rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rr := l.Rules[id]
		mr, ok := m.rules[id]
		if !ok {
			if !builtin {
				return fmt.Errorf("%s: unknown rule %s", name, id)
			}
			mr = &mergedRule{enabled: true, limits: map[string]any{}}
			m.rules[id] = mr
			m.order = append(m.order, id)
		}
		if rr == nil {
			continue
		}
		if rr.Enabled != nil {
			mr.enabled = *rr.Enabled
		}
		if rr.Color != "" {
			mr.color = rr.Color
		}
		var err error
		if mr.match, err = removeAll(mr.match, rr.MatchRemove); err != nil {
			return fmt.Errorf("%s: rule %s: match_remove: %w", name, id, err)
		}
		if mr.allow, err = removeAll(mr.allow, rr.AllowRemove); err != nil {
			return fmt.Errorf("%s: rule %s: allow_remove: %w", name, id, err)
		}
		if mr.deny, err = removeAll(mr.deny, rr.DenyRemove); err != nil {
			return fmt.Errorf("%s: rule %s: deny_remove: %w", name, id, err)
		}
		mr.match = append(mr.match, rr.Match...)
		mr.allow = append(mr.allow, rr.Allow...)
		mr.deny = append(mr.deny, rr.Deny...)
		for k, v := range rr.Limits {
			if _, known := mr.limits[k]; !known && !builtin {
				return fmt.Errorf("%s: rule %s: unknown key %s", name, id, k)
			}
			mr.limits[k] = v
		}
	}
	return nil
}

func removeAll(list, remove []string) ([]string, error) {
	for _, r := range remove {
		i := indexOf(list, r)
		if i < 0 {
			return nil, fmt.Errorf("entry %q not found", r)
		}
		list = append(append([]string(nil), list[:i]...), list[i+1:]...)
	}
	return list, nil
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// Load reads the wall configuration without a repo.
func Load(dir string) (*Config, error) { return load(dir, "") }

// LoadRepo reads the merged configuration for one repo. It returns an error
// wrapping ErrUnknownRepo when the repo has no folder.
func LoadRepo(dir, repo string) (*Config, error) {
	if !ValidRepoName(repo) {
		return nil, fmt.Errorf("%w: invalid name %q", ErrUnknownRepo, repo)
	}
	return load(dir, repo)
}

// Repos lists the repo folders on the wall.
func Repos(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "repos"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidRepoName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func load(dir, repo string) (*Config, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	m := &merged{rules: map[string]*mergedRule{}}
	l, err := parseLayer("built-in defaults", builtinDefaults)
	if err != nil {
		return nil, err
	}
	if err := m.apply("built-in defaults", l, false, true); err != nil {
		return nil, err
	}
	wall := filepath.Join(abs, "defaults.yaml")
	if data, err := os.ReadFile(wall); err == nil {
		l, err := parseLayer(wall, data)
		if err != nil {
			return nil, err
		}
		if err := m.apply(wall, l, false, false); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if repo != "" {
		file := filepath.Join(abs, "repos", repo, "warden.yaml")
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownRepo, repo)
		}
		if err != nil {
			return nil, err
		}
		l, err := parseLayer(file, data)
		if err != nil {
			return nil, err
		}
		if err := m.apply(file, l, true, false); err != nil {
			return nil, err
		}
	}
	return m.build(abs, repo)
}

func (m *merged) build(dir, repo string) (*Config, error) {
	c := &Config{
		Dir:                dir,
		RepoName:           repo,
		AgentName:          m.agentName,
		AgentTokenFile:     resolve(dir, m.tokenFile),
		NotifyCommand:      m.notify,
		StateDir:           resolve(dir, m.stateDir),
		PagesBranch:        m.pagesBranch,
		Gitleaks:           ResolveProgram(dir, m.gitleaks),
		ForwardAtomic:      m.atomic,
		Remote:             m.remote,
		Credential:         resolve(dir, m.credential),
		CredentialUsername: m.credentialUser,
		KnownHosts:         resolve(dir, m.knownHosts),
		DefaultBranch:      strings.TrimPrefix(m.defaultBranch, "refs/heads/"),
		Rules:              map[string]*Rule{},
	}
	if c.AgentName == "" {
		return nil, errors.New("agent.name is required in defaults.yaml")
	}
	if c.StateDir == "" {
		c.StateDir = filepath.Join(dir, "state")
	}
	t, err := time.ParseDuration(m.timeout)
	if err != nil {
		return nil, fmt.Errorf("timeout: invalid duration %q", m.timeout)
	}
	if t < MinTimeout || t > MaxTimeout {
		return nil, fmt.Errorf("timeout %s is out of range %s to %s", m.timeout, fmtDur(MinTimeout), fmtDur(MaxTimeout))
	}
	c.Timeout = t
	if repo != "" {
		if m.remote == "" {
			return nil, fmt.Errorf("repos/%s/warden.yaml: remote is required", repo)
		}
		if err := CheckRemote(m.remote, m.credential, m.credentialUser, m.knownHosts); err != nil {
			return nil, fmt.Errorf("repos/%s/warden.yaml: %w", repo, err)
		}
	}
	for _, id := range m.order {
		r, err := m.rules[id].compile(id)
		if err != nil {
			return nil, err
		}
		c.Rules[id] = r
	}
	return c, nil
}

func (mr *mergedRule) compile(id string) (*Rule, error) {
	r := &Rule{ID: id, Enabled: mr.enabled, Color: Color(mr.color), Limits: mr.limits}
	if r.Color != Red && r.Color != Yellow {
		return nil, fmt.Errorf("rule %s: color must be red or yellow, got %q", id, mr.color)
	}
	lists := []struct {
		src []string
		dst *[]*regexp.Regexp
		key string
	}{{mr.match, &r.Match, "match"}, {mr.allow, &r.Allow, "allow"}, {mr.deny, &r.Deny, "deny"}}
	for _, l := range lists {
		for _, p := range l.src {
			re, err := Compile(p)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %s %q: %w", id, l.key, p, err)
			}
			*l.dst = append(*l.dst, re)
		}
	}
	if err := r.bindLimits(); err != nil {
		return nil, err
	}
	return r, nil
}

// Timeout bounds (REQ-PG-047).
const (
	MinTimeout = time.Second
	MaxTimeout = time.Hour
)

// limitDecl is one limit a rule reads (REQ-PG-046). Durations are in
// nanoseconds. The same list is the range check and the set of required keys.
type limitDecl struct {
	key      string
	duration bool
	lo, hi   int64
}

const gib10 = 10 << 30

// ruleLimits is the single declaration of the limits each rule requires.
var ruleLimits = map[string][]limitDecl{
	"REF-COUNT":          {{key: "max_refs", lo: 1, hi: 1000}},
	"CONTENT-BLOB":       {{key: "min_base64", lo: 20, hi: 1_000_000}, {key: "min_hex", lo: 20, hi: 1_000_000}},
	"META-UNSIGNED":      {{key: "lookback", lo: 0, hi: 1000}},
	"META-BACKDATED":     {{key: "max_age", duration: true, lo: int64(time.Minute), hi: int64(8760 * time.Hour)}},
	"META-FUTURE":        {{key: "max_skew", duration: true, lo: 0, hi: int64(24 * time.Hour)}},
	"SIZE-LIMIT":         {{key: "max_files", lo: 1, hi: 10_000_000}, {key: "max_lines", lo: 1, hi: 10_000_000}, {key: "max_bytes", lo: 1, hi: gib10}, {key: "max_file_bytes", lo: 1, hi: gib10}},
	"SIZE-LARGE":         {{key: "max_files", lo: 1, hi: 10_000_000}, {key: "max_lines", lo: 1, hi: 10_000_000}, {key: "max_commits", lo: 1, hi: 10_000_000}, {key: "max_file_bytes", lo: 1, hi: gib10}},
	"SIZE-MASS-DELETE":   {{key: "max_deleted_files", lo: 1, hi: 10_000_000}, {key: "max_deleted_share", lo: 1, hi: 100}, {key: "min_lines", lo: 0, hi: 1_000_000}},
	"RATE-LIMIT":         {{key: "max_pushes", lo: 1, hi: 10_000}, {key: "window", duration: true, lo: int64(time.Minute), hi: int64(168 * time.Hour)}},
	"RATE-YELLOW-STREAK": {{key: "max_yellow", lo: 1, hi: 1000}, {key: "window", duration: true, lo: int64(time.Minute), hi: int64(720 * time.Hour)}},
}

// bindLimits parses every limit this rule declares, checks its range, and
// stores it on the rule. A missing required limit, an unknown limit key, or
// a value outside its range is an error. It never clamps.
func (r *Rule) bindLimits() error {
	decls := ruleLimits[r.ID]
	want := map[string]limitDecl{}
	for _, d := range decls {
		want[d.key] = d
	}
	keys := make([]string, 0, len(r.Limits))
	for k := range r.Limits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d, ok := want[k]
		if !ok {
			return fmt.Errorf("rule %s: limit %s is not used by this rule", r.ID, k)
		}
		if d.duration {
			v, err := r.Duration(k)
			if err != nil {
				return err
			}
			if int64(v) < d.lo || int64(v) > d.hi {
				return fmt.Errorf("rule %s: limit %s %s is out of range %s to %s", r.ID, k, v, fmtDur(time.Duration(d.lo)), fmtDur(time.Duration(d.hi)))
			}
			r.setDuration(k, v)
		} else {
			n, err := r.Int(k)
			if err != nil {
				return err
			}
			if n < d.lo || n > d.hi {
				return fmt.Errorf("rule %s: limit %s %d is out of range %d to %d", r.ID, k, n, d.lo, d.hi)
			}
			r.setCount(k, n)
		}
		delete(want, k)
	}
	for _, d := range decls {
		if _, ok := want[d.key]; ok {
			return fmt.Errorf("rule %s: limit %s not set", r.ID, d.key)
		}
	}
	return nil
}

func (r *Rule) setCount(key string, n int64) {
	switch key {
	case "max_refs":
		r.MaxRefs = n
	case "min_base64":
		r.MinBase64 = n
	case "min_hex":
		r.MinHex = n
	case "lookback":
		r.Lookback = n
	case "max_files":
		r.MaxFiles = n
	case "max_lines":
		r.MaxLines = n
	case "max_bytes":
		r.MaxBytes = n
	case "max_file_bytes":
		r.MaxFileBytes = n
	case "max_commits":
		r.MaxCommits = n
	case "max_deleted_files":
		r.MaxDeletedFiles = n
	case "max_deleted_share":
		r.MaxDeletedShare = n
	case "min_lines":
		r.MinLines = n
	case "max_pushes":
		r.MaxPushes = n
	case "max_yellow":
		r.MaxYellow = n
	}
}

func (r *Rule) setDuration(key string, d time.Duration) {
	switch key {
	case "max_age":
		r.MaxAge = d
	case "max_skew":
		r.MaxSkew = d
	case "window":
		r.Window = d
	}
}

// fmtDur prints a duration the way the docs write it: 1m, 24h, 8760h, 0s.
func fmtDur(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func resolve(dir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// ResolveProgram resolves an external program setting (gitleaks.path,
// everref.path): a bare name is looked up on PATH later, anything with a
// slash is a path, relative to the configuration directory.
func ResolveProgram(dir, p string) string {
	if p == "" || filepath.Base(p) == p {
		return p
	}
	return resolve(dir, p)
}

// CheckRemote applies the rules both guards share for a repo's remote and
// its credential settings:
//
//   - remote: an SSH remote (ssh:// or user@host:path), an https:// or
//     http:// URL, or a local repository as an absolute path or file:// URL;
//   - credential: required for SSH (a private key) and HTTPS (a token file),
//     not allowed for a local remote;
//   - credential_username: HTTPS only;
//   - known_hosts: SSH only.
func CheckRemote(remote, credential, credentialUser, knownHosts string) error {
	kind, err := RemoteKind(remote)
	if err != nil {
		return err
	}
	switch {
	case kind == KindLocal && !filepath.IsAbs(remote) && !strings.HasPrefix(remote, "file://"):
		return fmt.Errorf("a local remote must be an absolute path or a file:// URL, not %s", remote)
	case kind == KindSSH && credential == "":
		return fmt.Errorf("remote %s needs a credential (an SSH private key file)", remote)
	case kind == KindHTTPS && credential == "":
		return fmt.Errorf("remote %s needs a credential (a token file)", remote)
	case kind == KindLocal && credential != "":
		return fmt.Errorf("a local remote takes no credential")
	case credentialUser != "" && kind != KindHTTPS:
		return fmt.Errorf("credential_username only applies to https:// and http:// remotes")
	case knownHosts != "" && kind != KindSSH:
		return fmt.Errorf("known_hosts only applies to SSH remotes")
	}
	if credentialUser != "" && !validUser.MatchString(credentialUser) {
		return fmt.Errorf("credential_username %q: only letters, digits and . _ + @ - are allowed", credentialUser)
	}
	return nil
}

var validUser = regexp.MustCompile(`^[A-Za-z0-9._+@-]{1,128}$`)

// Kind classifies a remote by how the guard authenticates to it.
type Kind int

const (
	KindLocal Kind = iota // local path or file://
	KindSSH               // ssh:// or scp-style [user@]host:path
	KindHTTPS             // https:// or http://
)

var scpLike = regexp.MustCompile(`^(?:[^@/]+@)?[^/:]+:`)

// RemoteKind classifies remote.
func RemoteKind(remote string) (Kind, error) {
	switch {
	case strings.HasPrefix(remote, "file://"), strings.HasPrefix(remote, "/"),
		strings.HasPrefix(remote, "./"), strings.HasPrefix(remote, "../"):
		return KindLocal, nil
	case strings.HasPrefix(remote, "ssh://"), strings.HasPrefix(remote, "git+ssh://"), strings.HasPrefix(remote, "ssh+git://"):
		return KindSSH, nil
	case strings.HasPrefix(remote, "https://"), strings.HasPrefix(remote, "http://"):
		return KindHTTPS, nil
	case strings.Contains(remote, "://"):
		return 0, fmt.Errorf("unsupported remote URL scheme: %s", remote)
	case scpLike.MatchString(remote):
		return KindSSH, nil
	}
	return KindLocal, nil
}
