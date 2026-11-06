# Install

How to build Git Warden and how the pinned prerequisites get onto a host. What the programs are for: [Requirements](../README.md#requirements). How to run a guard once it is installed: [Using the Push Guard](push-guard.md#using-it), [Using the Backup Guard](backup-guard.md#using-it).

Git Warden does not download gitleaks or git-everref at runtime. Install them when you provision the host, and check the download's SHA-256, as CI does. [scripts/install-everref.sh](../scripts/install-everref.sh) installs git-everref v1.0.0 for Linux amd64/arm64 and checks the tarball against the SHA-256 values pinned in the script ([details](../SPEC.md#installing-git-everref)). The container image and the Nix flake below bring git, gitleaks 8.30.1 and git-everref v1.0.0 along, pinned the same way.

## Building from source

```bash
go build -o push-guard ./cmd/push-guard
go build -o backup-guard ./cmd/backup-guard
scripts/install-everref.sh   # optional: pinned git-everref v1.0.0 into ~/.local/bin
go test ./...                # offline; skips secret-scan tests without gitleaks,
                             # and Backup Guard end-to-end tests without git-everref
```

CI installs pinned gitleaks and git-everref and sets `GITLEAKS_REQUIRED=1` and `EVERREF_REQUIRED=1`, which turn those skips into failures.

The live test (`TestLiveRemote`) runs only when `WARDEN_LIVE_REMOTE` (an `https://` URL) and `WARDEN_LIVE_TOKEN_FILE` are set. It pushes through a real guard to a fresh `testrun-<UTC timestamp>-<run>` branch on that remote: a green create and fast-forward, a red rewrite that leaves the remote alone, approval of that commit going out with a lease, another writer moving the branch, and finally an allowed delete. CI runs it against this repository on pushes to `main` with the job's own token, and deletes leftover `testrun-*` branches older than a day. `TestSSHRemote` starts its own `sshd` (skipped if `sshd` or `ssh-keygen` is missing; CI installs it and sets `SSH_REQUIRED=1`).

Releases attach static `push-guard-linux-amd64` and `backup-guard-linux-amd64` binaries, `install-everref.sh`, `LICENSE` and `THIRD_PARTY_NOTICES.md`. Install a binary with `install -m 0755 push-guard-linux-amd64 /usr/local/bin/push-guard` (the same for `backup-guard`).

## Docker

The [Dockerfile](../Dockerfile) builds one image with both guards and their prerequisites, provisioned at pinned versions: static `push-guard` and `backup-guard` (built with Go 1.27), Debian trixie's `git` 2.47 (the build fails below 2.42) with `openssh-client`, gitleaks 8.30.1 (the CI release, SHA-256 checked) and git-everref v1.0.0 (via [scripts/install-everref.sh](../scripts/install-everref.sh), SHA-256 checked). Base images are pinned by digest. It runs as the unprivileged user `warden` (uid 10001) and contains no configuration and no secrets. The image isn't published; build it yourself:

```bash
docker build -t git-warden --build-arg VERSION=$(git describe --tags --always) .
```

**Push Guard** (the default command is `push-guard serve --config /etc/warden --listen 0.0.0.0:8418`):

```bash
docker run -d --name push-guard -p 8418:8418 \
  -v /etc/warden:/etc/warden:ro \
  -v warden-state:/var/lib/warden \
  git-warden
docker run --rm -v /etc/warden:/etc/warden:ro git-warden push-guard check-config --config /etc/warden
```

**Backup Guard**, from a timer on the backup host (for example the [systemd units](../examples/backup/systemd) with this as `ExecStart`):

```bash
docker run --rm \
  -v /etc/warden-backup:/etc/warden-backup:ro \
  -v warden-backup-state:/var/lib/warden-backup \
  git-warden backup-guard run --config /etc/warden-backup
```

In the container:

- Set `state_dir: /var/lib/warden` (Push Guard) or `/var/lib/warden-backup` (Backup Guard) in `defaults.yaml` and keep it on a volume; the [examples](../examples) already do. The configuration mount stays read-only.
- Every file the configuration points to (`agent.token_file`, `credential`, `known_hosts`, `gitleaks.toml`) must be inside the mounted directory and readable by uid 10001. Give SSH remotes a `known_hosts` file there, since the container user has none of its own.
- gitleaks is at `/usr/local/bin/gitleaks` and git-everref at `/usr/local/bin/git-everref`, both on `PATH`, so `gitleaks.path` and `everref.path` can stay unset (or keep the examples' values).
- `notify.command` runs inside the container: mount it in as well (the image has `sh`, but no mail or HTTP client besides `git`).
- The guard repositories in the state volume have hooks that call `/usr/local/bin/push-guard`, so keep using this image with that volume.

The same preflight runs at start as on a host: without a configuration, repos, an enabled rule or gitleaks, `serve` exits with the reason instead of starting.

## Nix

Two kinds of flake. Both need flakes enabled (`experimental-features = nix-command flakes`).

The reference environment for build and test lives under `nix/`. `nix/reference-env` is the Go toolchain. `nix/git-tools` adds git and OpenSSH. `nix/scanners` adds gitleaks 8.30.1 and git-everref v1.0.0 and includes the other two. CI runs the tests in that shell:

```bash
nix develop ./nix/scanners -c go test ./...
```

[flake.nix](../flake.nix) at the repo root is the package people install (nixpkgs pinned in [flake.lock](../flake.lock)), on Linux (x86_64, aarch64). `nix develop` there is that package's shell, on nixpkgs 26.05. The shells under `nix/` track nixpkgs unstable.

```bash
nix build          # ./result/bin/push-guard and ./result/bin/backup-guard; runs go test ./... in the sandbox
nix develop        # shell with Go, git, OpenSSH, gitleaks 8.30.1 and git-everref v1.0.0
nix flake check
```

gitleaks and git-everref come from their pinned release tarballs with the same SHA-256 as CI and the Dockerfile, not from nixpkgs, so every environment runs the same versions. Git Warden's license (PolyForm Noncommercial) counts as unfree in Nix; the flake allows exactly this package. When you change `go.mod` or `go.sum`, update `vendorHash` in `flake.nix` (Nix prints the new value).
