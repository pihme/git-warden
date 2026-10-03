package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pihme/git-warden/internal/config"
)

// Scanner runs gitleaks with configuration from the wall only.
type Scanner struct {
	Bin        string // gitleaks binary
	ConfigFile string // gitleaks.toml from the wall (or a generated one)
	IgnoreFile string // gitleaksignore from the wall (or an empty file)
	RepoDir    string // repository to scan
	cleanup    []string
}

// NewScanner picks the scanner files for cfg's repo: the repo folder's
// gitleaks.toml / gitleaksignore, else the wall's, else a generated config
// that extends the gitleaks defaults and an empty ignore file.
func NewScanner(cfg *config.Config, repoDir string) (*Scanner, error) {
	s := &Scanner{Bin: cfg.Gitleaks, RepoDir: repoDir}
	if s.Bin == "" {
		s.Bin = "gitleaks"
	}
	pick := func(name string) string {
		for _, dir := range []string{cfg.RepoDir(), cfg.Dir} {
			if cfg.RepoName == "" && dir == cfg.RepoDir() {
				continue
			}
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		return ""
	}
	s.ConfigFile = pick("gitleaks.toml")
	s.IgnoreFile = pick("gitleaksignore")
	if s.ConfigFile == "" {
		p, err := s.temp("gitleaks-*.toml", "[extend]\nuseDefault = true\n")
		if err != nil {
			return nil, err
		}
		s.ConfigFile = p
	}
	if s.IgnoreFile == "" {
		p, err := s.temp("gitleaksignore-*", "")
		if err != nil {
			return nil, err
		}
		s.IgnoreFile = p
	}
	return s, nil
}

func (s *Scanner) temp(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s.cleanup = append(s.cleanup, f.Name())
	_, err = f.WriteString(content)
	return f.Name(), err
}

// Close removes generated files.
func (s *Scanner) Close() {
	for _, p := range s.cleanup {
		os.Remove(p)
	}
}

type leak struct {
	RuleID    string `json:"RuleID"`
	File      string `json:"File"`
	StartLine int    `json:"StartLine"`
	Commit    string `json:"Commit"`
}

const leakExit = 99

var gitleaksError = regexp.MustCompile(`(?m)^\S*\s*(ERR|FTL|FATAL|ERROR)\b`)

func (e *eval) secretRule(s *Scanner) error {
	r := e.cfg.Rule("CONTENT-SECRET")
	if !r.Enabled || len(e.d.Commits) == 0 {
		return nil
	}
	var include []string
	for _, rd := range e.d.Refs {
		if len(rd.Commits) > 0 {
			include = append(include, rd.NewCommit)
		}
	}
	logOpts := strings.Join(include, " ")
	if len(e.d.RemoteOIDs) > 0 {
		logOpts += " --not " + strings.Join(e.d.RemoteOIDs, " ")
	}
	report, err := os.CreateTemp("", "gitleaks-report-*.json")
	if err != nil {
		return err
	}
	report.Close()
	defer os.Remove(report.Name())

	ctx := e.ctx
	cmd := exec.CommandContext(ctx, s.Bin, "git", "--log-opts="+logOpts,
		"--config", s.ConfigFile, "--gitleaks-ignore-path", s.IgnoreFile,
		"--ignore-gitleaks-allow", "--no-banner", "--no-color", "--redact",
		"--log-level", "info", "-f", "json", "-r", report.Name(),
		"--exit-code", fmt.Sprint(leakExit), s.RepoDir)
	cmd.Dir = s.RepoDir
	cmd.Env = e.g.Environ()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return fmt.Errorf("secret scanner: %w", runErr)
		}
		code = ee.ExitCode()
	}
	if code != 0 && code != leakExit {
		return fmt.Errorf("secret scanner failed (exit %d): %s", code, firstLine(out.String()))
	}
	// gitleaks logs git errors but still exits 0, so its log is checked too.
	if m := gitleaksError.FindString(out.String()); m != "" {
		return fmt.Errorf("secret scanner reported an error: %s", firstLine(out.String()[strings.Index(out.String(), m):]))
	}
	if !strings.Contains(out.String(), "commits scanned") {
		return fmt.Errorf("secret scanner did not report a scan: %s", firstLine(out.String()))
	}
	data, err := os.ReadFile(report.Name())
	if err != nil {
		return fmt.Errorf("secret scanner report: %w", err)
	}
	var leaks []leak
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &leaks); err != nil {
			return fmt.Errorf("secret scanner report: %w", err)
		}
	}
	if code == leakExit && len(leaks) == 0 {
		return errors.New("secret scanner reported leaks but the report is empty")
	}
	for _, l := range leaks {
		e.check("CONTENT-SECRET", l.File, true, Finding{Ref: e.refOf(l.Commit), Path: l.File, Line: l.StartLine,
			Commit: l.Commit, Message: "secret scanner hit (" + l.RuleID + ")"})
	}
	return nil
}

// refOf returns the first ref whose new commits contain oid.
func (e *eval) refOf(oid string) string {
	for _, rd := range e.d.Refs {
		for _, c := range rd.Commits {
			if c.OID == oid {
				return rd.Ref
			}
		}
	}
	if len(e.d.Refs) > 0 {
		return e.d.Refs[0].Ref
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
