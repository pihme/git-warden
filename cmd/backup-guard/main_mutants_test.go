package main

// Mutation-kill tests for cmd/backup-guard (gomutants survivors). Separate
// file so QA's fuzz/rapid work does not collide.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runSplit(args ...string) (code int, stdout, stderr string) {
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

// Usage errors go to stderr, not stdout (REQ-BG-091).
func TestMutantsUsageGoesToStderr(t *testing.T) {
	code, out, errOut := runSplit()
	if code != 2 || out != "" || !strings.Contains(errOut, "backup-guard run") {
		t.Fatalf("no args: exit %d stdout %q stderr %q", code, out, errOut)
	}
	code, out, errOut = runSplit("frobnicate")
	if code != 2 || out != "" || !strings.Contains(errOut, `unknown command "frobnicate"`) || !strings.Contains(errOut, "backup-guard run") {
		t.Fatalf("unknown command: exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// Flag errors are written to the given stderr (fs.SetOutput), not os.Stderr.
func TestMutantsFlagErrorsUseStderrWriter(t *testing.T) {
	code, out, errOut := runSplit("run", "--bogus")
	if code != 2 || out != "" || !strings.Contains(errOut, "flag provided but not defined: -bogus") {
		t.Fatalf("bad flag: exit %d stdout %q stderr %q", code, out, errOut)
	}
}

// -h on a subcommand is a help request: exit 0, not a usage error.
func TestMutantsSubcommandHelpExitsZero(t *testing.T) {
	for _, cmd := range []string{"run", "check-config"} {
		code, _, errOut := runSplit(cmd, "-h")
		if code != 0 {
			t.Errorf("%s -h: exit %d, want 0; stderr %q", cmd, code, errOut)
		}
		if strings.Contains(errOut, "backup-guard: flag: help requested") {
			t.Errorf("%s -h: reported as error: %q", cmd, errOut)
		}
	}
}

// A preflight error keeps its cause in the chain ("preflight: %w").
func TestMutantsPreflightErrorWraps(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	err := runRepos(context.Background(), missing, nil, &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "preflight: ") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preflight error lost its cause: %v", err)
	}
}

// Positional arguments may be interleaved with flags.
func TestMutantsParseInterleavedPositionals(t *testing.T) {
	code, out, errOut := runSplit("check-config", "x", "--config", t.TempDir())
	if code != 2 || out != "" || !strings.Contains(errOut, "check-config takes no arguments") {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}
