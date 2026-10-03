package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/pihme/git-warden/internal/config"
)

// QuarantineEnv is dropped for commands that talk to a remote: a local
// remote's receive-pack would otherwise inherit it and refuse ref updates.
var QuarantineEnv = []string{"GIT_QUARANTINE_PATH"}

// Remote talks to one remote with its credential.
type Remote struct {
	URL        string
	Credential string // SSH private key or token file; empty for local remotes
	git        *Git
}

// NewRemote prepares git for remote. The credential is a file reference only;
// its content is never put on a command line or in a log. knownHosts is an
// optional known_hosts file for SSH remotes; without it ssh uses the guard
// user's own. Unknown or changed host keys are always refused.
func NewRemote(g *Git, url, credential, knownHosts string) (*Remote, error) {
	kind, err := config.RemoteKind(url)
	if err != nil {
		return nil, err
	}
	rg := g.Without(QuarantineEnv...).With("GIT_TERMINAL_PROMPT=0")
	if knownHosts != "" && kind != config.KindSSH {
		return nil, fmt.Errorf("remote %s: known_hosts only applies to SSH remotes", url)
	}
	switch kind {
	case config.KindSSH:
		if credential == "" {
			return nil, fmt.Errorf("remote %s needs a credential (SSH key)", url)
		}
		cmd := "ssh -F none -i " + ShellQuote(credential) + " -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes"
		if knownHosts != "" {
			cmd += " -o UserKnownHostsFile=" + ShellQuote(knownHosts) + " -o GlobalKnownHostsFile=/dev/null"
		}
		rg = rg.With("GIT_SSH_COMMAND=" + cmd)
	case config.KindHTTPS:
		if credential == "" {
			return nil, fmt.Errorf("remote %s needs a credential (token file)", url)
		}
		helper := `!f() { test "$1" = get || exit 0; echo username=x-access-token; ` +
			`printf 'password=%s\n' "$(cat ` + ShellQuote(credential) + `)"; }; f`
		rg = rg.With(
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
			"GIT_CONFIG_KEY_1=credential.helper", "GIT_CONFIG_VALUE_1="+helper,
		)
	}
	return &Remote{URL: url, Credential: credential, git: rg}, nil
}

// Git returns the git runner with the remote's credential environment.
func (r *Remote) Git() *Git { return r.git }

// RemoteRefs is the result of ls-remote.
type RemoteRefs struct {
	Refs map[string]string // ref name -> oid (heads, tags and everything else)
	Head string            // symref target of HEAD, e.g. refs/heads/main; empty if unknown
}

// ListRefs runs git ls-remote --symref.
func (r *Remote) ListRefs(ctx context.Context) (*RemoteRefs, error) {
	out, err := r.git.Run(ctx, "ls-remote", "--symref", r.URL)
	if err != nil {
		return nil, err
	}
	return ParseLsRemote(string(out)), nil
}

// ParseLsRemote parses the output of git ls-remote --symref.
func ParseLsRemote(out string) *RemoteRefs {
	rr := &RemoteRefs{Refs: map[string]string{}}
	for _, line := range SplitLines(out) {
		oid, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(oid, "ref: ") {
			if name == "HEAD" {
				rr.Head = strings.TrimPrefix(oid, "ref: ")
			}
			continue
		}
		if strings.HasSuffix(name, "^{}") || name == "HEAD" {
			continue
		}
		rr.Refs[name] = oid
	}
	return rr
}

// FetchObjects fetches the given advertised oids without updating any ref or
// FETCH_HEAD. Inside a pre-receive hook the objects land in the quarantine.
func (r *Remote) FetchObjects(ctx context.Context, oids []string) error {
	if len(oids) == 0 {
		return nil
	}
	args := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--quiet",
		"--no-write-fetch-head", "--no-tags", "--no-recurse-submodules", r.URL}
	_, err := r.git.Run(ctx, append(args, oids...)...)
	return err
}
