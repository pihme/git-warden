# Git Warden

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/git-warden-logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/git-warden-logo-light.svg">
    <img src="docs/assets/git-warden-logo-universal.svg" width="120" alt="Git Warden logo">
  </picture>
</p>

[![CI](https://github.com/pihme/git-warden/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/pihme/git-warden/actions/workflows/ci.yml)
[![License: PolyForm Noncommercial](https://img.shields.io/badge/license-PolyForm%20Noncommercial-blue)](LICENSE)
[![Release](https://img.shields.io/github/v/release/pihme/git-warden?label=release)](https://github.com/pihme/git-warden/releases)
[![Website](https://img.shields.io/badge/website-pihme.github.io%2Fgit--warden-1d7fb3)](https://pihme.github.io/git-warden/)
[![Go](https://img.shields.io/badge/go-%3E%3D1.24-00ADD8?logo=go&logoColor=white)](go.mod)

Git Warden puts guard posts between AI agents and their Git remote: a **Push Guard** that checks every push with deterministic rules, and a **Backup Guard** that keeps an append-only backup of every branch and tag. The remote can be GitHub, GitLab, Gitea/Forgejo or a bare repo over SSH. Neither guard uses AI: every decision is a deterministic rule.

- **Push Guard** (`push-guard`): agents push to it instead of the remote. It checks the push while it is running, and only it holds a write credential for the remote. A clean push is forwarded. A **yellow** finding comes back to the agent with a message it can act on. A **red** finding is rejected without details and waits for a human.
- **Backup Guard** (`backup-guard`): on a timer, on a separate backup host, it records every branch and tag of each remote. A force push, a deletion or a moved tag becomes a new line of history or a tombstone in the backup. A state a run has seen is kept.

Part of [Hobelbank](https://pihme.github.io/hobelbank/), a set of projects on building software in the age of AI.

## Status

Both guard posts are built and covered by tests. The live test in CI pushes to this repository on GitHub over HTTPS.

What each guard does not cover is in [SPEC.md](SPEC.md#coverage-by-git-warden). The handbook is at [pihme.github.io/git-warden](https://pihme.github.io/git-warden/).

## Start here

### Push Guard

| You want to | Read |
| --- | --- |
| Run it. The example refuses every push to `main` of this repository | [Using the Push Guard](docs/push-guard.md#using-it) |
| See why a rule exists | [docs/push-guard-rules.md](docs/push-guard-rules.md) |

### Backup Guard

| You want to | Read |
| --- | --- |
| Run it. The example keeps a backup of this repository | [Using the Backup Guard](docs/backup-guard.md#using-it) |

### Both guards

| You want to | Read |
| --- | --- |
| Install with Docker or Nix, or build from source | [docs/install.md](docs/install.md) |
| See the settings both guards share | [docs/configuration.md](docs/configuration.md) |
| Choose a credential | [docs/credentials.md](docs/credentials.md) |

## How it works

A **wall** is the host the agents do not run on. The Push Guard's configuration and its write credential live there. The agents cannot read or change them.

### Push Guard

```
agent ── git push ──> push-guard (wall) ── checked commits only ──> remote
```

1. The agent pushes to the guard, not to the remote.
2. The guard reads the remote's branches and tags itself. It does not trust the agent's idea of what was there before.
3. Every rule runs. The worst color wins.
   - **Green:** those commits are pushed to the remote, and the agent's push is accepted.
   - **Yellow:** the push is rejected with the rule, the file and the reason. The agent fixes the change and pushes again. You do nothing.
   - **Red:** the push is rejected with only `rejected: waiting for a human`. The new commits are kept. If you set `notify.command` in the guard's `defaults.yaml`, that program is started and receives the warning. You then approve that commit, or you leave it. The steps are in [Using the Push Guard](docs/push-guard.md#using-it).
   - **Internal error:** the push is rejected with `internal error, try again later`. Something the guard needed was missing or the remote could not be reached. This is not a rule violation, and you are not asked to approve it.

### Backup Guard

```
timer ──> backup-guard run (backup host) ── read-only ──> remote
```

1. A timer starts a run.
2. The run checks `git`, git-everref and the configuration before it touches a repository.
3. For each remote it records every branch and tag into an append-only backup.
4. A failed repository is written to the log. If `notify.command` is set in that guard's `defaults.yaml`, the same kind of program is started with the failure. You fix the cause. The next run tries again. There is nothing to approve.

## Requirements

Git Warden does not install or download these programs at runtime. They must already be on the host.

| Program | Version | Needed for | If it is missing |
| --- | --- | --- | --- |
| [Git](https://git-scm.com/) | 2.42 or newer | Both guards | Neither guard starts |
| [gitleaks](https://github.com/gitleaks/gitleaks) | 8.x (CI pins 8.30.1) | The Push Guard's secret scan, on by default | `serve` refuses to start, and every push fails closed. Switch `CONTENT-SECRET` off only on purpose |
| [git-everref](https://github.com/daojyun/git-everref) | v1.0.0 | The Backup Guard | Every backup run fails before it touches a repository |
| [Go](https://go.dev/) | 1.24 or newer | Building from source | Use the release binaries |

Pinned installs, the container image and the Nix flake: [docs/install.md](docs/install.md). Licenses of these programs: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Build and install

```bash
go build -o push-guard ./cmd/push-guard
go build -o backup-guard ./cmd/backup-guard
go test ./...
```

`go test ./...` is offline. Secret-scan tests skip without gitleaks, and the Backup Guard's end-to-end tests skip without git-everref. CI turns those skips into failures.

Releases attach `push-guard-linux-amd64`, `backup-guard-linux-amd64`, `install-everref.sh`, `LICENSE` and `THIRD_PARTY_NOTICES.md`. Install a binary with `install -m 0755 push-guard-linux-amd64 /usr/local/bin/push-guard` (the same for `backup-guard`).

Docker, Nix and the live test: [docs/install.md](docs/install.md).

## Limits

- Tested live against GitHub over HTTPS, and over SSH against a local `sshd` the test starts itself. GitLab and Gitea are not tested yet.
- `META-UNSIGNED` checks only that a signature is present. The wall has no keyring, so it does not verify signatures. With fewer than `lookback` commits of history the rule stays quiet.
- `CONTENT-PAGES-SCRIPT` looks at one added line at a time, so a tag split across lines is missed. A host counts as known if its name appears anywhere in the old tree.
- `push.jsonl` is read in full on every push. That is fine for now and will need rotation or an index later.
- One Push Guard per agent. Rate limits count every push of that installation.
- No registry checks for new dependencies, and no semantic review of the code. See [what is not covered](SPEC.md#coverage-by-git-warden).
- The Backup Guard only sees the states present at its runs. A state that exists only between two runs, or history rewritten before the first run, is not in the backup. Very large, busy repositories are too slow for git-everref v1.0.0. LFS file contents and submodule targets are not backed up. The LFS pointer files in the commits are ordinary Git objects, so those are recorded.

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

[PolyForm Noncommercial 1.0.0](LICENSE). Source-available, not OSI Open Source. Commercial use is not granted. Other licenses can be negotiated with the copyright holder.

Third-party software compiled in or run by Git Warden, and its licenses: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
