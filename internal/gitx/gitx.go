// Package gitx runs git as a subprocess with a context timeout.
//
// The environment of the current process is passed through, so a git command
// started from a pre-receive hook sees the quarantined objects of the push.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ZeroOID is the all-zero object name used for created and deleted refs.
const ZeroOID = "0000000000000000000000000000000000000000"

// IsZero reports whether oid is all zeros (SHA-1 or SHA-256).
func IsZero(oid string) bool { return oid != "" && strings.Trim(oid, "0") == "" }

// EmptyTree is the SHA-1 empty tree object.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Git runs git commands in one repository.
type Git struct {
	Dir     string        // working directory (the repository); empty for none
	Env     []string      // extra environment, appended to os.Environ()
	Unset   []string      // environment variables to drop
	Timeout time.Duration // per command; 0 means none beyond the context
	Bin     string        // git binary, default "git"
}

// Error is a failed git command with its stderr.
type Error struct {
	Args     []string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// ExitCode returns the exit code of a failed git command, or -1.
func ExitCode(err error) int {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.ExitCode
	}
	return -1
}

// With returns a copy with extra environment.
func (g *Git) With(env ...string) *Git {
	c := *g
	c.Env = append(append([]string(nil), g.Env...), env...)
	return &c
}

// Without returns a copy that drops the given environment variables.
func (g *Git) Without(names ...string) *Git {
	c := *g
	c.Unset = append(append([]string(nil), g.Unset...), names...)
	return &c
}

// Environ is the environment a command gets.
func (g *Git) Environ() []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(g.Env))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, u := range g.Unset {
			if name == u {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return append(out, g.Env...)
}

// Command builds an exec.Cmd for git with this configuration.
func (g *Git) Command(ctx context.Context, args ...string) *exec.Cmd {
	bin := g.Bin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = g.Dir
	cmd.Env = g.Environ()
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// Run runs git and returns stdout.
func (g *Git) Run(ctx context.Context, args ...string) ([]byte, error) {
	return g.RunInput(ctx, nil, args...)
}

// RunInput runs git with stdin and returns stdout.
func (g *Git) RunInput(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	out, _, err := g.RunFull(ctx, stdin, args...)
	return out, err
}

// RunFull runs git and returns stdout and stderr.
func (g *Git) RunFull(ctx context.Context, stdin io.Reader, args ...string) ([]byte, []byte, error) {
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}
	cmd := g.Command(ctx, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("%w: %w", ctx.Err(), err)
		}
		return stdout.Bytes(), stderr.Bytes(), &Error{Args: args, Stderr: stderr.String(), ExitCode: code, Err: err}
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// Line runs git and returns the trimmed first line of stdout.
func (g *Git) Line(ctx context.Context, args ...string) (string, error) {
	out, err := g.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	s, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(s), nil
}

// Lines runs git and returns the non-empty lines of stdout.
func (g *Git) Lines(ctx context.Context, args ...string) ([]string, error) {
	out, err := g.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return SplitLines(string(out)), nil
}

// SplitLines splits s into non-empty lines.
func SplitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ShellQuote quotes s for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
