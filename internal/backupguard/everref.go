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

	"github.com/pihme/git-warden/internal/gitx"
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
	// The answer is evaluated: above 1 MiB the preflight fails (REQ-BG-116).
	stdout, stderr, err := gitx.RunBounded(cmd, path+" --version")
	if errors.Is(err, gitx.ErrOutputTooLarge) {
		return "", "", err
	}
	out := append(stdout, stderr...)
	if err != nil {
		return "", "", fmt.Errorf("%s --version: %w: %s", path, err, truncate(strings.TrimSpace(string(out)), maxErrorText))
	}
	version = parseVersion(string(out))
	if version == "" {
		return "", "", fmt.Errorf("%s --version: unexpected output %q", path, truncate(strings.TrimSpace(string(out)), maxErrorText))
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

// Run runs everref -C dir args... and returns the end of its combined
// output: only the last 1 MiB is kept, the rest is dropped (REQ-BG-115, 117).
func (e *Everref) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return e.RunEach(ctx, dir, nil, args...)
}

// RunEach is Run that also hands every output line to each while the output
// is read, so whatever each counts stays right for any output size
// (REQ-BG-115).
func (e *Everref) RunEach(ctx context.Context, dir string, each func(line string), args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, e.Bin, full...)
	cmd.Env = e.Env
	cmd.WaitDelay = killWait
	out := &lineWriter{tail: gitx.TailBuffer{Max: gitx.OutputLimit}, each: each}
	cmd.Stdout, cmd.Stderr = out, out // one writer: one pipe, order kept
	err := cmd.Run()
	out.flush()
	text := out.tail.String()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%v)", ctx.Err(), err)
		}
		return text, &RunError{Args: full, ExitCode: code, Output: text, Err: err}
	}
	return text, nil
}

// Fixed limits (REQ-BG-118).
const (
	maxErrorText = 300             // characters of a program's error text
	killWait     = 5 * time.Second // after a kill, for the output pipes to close
	maxEventLine = 64 << 10        // longest line handed to RunEach's each
)

// lineWriter keeps the tail of the output and hands each complete line to
// each. A line longer than maxEventLine is not handed on (no event line is
// that long), so memory stays bounded.
type lineWriter struct {
	tail    gitx.TailBuffer
	each    func(string)
	partial []byte
	tooLong bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.tail.Write(p)
	if w.each == nil {
		return len(p), nil
	}
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			w.add(rest)
			break
		}
		w.add(rest[:i])
		w.flush()
		rest = rest[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) add(b []byte) {
	if w.tooLong || len(w.partial)+len(b) > maxEventLine {
		w.tooLong, w.partial = true, w.partial[:0]
		return
	}
	w.partial = append(w.partial, b...)
}

// flush hands on the line collected so far.
func (w *lineWriter) flush() {
	if w.each != nil && !w.tooLong && len(w.partial) > 0 {
		w.each(string(w.partial))
	}
	w.partial, w.tooLong = w.partial[:0], false
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
