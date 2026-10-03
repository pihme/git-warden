package gitx

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// MinMajor and MinMinor are the oldest git Git Warden supports.
const (
	MinMajor = 2
	MinMinor = 42
)

var gitVersionRE = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// CheckGit is the git part of both guards' preflight: git must be on PATH
// and at least MinMajor.MinMinor. It returns git's path and its version as
// "major.minor".
func CheckGit(ctx context.Context) (path, version string, err error) {
	path, err = exec.LookPath("git")
	if err != nil {
		return "", "", fmt.Errorf("git not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", "", fmt.Errorf("%s version: %w", path, err)
	}
	version = string(out)
	major, minor, ok := ParseGitVersion(version)
	if !ok {
		return "", "", fmt.Errorf("%s version: unexpected output %q", path, version)
	}
	if major < MinMajor || (major == MinMajor && minor < MinMinor) {
		return "", "", fmt.Errorf("git %d.%d is too old: Git Warden needs %d.%d or newer", major, minor, MinMajor, MinMinor)
	}
	return path, fmt.Sprintf("%d.%d", major, minor), nil
}

// ParseGitVersion parses "git version 2.47.3" (and vendor suffixes).
func ParseGitVersion(s string) (major, minor int, ok bool) {
	m := gitVersionRE.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, true
}
