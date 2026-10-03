// Package pullguard is the Pull Guard of Git Warden: it keeps an append-only
// backup of every branch and tag of a remote by running git-everref.
// Git Warden contains no everref code; everref is an external program that
// must be installed on the host (like gitleaks for the Push Guard).
package pullguard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pihme/git-warden/internal/config"
)

// Built-in defaults. Everything else must be configured.
const (
	DefaultEverref = "git-everref"
	DefaultTimeout = 30 * time.Minute
	DefaultsFile   = "defaults.yaml"
	RepoFile       = "pull.yaml"
)

// ErrUnknownRepo is returned for a repo without repos/<name>/pull.yaml.
var ErrUnknownRepo = errors.New("unknown repo")

// Defaults is defaults.yaml of a Pull Guard configuration directory.
type Defaults struct {
	Dir            string        // configuration directory
	Everref        string        // everref binary: a path or a name looked up on PATH
	EverrefVersion string        // if set, everref --version must report exactly this
	NotifyCommand  []string      // gets a warning as JSON on stdin when a run fails
	StateDir       string        // bridge clones, backup repositories, pull.jsonl
	Timeout        time.Duration // per repo and run
}

// Repo is repos/<name>/pull.yaml.
type Repo struct {
	Name            string
	Remote          string           // the remote to back up (read access is enough)
	Credential      string           // read-only SSH key or token file; none for local remotes
	KnownHosts      string           // SSH only
	ExcludeBranches []*regexp.Regexp // anchored; matched against the branch name without refs/heads/
}

type rawDefaults struct {
	Everref struct {
		Path    *string `yaml:"path"`
		Version *string `yaml:"version"`
	} `yaml:"everref"`
	Notify struct {
		Command []string `yaml:"command"`
	} `yaml:"notify"`
	StateDir *string `yaml:"state_dir"`
	Timeout  *string `yaml:"timeout"`
}

type rawRepo struct {
	Remote          string   `yaml:"remote"`
	Credential      string   `yaml:"credential"`
	KnownHosts      string   `yaml:"known_hosts"`
	ExcludeBranches []string `yaml:"exclude_branches"`
}

func decodeStrict(name string, data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// LoadDefaults reads dir/defaults.yaml. The file is optional; unknown keys
// are errors.
func LoadDefaults(dir string) (*Defaults, error) {
	if dir == "" {
		return nil, errors.New("no configuration directory")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("configuration directory: %w", err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("configuration directory %s is not a directory", abs)
	}
	d := &Defaults{Dir: abs, Everref: DefaultEverref, StateDir: filepath.Join(abs, "state"), Timeout: DefaultTimeout}
	path := filepath.Join(abs, DefaultsFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var raw rawDefaults
	if err := decodeStrict(path, data, &raw); err != nil {
		return nil, err
	}
	if p := raw.Everref.Path; p != nil {
		if *p == "" {
			return nil, fmt.Errorf("%s: everref.path is empty", path)
		}
		d.Everref = *p
		if filepath.Base(*p) != *p { // a path, not a name on PATH
			d.Everref = resolve(abs, *p)
		}
	}
	if v := raw.Everref.Version; v != nil {
		d.EverrefVersion = *v
	}
	d.NotifyCommand = raw.Notify.Command
	if raw.StateDir != nil {
		if *raw.StateDir == "" {
			return nil, fmt.Errorf("%s: state_dir is empty", path)
		}
		d.StateDir = resolve(abs, *raw.StateDir)
	}
	if raw.Timeout != nil {
		t, err := time.ParseDuration(*raw.Timeout)
		if err != nil || t <= 0 {
			return nil, fmt.Errorf("%s: timeout %q is not a positive duration", path, *raw.Timeout)
		}
		d.Timeout = t
	}
	return d, nil
}

// Repos lists the configured repos, sorted.
func Repos(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "repos"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "repos", e.Name(), RepoFile)); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// LoadRepo reads dir/repos/<name>/pull.yaml.
func LoadRepo(dir, name string) (*Repo, error) {
	if !config.ValidRepoName(name) {
		return nil, fmt.Errorf("invalid repo name %q", name)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	repoDir := filepath.Join(abs, "repos", name)
	path := filepath.Join(repoDir, RepoFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w %q: %s is missing", ErrUnknownRepo, name, path)
	}
	if err != nil {
		return nil, err
	}
	var raw rawRepo
	if err := decodeStrict(path, data, &raw); err != nil {
		return nil, err
	}
	if raw.Remote == "" {
		return nil, fmt.Errorf("%s: remote is required", path)
	}
	kind, err := config.RemoteKind(raw.Remote)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	r := &Repo{Name: name, Remote: raw.Remote, Credential: resolve(repoDir, raw.Credential), KnownHosts: resolve(repoDir, raw.KnownHosts)}
	if kind == config.KindLocal && !filepath.IsAbs(raw.Remote) && !strings.HasPrefix(raw.Remote, "file://") {
		return nil, fmt.Errorf("%s: a local remote must be an absolute path or a file:// URL", path)
	}
	switch {
	case kind == config.KindSSH && r.Credential == "":
		return nil, fmt.Errorf("%s: an SSH remote needs a credential (read-only deploy key)", path)
	case kind == config.KindHTTPS && r.Credential == "":
		return nil, fmt.Errorf("%s: an HTTPS remote needs a credential (read-only token file)", path)
	case kind != config.KindSSH && r.KnownHosts != "":
		return nil, fmt.Errorf("%s: known_hosts only applies to SSH remotes", path)
	case kind == config.KindLocal && r.Credential != "":
		return nil, fmt.Errorf("%s: a local remote takes no credential", path)
	}
	for _, p := range raw.ExcludeBranches {
		re, err := config.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: exclude_branches: %w", path, err)
		}
		r.ExcludeBranches = append(r.ExcludeBranches, re)
	}
	return r, nil
}

// Excluded reports whether branch (without refs/heads/) is excluded.
func (r *Repo) Excluded(branch string) bool {
	for _, re := range r.ExcludeBranches {
		if re.MatchString(branch) {
			return true
		}
	}
	return false
}

func resolve(dir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}
