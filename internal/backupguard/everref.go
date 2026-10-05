package backupguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Everref runs the git-everref binary. Git Warden never downloads it: it must
// be installed on the host (on PATH, or everref.path in defaults.yaml).
type Everref struct {
	Bin string   // resolved absolute path
	Env []string // full environment for every call
}

// Exit codes documented by git-everref.
const (
	EverrefOK       = 0 // every ref backed up
	EverrefRefError = 1 // at least one ref failed or was skipped
	EverrefUsage    = 2 // usage or configuration error
)

// FindEverref resolves the configured binary and checks its version
// against want (everref.version; empty means DefaultEverrefVersion). Only the
// major version is compared, so v1.4.2 satisfies 1.0.0. A source build
// reports "dev"; it is refused unless want is exactly "dev", because its
// version can't be told. Any problem is an error: without a known everref
// there is no backup, so the Backup Guard fails closed.
func FindEverref(ctx context.Context, bin, want string) (path, version string, err error) {
	path, err = exec.LookPath(bin)
	if err != nil {
		return "", "", fmt.Errorf("everref not found (%s): %w; install git-everref (see scripts/install-everref.sh) or set everref.path in defaults.yaml", bin, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("%s --version: %w: %s", path, err, truncate(strings.TrimSpace(string(out)), 300))
	}
	version = parseVersion(string(out))
	if version == "" {
		return "", "", fmt.Errorf("%s --version: unexpected output %q", path, truncate(strings.TrimSpace(string(out)), 300))
	}
	if want == "" {
		want = DefaultEverrefVersion
	}
	switch {
	case want == DevVersion && version == DevVersion:
		return path, version, nil
	case want == DevVersion:
		return "", "", fmt.Errorf("%s is version %s, but everref.version requires a source build (dev)", path, version)
	case version == DevVersion:
		return "", "", fmt.Errorf("%s is a source build (version dev), so its major version can't be checked against everref.version %s; install a release (scripts/install-everref.sh) or set everref.version: dev", path, want)
	}
	got, ok := majorVersion(version)
	if !ok {
		return "", "", fmt.Errorf("%s --version: can't read a major version from %q", path, version)
	}
	if need, _ := majorVersion(want); got != need {
		return "", "", fmt.Errorf("%s is version %s, but everref.version %s requires major version %d", path, version, want, need)
	}
	return path, version, nil
}

var majorRE = regexp.MustCompile(`^v?([0-9]+)(?:[.+-]|$)`)

// majorVersion returns the major version of "v1.2.3", "1.2.3", "1" and the like.
func majorVersion(v string) (int, bool) {
	m := majorRE.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// parseVersion takes "everref version v1.0.0" and returns "v1.0.0".
func parseVersion(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	f := strings.Fields(line)
	if len(f) < 3 || f[1] != "version" {
		return ""
	}
	return f[len(f)-1]
}

// RunError is a failed everref call.
type RunError struct {
	Args     []string
	ExitCode int
	Output   string
	Err      error
}

func (e *RunError) Error() string {
	return fmt.Sprintf("git-everref %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, tail(lastLines(e.Output, 5), 500))
}

func (e *RunError) Unwrap() error { return e.Err }

// Run runs everref -C dir args... and returns its combined output.
func (e *Everref) Run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, e.Bin, full...)
	cmd.Env = e.Env
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%v)", ctx.Err(), err)
		}
		return out.String(), &RunError{Args: full, ExitCode: code, Output: out.String(), Err: err}
	}
	return out.String(), nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
