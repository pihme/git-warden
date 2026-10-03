package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLsRemote(t *testing.T) {
	rr := ParseLsRemote("ref: refs/heads/main\tHEAD\n" +
		"aaa\tHEAD\n" +
		"aaa\trefs/heads/main\n" +
		"bbb\trefs/tags/v1\n" +
		"ccc\trefs/tags/v1^{}\n" +
		"ddd\trefs/pull/1/head\n")
	if rr.Head != "refs/heads/main" || rr.Refs["refs/heads/main"] != "aaa" || rr.Refs["refs/tags/v1"] != "bbb" || len(rr.Refs) != 3 {
		t.Fatalf("%+v", rr)
	}
}

func TestIsZero(t *testing.T) {
	if !IsZero(ZeroOID) || !IsZero(strings.Repeat("0", 64)) || IsZero("") || IsZero("0a") {
		t.Fatal("IsZero")
	}
}

func TestHTTPSCredentialHelper(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("synthetic-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := NewRemote(&Git{Env: []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}}, "https://example.invalid/o/r.git", tok)
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Git().RunInput(context.Background(), strings.NewReader("protocol=https\nhost=example.invalid\n\n"), "credential", "fill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "password=synthetic-token\n") {
		t.Fatalf("credential fill: %q", out)
	}
	for _, kv := range r.Git().Env {
		if strings.Contains(kv, "synthetic-token") {
			t.Fatal("token content in the environment")
		}
	}
}

func TestSSHCommand(t *testing.T) {
	r, err := NewRemote(&Git{}, "git@example.invalid:o/r.git", "/keys/it's")
	if err != nil {
		t.Fatal(err)
	}
	var cmd string
	for _, kv := range r.Git().Env {
		if v, ok := strings.CutPrefix(kv, "GIT_SSH_COMMAND="); ok {
			cmd = v
		}
	}
	if cmd != `ssh -i '/keys/it'\''s' -o IdentitiesOnly=yes -o BatchMode=yes` {
		t.Fatalf("GIT_SSH_COMMAND=%s", cmd)
	}
	if _, err := NewRemote(&Git{}, "git@example.invalid:o/r.git", ""); err == nil {
		t.Fatal("ssh remote without credential accepted")
	}
}
