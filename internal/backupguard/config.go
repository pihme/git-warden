// Package backupguard is the Backup Guard of Git Warden: it keeps an append-only
// backup of every branch and tag of a remote by running git-everref.
// Git Warden contains no everref code; everref is an external program that
// must be installed on the host (like gitleaks for the Push Guard).
package backupguard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pihme/git-warden/internal/config"
)

// Built-in defaults. Everything else must be configured.
const (
	DefaultEverref = "git-everref"
	// DefaultEverrefVersion is the everref.version used when defaults.yaml
	// doesn't set one. Only its major version is compared (FindEverref).
	DefaultEverrefVersion = "1.0.0"
	// DevVersion is what a plain source build of git-everref reports. It is
	// only accepted when everref.version is exactly "dev".
	DevVersion     = "dev"
	DefaultTimeout = 30 * time.Minute
	DefaultsFile   = "defaults.yaml"
	RepoFile       = "backup.yaml"
)

// ErrUnknownRepo is returned for a repo without repos/<name>/backup.yaml.
var ErrUnknownRepo = errors.New("unknown repo")

// Defaults is defaults.yaml of a Backup Guard configuration directory.
type Defaults struct {
	Dir            string        // configuration directory
	Everref        string        // everref binary: a path or a name looked up on PATH
	EverrefVersion string        // everref --version must report the same major version ("dev": exactly dev)
	NotifyCommand  []string      // gets a warning as JSON on stdin when a run fails
	StateDir       string        // bridge clones, backup repositories, backup.jsonl
	Timeout        time.Duration // per repo and run

	everrefVersionSet bool // everref.version came from defaults.yaml
}

// Repo is repos/<name>/backup.yaml.
type Repo struct {
	Name               string
	Remote             string           // the remote to back up (read access is enough)
	Credential         string           // read-only SSH key or token file; none for local remotes
	CredentialUsername string           // HTTPS user name sent with the token; empty: x-access-token
	KnownHosts         string           // SSH only
	ExcludeBranches    []*regexp.Regexp // anchored; matched against the branch name without refs/heads/
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
	Remote             string   `yaml:"remote"`
	Credential         string   `yaml:"credential"`
	CredentialUsername string   `yaml:"credential_username"`
	KnownHosts         string   `yaml:"known_hosts"`
	ExcludeBranches    []string `yaml:"exclude_branches"`
}

func decodeStrict(name string, data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// versionRE is a valid everref.version other than "dev": [v]MAJOR[.MINOR[.PATCH]].
var versionRE = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+){0,2}$`)

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
	d := &Defaults{Dir: abs, Everref: DefaultEverref, EverrefVersion: DefaultEverrefVersion, StateDir: filepath.Join(abs, "state"), Timeout: DefaultTimeout}
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
		d.Everref = config.ResolveProgram(abs, *p)
	}
	if v := raw.Everref.Version; v != nil {
		if *v != DevVersion && !versionRE.MatchString(*v) {
			return nil, fmt.Errorf("%s: everref.version %q is neither a version like 1.0.0 nor %q", path, *v, DevVersion)
		}
		d.EverrefVersion, d.everrefVersionSet = *v, true
	}
	if c := raw.Notify.Command; len(c) > 0 {
		if c[0] == "" {
			return nil, fmt.Errorf("%s: notify.command has an empty program", path)
		}
		// Like everref.path: a bare name is looked up on PATH, a relative
		// path is relative to the configuration directory.
		d.NotifyCommand = append([]string{config.ResolveProgram(abs, c[0])}, c[1:]...)
	}
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

// LoadRepo reads dir/repos/<name>/backup.yaml.
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
	if err := config.CheckRemote(raw.Remote, raw.Credential, raw.CredentialUsername, raw.KnownHosts); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Relative file paths are relative to the configuration directory, as in
	// the Push Guard.
	r := &Repo{Name: name, Remote: raw.Remote, Credential: resolve(abs, raw.Credential),
		CredentialUsername: raw.CredentialUsername, KnownHosts: resolve(abs, raw.KnownHosts)}
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
