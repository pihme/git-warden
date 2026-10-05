# Git Warden

[![CI](https://github.com/pihme/git-warden/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/pihme/git-warden/actions/workflows/ci.yml)
[![License: PolyForm Noncommercial](https://img.shields.io/badge/license-PolyForm%20Noncommercial-blue)](LICENSE)
[![Release](https://img.shields.io/github/v/release/pihme/git-warden?label=release)](https://github.com/pihme/git-warden/releases)
[![Website](https://img.shields.io/badge/website-pihme.github.io%2Fgit--warden-455a6e)](https://pihme.github.io/git-warden/)
[![Go](https://img.shields.io/badge/go-%3E%3D1.24-00ADD8?logo=go&logoColor=white)](go.mod)

Git Warden puts guard posts between AI agents and their Git remote: a **Push Guard** that checks every push with deterministic rules, and a **Backup Guard** that keeps an append-only backup of every branch and tag. The remote can be GitHub, GitLab, Gitea/Forgejo or a bare repo over SSH. Neither guard uses AI: every decision is a deterministic rule.

- **Push Guard** (`push-guard`): agents push to it instead of the remote, it checks every push while the push is running, and only it holds a write credential for the remote. A clean push is forwarded with exactly the checked SHAs; a `yellow` finding goes back to the agent with a message it can act on; a `red` finding is rejected without details and waits for a human.
- **Backup Guard** (`backup-guard`): on a timer on a separate backup host, it runs [git-everref](https://github.com/daojyun/git-everref) against each remote with a read-only credential. A force push, deletion or moved tag upstream becomes a new lineage or a tombstone in the backup; nothing that a run has seen is ever lost.

## Status

Both guard posts are built. The Push Guard (`push-guard`): all rules of the spec, the pre-receive hook, the HTTP server, the human commands and `replay`, covered by offline tests against real temporary Git repositories, plus a live test in CI against GitHub over HTTPS and an SSH test against a local `sshd` (see [Limits](#limits)). The Backup Guard (`backup-guard`): preflight, bridge and backup setup, branch selection and the git-everref run, covered by tests with a stand-in everref and end to end with the real git-everref against a local remote (force push, deletion, moved tag, unreachable remote, an HTTP remote that requires a token, first runs killed at every stage). Neither is **in production use yet**.

Website and handbook: [pihme.github.io/git-warden](https://pihme.github.io/git-warden/). Documentation: [SPEC.md](SPEC.md) is the one authoritative document (design of both guard posts, decisions, the risk register and [what Git Warden does **not** cover](SPEC.md#coverage-by-git-warden)). Configuration and operation, the same structure for both guards: [docs/push-guard.md](docs/push-guard.md) (Push Guard, with the rules; reasoning per rule in [docs/push-guard-rules.md](docs/push-guard-rules.md)) and [docs/backup-guard.md](docs/backup-guard.md) (Backup Guard); overview in [Configuration and operation](#configuration-and-operation).

## How it works

### Push Guard

```
agent ── git push ──> push-guard (wall) ── checked SHAs only ──> remote
                       │ pre-receive hook: ls-remote, rules, gitleaks
                       ├ push.jsonl (every push, approval, reset)
                       └ pending/<repo>/<id>.bundle (red pushes)
```

1. The agent pushes to `<push guard>/<name>.git`. The guard's bare repository runs `push-guard pre-receive` as its hook.
2. The hook reads the remote's refs fresh (`git ls-remote`; the agent's idea of the old state is not trusted), fetches remote objects it lacks into the push's quarantine, and computes per ref the kind of change (`create`, `delete`, `ff`, `non_ff`, `tag_move`), the cumulative diff and the new commits.
3. All rules run; the verdict is the worst color found.
   - **green:** `git push --porcelain --atomic <remote> <sha>:<ref> …` with the guard's credential, never `--force`. Then the agent's push is accepted. If the remote rejects, the agent's push is rejected with the remote's message.
   - **yellow:** rejected with one line per finding (rule, ref, path, line, why). No human is involved; the agent fixes its change or leaves the part out.
   - **red:** rejected with only `rejected: waiting for a human (push <id>)`. The new commits are kept as a bundle and `notify.command` warns the owner.
   - **internal error** (scanner missing, remote unreachable, object missing, timeout): rejected with `internal error, try again later`, logged, counted as red, but not warned as a violation.

### Backup Guard

```
timer ──> backup-guard run (backup host) ── read-only ──> remote
            │ preflight: git, git-everref (version), config
            ├ repos/<name>/bridge      ls-remote, git-everref add, git-everref run --all
            ├ repos/<name>/backup.git  append-only: lineages, tombstones, journals
            └ backup.jsonl             one line per repo and run
```

1. **Preflight:** the configuration loads, `git` and `git-everref` are found and `git-everref --version` answers (and matches the pinned version if one is configured), at least one repo is configured. Otherwise the run fails closed before anything is touched.
2. Per repo it lists the remote's branches, protects new ones with `git-everref add origin/<branch> --remote backup` (minus `exclude_branches`), and runs `git-everref run --all`, which fetches the remote and records every protected branch and all tags.
3. A failed repo (remote unreachable, a branch everref refuses, a non-zero everref exit, a timeout) is logged in `backup.jsonl` and warned through `notify.command`; the other repos still run.

## Requirements

Git Warden calls a few external programs. **It doesn't install or download any of them at runtime**: they must already be on the host, found on `PATH` or at the configured path, and each runs as its own process.

| Program | Version | License | Needed for | If it is missing |
| --- | --- | --- | --- | --- |
| [Git](https://git-scm.com/) | 2.42 or newer | GPL-2.0 | Both guards at runtime: every Git operation, and `git http-backend` for `push-guard serve` | Neither guard starts: both check it in their preflight |
| [gitleaks](https://github.com/gitleaks/gitleaks) | 8.x (CI pins 8.30.1) | MIT | Push Guard at runtime: the secret scan (`CONTENT-SECRET`). Must be on `PATH`, or at `gitleaks.path` | **Required if `CONTENT-SECRET` is enabled, which is the default**: `serve` and `init-repo` refuse to start, every push fails closed (`internal error`), and `check-config` reports it. Optional only where `CONTENT-SECRET` is switched off on purpose. Optional for the tests: the secret-scan tests are skipped |
| [git-everref](https://github.com/daojyun/git-everref) | v1.0.0 (pinned) | MIT | Backup Guard at runtime: records branches and tags in the backup. Must be on `PATH`, or at the path set as `everref.path` | **Required** for the Backup Guard: every run fails closed in its preflight, and `check-config` reports it. Optional for the tests: the end-to-end tests are skipped |
| [Go](https://go.dev/) | 1.24 or newer | BSD-3-Clause | Building from source only | Use the release binaries |

**Pinning is part of provisioning the host, not something Git Warden does at runtime.** Install gitleaks and git-everref at a fixed version and check the download's SHA-256 when you set up the host, as CI does. For git-everref there is an optional script that does exactly that: [scripts/install-everref.sh](scripts/install-everref.sh) installs v1.0.0 for Linux amd64/arm64 and checks the tarball against SHA-256 values pinned in the script ([details](SPEC.md#installing-git-everref)). The [container image](#docker) and the [Nix flake](#nix) bring git, gitleaks 8.30.1 and git-everref v1.0.0 along, pinned the same way. Licenses of these programs and of the code compiled into the binaries: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Build and install

```bash
go build -o push-guard ./cmd/push-guard
go build -o backup-guard ./cmd/backup-guard
scripts/install-everref.sh   # optional: pinned git-everref v1.0.0 into ~/.local/bin (SHA-256 checked)
go test ./...      # offline; secret-scan tests skip without gitleaks, everref end-to-end tests without git-everref
```

CI installs pinned gitleaks and git-everref and sets `GITLEAKS_REQUIRED=1` and `EVERREF_REQUIRED=1`, which turn those skips into failures.

The live test (`TestLiveRemote`) runs only when `WARDEN_LIVE_REMOTE` (an `https://` URL) and `WARDEN_LIVE_TOKEN_FILE` are set. It pushes through a real guard to a fresh `testrun-<UTC timestamp>-<run>` branch on that remote: green create and fast-forward, red rewrite that leaves the remote alone, approval of that SHA going out with a lease, another writer moving the branch, and finally an allowed delete. CI runs it against this repository on pushes to `main` with the job's own token, and deletes leftover `testrun-*` branches older than a day. `TestSSHRemote` starts its own `sshd` (skipped if `sshd` or `ssh-keygen` is missing; CI installs it and sets `SSH_REQUIRED=1`).

Releases attach static `push-guard-linux-amd64` and `backup-guard-linux-amd64` binaries, `install-everref.sh`, `LICENSE` and `THIRD_PARTY_NOTICES.md`. Install a binary with `install -m 0755 push-guard-linux-amd64 /usr/local/bin/push-guard` (the same for `backup-guard`).

## Docker

The [Dockerfile](Dockerfile) builds one image with both guards and their prerequisites, provisioned at pinned versions: static `push-guard` and `backup-guard` (built with Go 1.27), Debian trixie's `git` 2.47 (the build fails below 2.42) with `openssh-client`, gitleaks 8.30.1 (the CI release, SHA-256 checked) and git-everref v1.0.0 (via [scripts/install-everref.sh](scripts/install-everref.sh), SHA-256 checked). Base images are pinned by digest. It runs as the unprivileged user `warden` (uid 10001) and contains no configuration and no secrets. The image isn't published; build it yourself:

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

**Backup Guard**, from a timer on the backup host (for example the [systemd units](examples/backup/systemd) with this as `ExecStart`):

```bash
docker run --rm \
  -v /etc/warden-backup:/etc/warden-backup:ro \
  -v warden-backup-state:/var/lib/warden-backup \
  git-warden backup-guard run --config /etc/warden-backup
```

In the container:

- Set `state_dir: /var/lib/warden` (Push Guard) or `/var/lib/warden-backup` (Backup Guard) in `defaults.yaml` and keep it on a volume; the [examples](examples) already do. The configuration mount stays read-only.
- Every file the configuration points to (`agent.token_file`, `credential`, `known_hosts`, `gitleaks.toml`) must be inside the mounted directory and readable by uid 10001. Give SSH remotes a `known_hosts` file there, since the container user has none of its own.
- gitleaks is at `/usr/local/bin/gitleaks` and git-everref at `/usr/local/bin/git-everref`, both on `PATH`, so `gitleaks.path` and `everref.path` can stay unset (or keep the examples' values).
- `notify.command` runs inside the container: mount it in as well (the image has `sh`, but no mail or HTTP client besides `git`).
- The guard repositories in the state volume have hooks that call `/usr/local/bin/push-guard`, so keep using this image with that volume.

The same preflight runs at start as on a host: without a configuration, repos, an enabled rule or gitleaks, `serve` exits with the reason instead of starting.

## Nix

[flake.nix](flake.nix) (nixpkgs pinned in [flake.lock](flake.lock)) gives a reproducible build and development environment on Linux (x86_64, aarch64). It needs flakes enabled (`experimental-features = nix-command flakes`).

```bash
nix build          # ./result/bin/push-guard and ./result/bin/backup-guard; runs go test ./... in the sandbox
nix develop        # shell with Go, git, OpenSSH, gitleaks 8.30.1 and git-everref v1.0.0
nix flake check
```

gitleaks and git-everref come from their pinned release tarballs with the same SHA-256 as CI and the Dockerfile, not from nixpkgs, so every environment runs the same versions. Git Warden's license (PolyForm Noncommercial) counts as unfree in Nix; the flake allows exactly this package. When you change `go.mod` or `go.sum`, update `vendorHash` in `flake.nix` (Nix prints the new value).

## Configuration and operation

Each guard has its own configuration directory on its own host, with the same layout and the same names for what they share: a `defaults.yaml` for the whole guard and one folder per remote under `repos/<name>/` (`warden.yaml` for the Push Guard, `backup.yaml` for the Backup Guard). Unknown keys are errors in both. The full reference, with the same structure for both guards (prerequisites, configuration, running it, commands, state, looking and restoring, warnings):

- **Push Guard:** [docs/push-guard.md](docs/push-guard.md), with the rules and the rule reasoning in [docs/push-guard-rules.md](docs/push-guard-rules.md). Examples: [examples/warden](examples/warden).
- **Backup Guard:** [docs/backup-guard.md](docs/backup-guard.md). Examples: [examples/backup](examples/backup).

### Shared settings

| Key | File | Meaning |
| --- | --- | --- |
| `remote` | repo file | The remote: `ssh://…` or `user@host:path` (SSH), `https://…` or `http://…` (HTTPS), or a local repository as an absolute path or `file://` URL. |
| `credential` | repo file | A file path, never the value: an SSH private key for SSH remotes, a file holding a token for HTTPS remotes. Required for SSH and HTTPS, not allowed for a local remote. Push Guard: write access; Backup Guard: read access only ([rights per platform](#credentials-and-the-rights-they-need)). |
| `credential_username` | repo file | HTTPS only: the user name sent with the token. Default `x-access-token`. |
| `known_hosts` | repo file | SSH only: a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well). Unknown or changed host keys are always refused. |
| `notify.command` | `defaults.yaml` | argv list; gets a warning as JSON on stdin. |
| `state_dir` | `defaults.yaml` | Where the guard keeps its repositories, its JSON-lines log and its locks. Default `<config>/state`. |
| `timeout` | `defaults.yaml` | Push Guard: per push (default `60s`); Backup Guard: per repo and run (default `30m`). |
| `gitleaks.path` / `everref.path` | `defaults.yaml` | The external program: a bare name is looked up on `PATH` (the default), a path is relative to the configuration directory. |

Relative file paths are relative to the configuration directory. The credential never appears in a URL, on a command line or in a log: SSH runs as `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`; for HTTPS an inline credential helper reads the token file when git asks. The Backup Guard passes the same environment to git-everref, so everref's own fetches use the same key or token (tested against a local HTTP server that requires the token).

### Credentials and the rights they need

The Push Guard writes to the remote, the Backup Guard only reads. Give each exactly that, and never reuse the Push Guard's credential for the Backup Guard.

| Credential | Push Guard (`warden.yaml`): read and write | Backup Guard (`backup.yaml`): read only |
| --- | --- | --- |
| GitHub deploy key (SSH) | Deploy key with **Allow write access** | Deploy key without it (deploy keys are read-only by default) |
| GitHub fine-grained personal access token (HTTPS) | Repository permission **Contents: Read and write**; **Metadata: Read-only** is always included | **Contents: Read-only**; **Metadata: Read-only** |
| GitHub App installation token (HTTPS, user name `x-access-token`) | Repository permission **Contents: Read and write** | **Contents: Read-only** |
| GitLab deploy key (SSH) | Deploy key with **Grant write permissions to this key** | Deploy key without it (read-only) |
| GitLab project access token (HTTPS, any non-blank user name) | Scope **`write_repository`**, role **Developer** (pushes to unprotected branches) or **Maintainer** (protected branches) | Scope **`read_repository`**, with a role that may read the code (e.g. **Reporter**) |
| GitLab deploy token (HTTPS) | Not possible: deploy tokens have no scope for pushing to a repository | Scope **`read_repository`**; set `credential_username` to the token's user name (default format `gitlab+deploy-token-<n>`) |

Notes:

- With a GitHub personal access token, GitHub's documentation uses the account's user name as the Git user name; set `credential_username` to it.
- Changes to `.github/workflows` additionally need the **Workflows** permission on GitHub. Leaving it out of the Push Guard's token means GitHub itself refuses workflow changes, even if a rule missed one.
- The remote's own protections (protected branches, rulesets) still apply to the guard's credential.

Sources: GitHub [deploy keys](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys), [personal access tokens](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens), [GitHub App permissions for Git access](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app); GitLab [deploy keys](https://docs.gitlab.com/user/project/deploy_keys/), [project access tokens](https://docs.gitlab.com/user/project/settings/project_access_tokens/), [token scopes](https://docs.gitlab.com/security/tokens/access_token_scopes/), [deploy tokens](https://docs.gitlab.com/user/project/deploy_tokens/), [roles and permissions](https://docs.gitlab.com/user/permissions/).

### Push Guard

```bash
push-guard check-config --config /etc/warden [--remote]
push-guard serve --config /etc/warden --listen 0.0.0.0:8418    # agents push over HTTP
push-guard init-repo --config /etc/warden hermetarium          # or over SSH / a local path
push-guard approve --config /etc/warden hermetarium refs/heads/main 3f2a9c1
push-guard reset-streak --config /etc/warden hermetarium
push-guard replay --config /etc/warden hermetarium --git-dir /path/to/clone --branch main
```

`serve` and `init-repo` start only after a preflight: the wall's `defaults.yaml` loads, `git` is 2.42 or newer, at least one repo is configured, every repo has a rule enabled, and gitleaks 8.x is found for every repo with `CONTENT-SECRET` enabled (the default). The hook repeats the per-repo part on every push and fails closed. `approve` and `reset-streak` are the human's answers to a red push or a yellow streak; `replay` runs the rules over a branch's history without forwarding anything, to tune them before going live. Agents use the guard as their only remote (`git remote set-url origin http://hermit:$TOKEN@warden.internal:8418/hermetarium.git`). Details: [docs/push-guard.md](docs/push-guard.md).

### Backup Guard

```bash
backup-guard check-config --config /etc/warden-backup [--remote]
backup-guard run --config /etc/warden-backup [repo...]     # from a timer, e.g. every 15 minutes
```

`run` starts only after a preflight: the configuration loads (every repo's `backup.yaml` too), `git` is 2.42 or newer, git-everref is found with the major version of `everref.version` (default `1.0.0`), and at least one repo is configured; a failed preflight is logged in `backup.jsonl` and warned. A repo whose previous run is still going is skipped as failed rather than waited for. A run that is killed or times out is picked up cleanly by the next one. Example systemd units: [examples/backup/systemd](examples/backup/systemd). The backup of a repo is `state_dir/repos/<name>/backup.git`; look at it and restore with git-everref's own commands in `state_dir/repos/<name>/bridge`. Restoring to the remote is a human's step. Details: [docs/backup-guard.md](docs/backup-guard.md).

## Limits

- Tested live against GitHub over HTTPS, and over SSH against a local `sshd` the test starts itself (fresh host and user keys, wrong and unknown host key, unauthorized key). GitLab and Gitea are not tested yet.
- `META-UNSIGNED` checks only that a signature is present (`gpgsig` header). The wall has no keyring, so it doesn't verify signatures; with fewer than `lookback` commits of history the rule stays quiet.
- `CONTENT-PAGES-SCRIPT` looks at one added line at a time, so a tag split across lines is missed, and a host counts as known if its name appears anywhere in the old tree.
- `push.jsonl` is read in full on every push; fine for now, it will need rotation or an index later.
- One Push Guard per agent: `agent.name` is per installation, and rate limits count all pushes of that installation.
- No registry checks for new dependencies (slopsquatting, [R17](SPEC.md#4-agent-specific-vectors)) and no AI: the Push Guard is rules only, with no semantic code review (see [what is not covered](SPEC.md#coverage-by-git-warden)).
- The Backup Guard only sees the states present at its runs: a state that exists only between two runs, or history rewritten before the first run, is not in the backup. everref v1.0.0 runs one `ls-remote` per new branch and one push per recorded event, so very large, busy repos are too slow for it for now. LFS objects and submodule targets are not backed up.

## How this project is built

git-warden is developed agent-first. AI coding agents write all code,
tests, and documentation, and review each other's changes, under human
direction: specs, design decisions, and acceptance based on observed
behaviour and test results. No human reads the code line by line. This
is a deliberate choice. Quality rests on automated tests, CI, and
independent agent review.

Evaluate the code against your own requirements before you depend on it.
Found a problem? Open an issue.

## License

[PolyForm Noncommercial 1.0.0](LICENSE). Source-available, not OSI Open Source. Third-party software compiled in or run by Git Warden, and its licenses: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
