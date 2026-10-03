# Git Warden

Git Warden puts guard posts between AI agents and their Git remote: a **Push Guard** that checks every push with deterministic rules, and a **Pull Guard** that keeps an append-only backup of every branch and tag. The remote can be GitHub, GitLab, Gitea/Forgejo or a bare repo over SSH. Neither guard uses AI: every decision is a deterministic rule.

- **Push Guard** (`push-guard`): agents push to it instead of the remote, it checks every push while the push is running, and only it holds a write credential for the remote. A clean push is forwarded with exactly the checked SHAs; a `yellow` finding goes back to the agent with a message it can act on; a `red` finding is rejected without details and waits for a human.
- **Pull Guard** (`pull-guard`): on a timer on a separate backup host, it runs [git-everref](https://github.com/daojyun/git-everref) against each remote with a read-only credential. A force push, deletion or moved tag upstream becomes a new lineage or a tombstone in the backup; nothing that a run has seen is ever lost.

## Status

Both guard posts are built. The Push Guard (`push-guard`): all rules of the spec, the pre-receive hook, the HTTP server, the human commands and `replay`, covered by offline tests against real temporary Git repositories, plus a live test in CI against GitHub over HTTPS and an SSH test against a local `sshd` (see [Limits](#limits)). The Pull Guard (`pull-guard`): preflight, bridge and backup setup, branch selection and the git-everref run, covered by tests with a stand-in everref and end to end with the real git-everref against a local remote (force push, deletion, moved tag, unreachable remote). Neither is **in production use yet**.

Documentation: [SPEC.md](SPEC.md) is the one authoritative document (design of both guard posts, decisions, the risk register and [what Git Warden does **not** cover](SPEC.md#coverage-by-git-warden)). Reference: [docs/push-guard-rules.md](docs/push-guard-rules.md) (reasoning per Push Guard rule) and [docs/pull-guard.md](docs/pull-guard.md) (Pull Guard configuration and operation).

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

### Pull Guard

```
timer ──> pull-guard run (backup host) ── read-only ──> remote
            │ preflight: git, git-everref (version), config
            ├ repos/<name>/bridge      ls-remote, git-everref add, git-everref run --all
            ├ repos/<name>/backup.git  append-only: lineages, tombstones, journals
            └ pull.jsonl               one line per repo and run
```

1. **Preflight:** the configuration loads, `git` and `git-everref` are found and `git-everref --version` answers (and matches the pinned version if one is configured), at least one repo is configured. Otherwise the run fails closed before anything is touched.
2. Per repo it lists the remote's branches, protects new ones with `git-everref add origin/<branch> --remote backup` (minus `exclude_branches`), and runs `git-everref run --all`, which fetches the remote and records every protected branch and all tags.
3. A failed repo (remote unreachable, a branch everref refuses, a non-zero everref exit, a timeout) is logged in `pull.jsonl` and warned through `notify.command`; the other repos still run.

## Requirements

Git Warden calls a few external programs. **It doesn't install or download any of them at runtime**: they must already be on the host, found on `PATH` or at the configured path, and each runs as its own process.

| Program | Version | License | Needed for | If it is missing |
| --- | --- | --- | --- | --- |
| [Git](https://git-scm.com/) | 2.42 or newer | GPL-2.0 | Both guards at runtime: every Git operation, and `git http-backend` for `push-guard serve` | Neither guard starts: both check it in their preflight |
| [gitleaks](https://github.com/gitleaks/gitleaks) | 8.x (CI pins 8.30.1) | MIT | Push Guard at runtime: the secret scan. Must be on `PATH`, or at the path set as `scanner.gitleaks` | **Required** for the Push Guard (unless `CONTENT-SECRET` is disabled for every repo): `serve` and `init-repo` refuse to start, every push fails closed (`internal error`), and `check-config` reports it. Optional for the tests: the secret-scan tests are skipped |
| [git-everref](https://github.com/daojyun/git-everref) | v1.0.0 (pinned) | MIT | Pull Guard at runtime: records branches and tags in the backup. Must be on `PATH`, or at the path set as `everref.path` | **Required** for the Pull Guard: every run fails closed in its preflight, and `check-config` reports it. Optional for the tests: the end-to-end tests are skipped |
| [Go](https://go.dev/) | 1.24 or newer | BSD-3-Clause | Building from source only | Use the release binaries |

**Pinning is part of provisioning the host, not something Git Warden does at runtime.** Install gitleaks and git-everref at a fixed version and check the download's SHA-256 when you set up the host, as CI does. For git-everref there is an optional script that does exactly that: [scripts/install-everref.sh](scripts/install-everref.sh) installs v1.0.0 for Linux amd64/arm64 and checks the tarball against SHA-256 values pinned in the script ([details](SPEC.md#installing-git-everref)). The [container image](#docker) and the [Nix flake](#nix) bring git, gitleaks 8.30.1 and git-everref v1.0.0 along, pinned the same way. Licenses of these programs and of the code compiled into the binaries: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Build and install

```bash
go build -o push-guard ./cmd/push-guard
go build -o pull-guard ./cmd/pull-guard
scripts/install-everref.sh   # optional: pinned git-everref v1.0.0 into ~/.local/bin (SHA-256 checked)
go test ./...      # offline; secret-scan tests skip without gitleaks, everref end-to-end tests without git-everref
```

CI installs pinned gitleaks and git-everref and sets `GITLEAKS_REQUIRED=1` and `EVERREF_REQUIRED=1`, which turn those skips into failures.

The live test (`TestLiveRemote`) runs only when `WARDEN_LIVE_REMOTE` (an `https://` URL) and `WARDEN_LIVE_TOKEN_FILE` are set. It pushes through a real guard to a fresh `testrun-<UTC timestamp>-<run>` branch on that remote: green create and fast-forward, red rewrite that leaves the remote alone, approval of that SHA going out with a lease, another writer moving the branch, and finally an allowed delete. CI runs it against this repository on pushes to `main` with the job's own token, and deletes leftover `testrun-*` branches older than a day. `TestSSHRemote` starts its own `sshd` (skipped if `sshd` or `ssh-keygen` is missing; CI installs it and sets `SSH_REQUIRED=1`).

Releases attach static `push-guard-linux-amd64` and `pull-guard-linux-amd64` binaries, `install-everref.sh`, `LICENSE` and `THIRD_PARTY_NOTICES.md`. Install a binary with `install -m 0755 push-guard-linux-amd64 /usr/local/bin/push-guard` (the same for `pull-guard`).

## Docker

The [Dockerfile](Dockerfile) builds one image with both guards and their prerequisites, provisioned at pinned versions: static `push-guard` and `pull-guard` (built with Go 1.24), Debian trixie's `git` 2.47 (the build fails below 2.42) with `openssh-client`, gitleaks 8.30.1 (the CI release, SHA-256 checked) and git-everref v1.0.0 (via [scripts/install-everref.sh](scripts/install-everref.sh), SHA-256 checked). Base images are pinned by digest. It runs as the unprivileged user `warden` (uid 10001) and contains no configuration and no secrets. The image isn't published; build it yourself:

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

**Pull Guard**, from a timer on the backup host (for example the [systemd units](examples/pull/systemd) with this as `ExecStart`):

```bash
docker run --rm \
  -v /etc/warden-pull:/etc/warden-pull:ro \
  -v warden-pull-state:/var/lib/warden-pull \
  git-warden pull-guard run --config /etc/warden-pull
```

In the container:

- Set `state_dir: /var/lib/warden` (Push Guard) or `/var/lib/warden-pull` (Pull Guard) in `defaults.yaml` and keep it on a volume; the [examples](examples) already do. The configuration mount stays read-only.
- Every file the configuration points to (`agent.token_file`, `credential`, `known_hosts`, `gitleaks.toml`) must be inside the mounted directory and readable by uid 10001. Give SSH remotes a `known_hosts` file there, since the container user has none of its own.
- gitleaks is at `/usr/local/bin/gitleaks` and git-everref at `/usr/local/bin/git-everref`, both on `PATH`, so `scanner.gitleaks` and `everref.path` can stay unset (or keep the examples' values).
- `notify.command` runs inside the container: mount it in as well (the image has `sh`, but no mail or HTTP client besides `git`).
- The guard repositories in the state volume have hooks that call `/usr/local/bin/push-guard`, so keep using this image with that volume.

The same preflight runs at start as on a host: without a configuration, repos, an enabled rule or gitleaks, `serve` exits with the reason instead of starting.

## Nix

[flake.nix](flake.nix) (nixpkgs pinned in [flake.lock](flake.lock)) gives a reproducible build and development environment on Linux (x86_64, aarch64). It needs flakes enabled (`experimental-features = nix-command flakes`).

```bash
nix build          # ./result/bin/push-guard and ./result/bin/pull-guard; runs go test ./... in the sandbox
nix develop        # shell with Go, git, OpenSSH, gitleaks 8.30.1 and git-everref v1.0.0
nix flake check
```

gitleaks and git-everref come from their pinned release tarballs with the same SHA-256 as CI and the Dockerfile, not from nixpkgs, so every environment runs the same versions. Git Warden's license (PolyForm Noncommercial) counts as unfree in Nix; the flake allows exactly this package. When you change `go.mod` or `go.sum`, update `vendorHash` in `flake.nix` (Nix prints the new value).

## Configuration (Push Guard)

Everything lives on the wall, never in a repo. One folder:

```
warden/
  defaults.yaml          # applies to every repo: agent, notify, state_dir, rule changes
  gitleaks.toml          # optional: scanner config for every repo
  gitleaksignore         # optional: known false positives for every repo
  repos/
    hermetarium/         # folder name = repo name = the path the agent pushes to
      warden.yaml        # required: remote + credential, plus what differs here
      gitleaks.toml      # optional: per-repo scanner config (use [extend] path = ".../gitleaks.toml")
      gitleaksignore     # optional
```

See [examples/warden](examples/warden). A repo file:

```yaml
remote: git@github.com:example/hermetarium.git  # any Git URL or a local path
credential: /etc/warden/keys/hermetarium        # SSH key, or token file for https://
known_hosts: /etc/warden/known_hosts           # optional, SSH only; default: the guard user's own
default_branch: main                            # optional; otherwise ls-remote --symref HEAD
rules:
  PATH-YELLOW:
    allow: ['package-lock\.json']
```

**Layers.** The built-in defaults ([internal/config/defaults.yaml](internal/config/defaults.yaml), compiled in) come first, then the wall's `defaults.yaml`, then the repo's `warden.yaml`. Scalars and limits override. The lists `match`, `allow` and `deny` are appended to, so a default can't vanish unnoticed; `match_remove`, `allow_remove` and `deny_remove` remove an exact entry from the layers below (an entry that isn't there is an error). Unknown keys and rule IDs are errors.

**Repo settings.** `remote` is required and `credential` too, except for a local path or `file://` remote. Both only come from the repo's file. The credential is a file path, never a value: for `ssh://` and `user@host:path` remotes an SSH private key (used with `-i` and `IdentitiesOnly=yes`; the host must be in the guard user's `known_hosts`), for `https://` a file holding a token (sent as password with the user name `x-access-token`). Optional `known_hosts` (SSH remotes only) points to a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well); either way an unknown or changed host key is refused (`StrictHostKeyChecking=yes`). `ssh` runs with `-F none`, so no ssh_config on the wall host (`ProxyCommand`, `HostName`, other keys) can change where or how the guard connects.

**Wall settings** (`defaults.yaml` only): `agent.name` (required; in every log entry and warning), `agent.token_file` (password for `serve`), `notify.command` (argv list; gets the warning JSON on stdin), `state_dir` (default `<config>/state`), `pages_branch` (default `gh-pages`), `scanner.gitleaks`, `timeout` (per push, default `60s`), `forward.atomic` (default `true`).

**Rules.** Each rule has `enabled` (default `true`), `color` (`red` or `yellow`), `allow` and `deny` (regular expressions over the rule's subject) and its limits. Patterns always match the whole subject (they are wrapped as `^(?:…)$`). `deny` fires even if `allow` matches; `REF-NAMESPACE` looks at every pushed ref and `PATH-*` at every changed path, so a `deny` there protects a ref or path outright. Subjects: the full ref name for `REF-*`, the path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*` limits, the ref name for per-ref `SIZE-*` totals, the branch name for `META-*`; `REF-COUNT` and `RATE-*` have no subject.

| Rule | Default | Fires on |
| --- | --- | --- |
| `REF-DELETE` | red | a branch or tag is deleted |
| `REF-NON-FF` | red | a branch moves to a commit that doesn't contain the remote's old commit |
| `REF-TAG-MOVE` | red | an existing tag is moved |
| `REF-TAG-NEW` | yellow | a new tag |
| `REF-NAMESPACE` | red | a ref outside `refs/heads/*` and `refs/tags/*` |
| `REF-COUNT` | red | more than `max_refs` (10) refs in one push |
| `PATH-RED` / `PATH-YELLOW` | red / yellow | a changed path (both names of a rename) matches the rule's `match` list |
| `MODE-EXEC` / `MODE-SYMLINK` / `MODE-SUBMODULE` | yellow | a file becomes executable; a symlink or gitlink is added or changed |
| `CONTENT-SECRET` | red | gitleaks finds something in any new commit |
| `CONTENT-SCANNER-ALLOW` | red | an added `gitleaks:allow` or `trufflehog:ignore` |
| `CONTENT-INVISIBLE` | red | bidi controls, zero-width characters, U+FEFF other than at the very start of a file, Unicode tag characters |
| `CONTENT-BINARY` | yellow | a binary file (or text that isn't UTF-8) is added or changed |
| `CONTENT-BLOB` | yellow | 200+ characters of base64 or 100+ of hex on one added line |
| `CONTENT-PAGES-SCRIPT` | yellow | on the Pages branch: `<script src>` or `<link href>` to a host not found in the old tree |
| `META-UNSIGNED` | yellow | an unsigned commit where the last `lookback` (20) commits of the branch carry a signature |
| `META-BACKDATED` | yellow | committer date before a parent's, or more than `max_age` (24h) before the push |
| `META-FUTURE` | yellow | author or committer date more than `max_skew` (10m) after the push |
| `SIZE-LIMIT` | red | over 2,000 files, 50,000 lines, 50 MB of new objects or a 10 MB file |
| `SIZE-LARGE` | yellow | over 200 files, 5,000 lines, 100 commits or a 1 MB file |
| `SIZE-MASS-DELETE` | yellow | more than 20 files deleted, or more than 50 % of a file over 100 lines removed |
| `RATE-LIMIT` | red | more than 30 pushes per hour (answer: `rate limited, try later`) |
| `RATE-YELLOW-STREAK` | red | more than 5 yellow rejections in a repo within 24 h; then every push is red until a human approves a SHA or resets |

The full reasoning per rule is in [docs/push-guard-rules.md](docs/push-guard-rules.md).

**Secret scanner.** gitleaks runs as `gitleaks git --log-opts "<new> --not <remote refs>"` with `--config` (the repo folder's `gitleaks.toml`, else the wall's, else a generated one that extends the gitleaks defaults), `--gitleaks-ignore-path` (the repo folder's `gitleaksignore`, else the wall's, else an empty file) and `--ignore-gitleaks-allow`, so nothing in the pushed repo can switch it off. Findings are redacted.

Check a configuration with `push-guard check-config --config /etc/warden` (add `--remote` to also run `ls-remote` against every remote with its credential).

## Running the Push Guard

Both ways start with a preflight: `serve` and `init-repo` refuse to start, naming every problem, if the wall's `defaults.yaml` is missing or doesn't load, `git` is older than 2.42, no repo is configured, a repo has every rule disabled, or the gitleaks a repo needs for `CONTENT-SECRET` is missing or isn't 8.x. The hook repeats the per-repo part on every push and fails closed (`internal error, try again later`). `check-config` reports the same problems.

**Over HTTP** (the usual setup):

```bash
push-guard serve --config /etc/warden --listen 0.0.0.0:8418
```

`serve` answers only Git smart HTTP routes `/<name>.git/…` through `git http-backend`, behind HTTP basic auth with the password from `agent.token_file` (any user name). It creates the guard's bare repository `state_dir/repos/<name>.git` on first use with the hook installed, and syncs it from the remote (branches and tags, with prune) before every ref advertisement, so agents fetch through the guard as well. Put TLS in front of it if the traffic leaves the host.

**Over SSH or a local path:** `push-guard init-repo --config /etc/warden hermetarium` creates and syncs the guard repository; agents then push to that path, e.g. over SSH with `git-shell`. Without `serve` nothing re-syncs it automatically; the hook still reads the remote fresh for every push.

**Agents** use the guard as their only remote:

```bash
git remote set-url origin http://hermit:$TOKEN@warden.internal:8418/hermetarium.git
git push origin agent/fix-typo
```

The agent must not have any other write credential for the remote, otherwise the guard is decoration.

## Running the Pull Guard

Its own configuration directory, usually on the backup host (see [examples/pull](examples/pull) and [docs/pull-guard.md](docs/pull-guard.md)):

```
warden-pull/
  defaults.yaml          # optional: everref.path, everref.version, notify.command, state_dir, timeout
  repos/
    hermetarium/
      pull.yaml          # remote, read-only credential, optional known_hosts and exclude_branches
```

```yaml
# defaults.yaml
everref:
  version: v1.0.0                        # refuse any other installed version
notify:
  command: [/usr/local/bin/warden-notify]
state_dir: /var/lib/warden-pull
```

```yaml
# repos/hermetarium/pull.yaml
remote: git@github.com:example/hermetarium.git
credential: /etc/warden-pull/keys/hermetarium   # read-only deploy key
exclude_branches: ['dependabot/.*']
```

```bash
pull-guard check-config --config /etc/warden-pull [--remote]
pull-guard run --config /etc/warden-pull [repo...]     # from a timer, e.g. every 15 minutes
pull-guard version
```

Exit codes: `0` every repo backed up, `1` a repo or the preflight failed, `2` usage error. Example systemd units: [examples/pull/systemd](examples/pull/systemd). The backup of a repo is `state_dir/repos/<name>/backup.git`; look at it and restore with git-everref's own commands in `state_dir/repos/<name>/bridge` (`git-everref -C <bridge> status --all`, `log`, `restore`). Restoring to the remote is a human's step.

## Human commands (Push Guard)

Run on the wall host; being able to run them there is the authentication.

```bash
push-guard approve --config /etc/warden hermetarium refs/heads/main 3f2a9c1   # SHA prefix of a rejected push is enough
push-guard reset-streak --config /etc/warden hermetarium
push-guard replay --config /etc/warden hermetarium --git-dir /path/to/clone --branch main [--skip-scanner] [-v]
push-guard version
```

- `approve`: the agent's next push of exactly that SHA to that ref is forwarded without re-checking (also with a lease if it rewrites or deletes the ref). It ends an active yellow streak. The approval is logged with the rule IDs of the overruled push. Any other SHA is checked from scratch.
- `reset-streak`: ends a `RATE-YELLOW-STREAK` without approving anything.
- `replay`: treats every first-parent step of a branch's history as one push (with the commit's committer time as the time of the push), forwards nothing, writes nothing, and prints the hits per rule. Use it to tune the defaults before going live.

A red push's commits are in `state_dir/pending/<repo>/<id>.bundle`; inspect them with `git fetch <bundle> 'refs/*:refs/pending/*'` in a scratch clone.

## Files on the wall (Push Guard)

- `state_dir/push.jsonl`: one JSON line per push (time, id, agent, repo, updates with remote old, new and kind, findings, verdict, forwarded, remote message) and per approval, reset and streak. Append-only. A malformed line makes every push fail closed until a human fixes it.
- `state_dir/pending/<repo>/<id>.bundle`: new commits of red pushes, kept for now.
- `state_dir/repos/<name>.git`: the guard repositories.
- `state_dir/error.log`: errors that could not go into `push.jsonl`.

## Warnings

`notify.command` gets a JSON object on stdin for red pushes (`red_push`), the start of a yellow streak (`yellow_streak`), the first push of a rate-limited series (`rate_limited`) and pushes to an unknown repo (`unknown_repo`). Facts come first: updates, findings with rule IDs, the bundle path and the `approve` commands. The agent's commit messages only appear under `untrusted`, truncated and marked as an unverified quote.

The Pull Guard sends `pull_failed` (repo, error, everref's exit code, the tail of its output) when a repo's run fails; see [docs/pull-guard.md](docs/pull-guard.md#warning).

## Limits

- Tested live against GitHub over HTTPS, and over SSH against a local `sshd` the test starts itself (fresh host and user keys, wrong and unknown host key, unauthorized key). GitLab and Gitea are not tested yet.
- `META-UNSIGNED` checks only that a signature is present (`gpgsig` header). The wall has no keyring, so it doesn't verify signatures; with fewer than `lookback` commits of history the rule stays quiet.
- `CONTENT-PAGES-SCRIPT` looks at one added line at a time, so a tag split across lines is missed, and a host counts as known if its name appears anywhere in the old tree.
- `push.jsonl` is read in full on every push; fine for now, it will need rotation or an index later.
- One Push Guard per agent: `agent.name` is per installation, and rate limits count all pushes of that installation.
- No registry checks for new dependencies (slopsquatting, [R17](SPEC.md#4-agent-specific-vectors)) and no AI: the Push Guard is rules only, with no semantic code review (see [what is not covered](SPEC.md#coverage-by-git-warden)).
- The Pull Guard only sees the states present at its runs: a state that exists only between two runs, or history rewritten before the first run, is not in the backup. everref v1.0.0 runs one `ls-remote` per new branch and one push per recorded event, so very large, busy repos are too slow for it for now. LFS objects and submodule targets are not backed up.

## License

[PolyForm Noncommercial 1.0.0](LICENSE). Source-available, not OSI Open Source. Third-party software compiled in or run by Git Warden, and its licenses: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
