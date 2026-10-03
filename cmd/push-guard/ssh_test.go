package main

// SSH end to end: the test starts its own sshd on a free local port with a
// fresh host key and a fresh user key, and the guard forwards to a bare repo
// behind it. This covers the key (-i, IdentitiesOnly) and the host key check
// (known_hosts) without any outside service.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pihme/git-warden/internal/pushguard"
	"github.com/pihme/git-warden/internal/testutil"
)

func findSSHD() string {
	if p, err := exec.LookPath("sshd"); err == nil {
		return p
	}
	if _, err := os.Stat("/usr/sbin/sshd"); err == nil {
		return "/usr/sbin/sshd"
	}
	return ""
}

func keygen(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "warden-test", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(pub))
}

// startSSHD runs sshd as the current user and returns its port and host key.
func startSSHD(t *testing.T, dir, authorized string) (int, string) {
	t.Helper()
	sshd := findSSHD()
	hostPub := keygen(t, filepath.Join(dir, "host_key"))
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), []byte(authorized+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	cfg := filepath.Join(dir, "sshd_config")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`Port %d
ListenAddress 127.0.0.1
HostKey %s
AuthorizedKeysFile %s
PidFile %s
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
UsePAM no
StrictModes no
LogLevel ERROR
`, port, filepath.Join(dir, "host_key"), filepath.Join(dir, "authorized_keys"), filepath.Join(dir, "sshd.pid"))), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sshd, "-D", "-e", "-f", cfg)
	var logs strings.Builder
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sshd: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; ; i++ {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		if i > 50 {
			t.Fatalf("sshd did not come up: %v\n%s", err, logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return port, hostPub
}

func TestSSHRemote(t *testing.T) {
	_, kerr := exec.LookPath("ssh-keygen")
	if findSSHD() == "" || kerr != nil {
		if os.Getenv("SSH_REQUIRED") != "" {
			t.Fatal("sshd or ssh-keygen not found but SSH_REQUIRED is set")
		}
		t.Skip("sshd or ssh-keygen not found")
	}
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	sshDir := filepath.Join(base, "ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(sshDir, "guard_key")
	port, hostPub := startSSHD(t, sshDir, keygen(t, key))

	known := filepath.Join(sshDir, "known_hosts")
	hostFields := strings.Fields(hostPub)
	os.WriteFile(known, []byte(fmt.Sprintf("[127.0.0.1]:%d %s %s\n", port, hostFields[0], hostFields[1])), 0o600)

	remoteDir := filepath.Join(base, "remote.git")
	testutil.Run(t, "", "init", "--quiet", "--bare", "-b", "main", remoteDir)
	seed := testutil.Open(t, filepath.Join(base, "seed"))
	testutil.Run(t, "", "clone", "--quiet", remoteDir, seed.Dir)
	seed.Commit("initial", map[string]string{"README.md": "hello\n"})
	seed.Git("push", "--quiet", "origin", "HEAD:main")
	remote := testutil.Open(t, remoteDir)
	url := fmt.Sprintf("ssh://%s@127.0.0.1:%d%s", me.Username, port, remoteDir)

	e := &env{t: t, base: base, config: filepath.Join(base, "warden"), state: filepath.Join(base, "state"),
		notify: filepath.Join(base, "notify.jsonl"), remote: remote}
	repoYAML := func(keyFile, knownHosts string) string {
		return fmt.Sprintf("remote: %s\ncredential: %s\nknown_hosts: %s\n", url, keyFile, knownHosts)
	}
	testutil.WriteFiles(t, e.config, map[string]string{
		"defaults.yaml": fmt.Sprintf(`agent:
  name: ssh-test-agent
state_dir: %s
notify:
  command: [sh, -c, 'cat >> "$0"', %s]
rules:
  CONTENT-SECRET: {enabled: false}
`, e.state, e.notify),
		"repos/" + repoName + "/warden.yaml": repoYAML(key, known),
	})
	assertContains(t, e.run("check-config", "--config", e.config, "--remote"), "remote "+url)
	e.run("init-repo", "--config", e.config, repoName)
	e.guardDir = pushguard.RepoPath(e.state, repoName)
	e.agent = testutil.Open(t, filepath.Join(base, "agent"))
	testutil.Run(t, "", "clone", "--quiet", e.guardDir, e.agent.Dir)

	// a green push goes out over SSH with exactly the checked SHA
	head := e.agent.Commit("over ssh", map[string]string{"ssh.txt": "x\n"})
	assertContains(t, e.mustPush("origin", "main"), "forwarded to the remote")
	if got := e.remoteRef("refs/heads/main"); got != head {
		t.Fatalf("remote main = %s, want %s", got, head)
	}

	setRepo := func(yaml string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(e.config, "repos", repoName, "warden.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(what, wantErr string) {
		t.Helper()
		before := e.remoteRef("refs/heads/main")
		e.agent.Commit(what, map[string]string{"ssh.txt": what + "\n"})
		out := e.mustReject("origin", "main")
		assertNotContains(t, out, url, sshDir)
		assertContains(t, out, "internal error, try again later")
		if got := e.remoteRef("refs/heads/main"); got != before {
			t.Fatalf("%s: remote moved to %s", what, got)
		}
		if p := e.lastPush(); p.Forwarded || !strings.Contains(p.Error, wantErr) {
			t.Fatalf("%s: journal entry %+v, want error containing %q", what, p, wantErr)
		}
	}

	// a host key that doesn't match known_hosts is refused, never accepted
	other := filepath.Join(sshDir, "other_host")
	otherPub := strings.Fields(keygen(t, other))
	wrongKnown := filepath.Join(sshDir, "known_hosts_wrong")
	os.WriteFile(wrongKnown, []byte(fmt.Sprintf("[127.0.0.1]:%d %s %s\n", port, otherPub[0], otherPub[1])), 0o600)
	setRepo(repoYAML(key, wrongKnown))
	refused("wrong host key", "REMOTE HOST IDENTIFICATION HAS CHANGED")

	// an unknown host (empty known_hosts) is refused too
	emptyKnown := filepath.Join(sshDir, "known_hosts_empty")
	os.WriteFile(emptyKnown, nil, 0o600)
	setRepo(repoYAML(key, emptyKnown))
	refused("unknown host", "you have requested strict checking")

	// a key the server doesn't know is refused
	setRepo(repoYAML(other, known))
	refused("unauthorized key", "Permission denied (publickey)")
}
