package backupguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
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

// FindEverref resolves the configured binary and, if want is set, checks that
// everref --version reports exactly that version. Any problem is an error:
// without everref there is no backup, so the Backup Guard fails closed.
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
	if want != "" && version != want {
		return "", "", fmt.Errorf("%s is version %s, but everref.version requires %s", path, version, want)
	}
	return path, version, nil
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
	return fmt.Sprintf("git-everref %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, truncate(lastLines(e.Output, 5), 500))
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
