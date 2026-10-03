# AGENTS.md

This file is for the coding agent working in this repo. Read it at the start of a session.

## What this repo is

**Git Warden** puts guard posts between AI agents and their Git remote. This monorepo holds all parts; the first is the **Push Guard** (`cmd/push-guard`): agents push to it, it checks every push with deterministic rules and forwards only green pushes to the real remote. Spec and decisions: `SPEC.md`. User docs: `README.md`.

License: **PolyForm Noncommercial 1.0.0** (`LICENSE`). Source-available, not OSI Open Source. Do not relicense to Apache/MIT/GPL.

## How to work here

Go 1.24 (`go.mod`), one dependency (`gopkg.in/yaml.v3`). At runtime: `git` 2.42+ and `gitleaks` 8.x.

- `cmd/push-guard/`: the binary (subcommands `serve`, `pre-receive`, `init-repo`, `approve`, `reset-streak`, `check-config`, `replay`, `version`) and the end-to-end tests.
- `internal/config/`: three config layers and merge; `defaults.yaml` holds the built-in rule defaults (embedded).
- `internal/gitx/`: git subprocess wrapper with timeouts, env passthrough and per-remote credentials.
- `internal/rules/`: the decision core: delta normalisation, all deterministic rules, verdict. No platform knowledge.
- `internal/journal/`: append-only `push.jsonl` and the queries on it (approvals, streak, rate).
- `internal/pushguard/`: pre-receive hook, forwarding, pending bundles, notify, human commands, serve, replay.
- `internal/testutil/`: helpers for tests with real temporary Git repos.
- `examples/warden/`: example wall configuration (placeholder remote and credential).
- Later: `cmd/merge-guard/`, `cmd/pull-guard/`, sharing `internal/rules`.

Invariants that must not break:

- **Fail closed.** Any error, timeout or missing input (scanner, remote, object) rejects the push with `internal error, try again later`. Never forward on doubt.
- **Red stays silent.** A red rejection says only `rejected: waiting for a human (push <id>)`; no rule IDs, paths or reasons reach the agent. Yellow names rule, ref, path, line and why.
- **Forward exactly the checked SHAs**, never `--force`; `--force-with-lease` only for refs whose `REF-NON-FF` / `REF-TAG-MOVE` / `REF-DELETE` was explicitly allowed or whose SHA a human approved.
- **The remote's state is read fresh** for every push; the agent's old-oid is never trusted.
- **Nothing in the pushed repo configures the guard** (scanner config, exceptions, attributes). Configuration lives on the wall only.
- **Credentials stay file references:** never in YAML, logs, journal, command lines or agent output.
- Patterns are anchored (`^(?:…)$`); lists are appended across layers, removed only by exact `*_remove`.

Tests cover the config merge, every rule against real temporary repos, the journal queries, and end to end: a bare remote, the guard repo with the compiled binary as hook, an agent clone pushing by file path and over HTTP (`serve`). The secret-scan tests skip without `gitleaks` on `PATH`; CI installs a pinned, checksum-verified gitleaks and sets `GITLEAKS_REQUIRED=1`, which turns that skip into a failure. `TestLiveRemote` pushes to a real HTTPS remote and only runs in CI's `live` job (or with `WARDEN_LIVE_REMOTE` and `WARDEN_LIVE_TOKEN_FILE` set); it must only ever touch its own `testrun-*` branch.

- Tests: `go test ./...` must pass before anything lands on `main`. Tests stay offline: no real accounts, tokens or live services, except the env-gated `TestLiveRemote`.
- Release paths (only commits touching them can cut a release) are listed in `.github/release.json`.

## Shared rules

### Commits and changes

- [Conventional Commits](https://www.conventionalcommits.org/) for every commit: `feat:`, `fix:`, `perf:`, `docs:`, `test:`, `refactor:`, `chore:`, `ci:`, `build:`, optional scope, `!` for breaking changes.
- **Trivial changes go straight to `main`:** typos, small docs fixes, obvious small bugs, housekeeping. Keep commits small and focused; pull before you commit, CI pushes release commits to `main`.
- **Non-trivial findings become GitHub issues** (with the matching issue form): bugs you do not fix right away, design questions, anything that needs a decision. Do not hide them in a commit.
- **Never force-push**, never rewrite pushed history, never delete branches or tags.
- **No comments, reviews, label changes, closes or merges on other people's issues and PRs** unless the maintainer asked for it in the session. Exception: Dependabot PRs (see below). Opening issues for your own findings and closing them with your own commits (`Closes #n`) is fine.
- **Never write the skip-CI token** (the word `skip` and `ci` in square brackets, or any of its variants) in a commit message, not even quoted or explained: GitHub then skips CI for that push and no release is cut. Only `release.py` puts it in its own release commits.

### Versions and releases

- SemVer, one tag per artifact: `push-guard/vX.Y.Z` (later `merge-guard/…`, `pull-guard/…`).
- `feat:` bumps minor, `fix:`/`perf:` patch, `feat!:`/`fix!:` or a `BREAKING CHANGE:` footer major. **Before 1.0.0 a breaking change bumps the minor version** (0.3.x to 0.4.0, never to 1.0.0). Reaching 1.0.0 is a deliberate decision by the maintainer, not a side effect.
- `docs:`, `test:`, `refactor:`, `chore:`, `ci:`, `build:` never bump. A commit only counts when it touches a release path.
- **Release 1.0** (or any chosen version): an empty commit with the footer `Release-As: 1.0.0`, e.g. `git commit --allow-empty -m "chore: release 1.0" -m "Release-As: 1.0.0"`. With several artifacts in `.github/release.json`, name one per footer line: `Release-As: <name>@1.0.0` (the bare form is then ignored). It releases by itself, regardless of type or paths; upwards only (at or below the current version it is ignored with a warning); several footers: the highest wins. Only use it when the maintainer decided the version.
- After CI passes on a push to `main`, `.github/scripts/release.py` computes the version, updates the version file if there is one (commit `chore(release): …` by github-actions), creates the GitHub Release with notes and attaches the build. **Never tag, bump versions or create releases by hand.**

### Dependabot

- Commit prefixes (set in `.github/dependabot.yml`): runtime dependencies and shipped Docker base images `fix(deps):` (patch release), dev dependencies `chore(deps-dev):`, GitHub Actions `ci(deps):`, images that are not shipped (examples, tests) `chore(deps):`.
- **Never turn a dependency update into `feat!:`** or reword its title. A major dependency update is still `fix(deps)` / `chore(deps-dev)`. If it forces a breaking change on users (for example a new minimum runtime), that is a separate, deliberate commit after the maintainer decides.
- **Merge Dependabot PRs when CI is green** (squash, keep the Dependabot title). If one conflicts, comment `@dependabot rebase`. If CI is red and the fix is not obvious, leave the PR open and open an issue.

### Secrets

- Never commit, print, log or paste tokens, API keys, credentials, `.env` files, session files or real service responses: not in code, tests, fixtures, issues, PRs or commit messages. Use synthetic fixtures.
- Security problems are reported privately (`SECURITY.md`). Do not discuss an unfixed vulnerability in a public issue.

### Contributions

- Outside pull requests are not accepted (see `CONTRIBUTING.md`); issues are.
- `CONTRIBUTING.md` holds **only content for outside people**: license, how to report, how to build and test. Internal working rules (this file) never go there.

### Website

No website yet (private repo).

## Do not invent

- **No AI judge in the Push Guard.** Rules only; the judge belongs to the Merge Guard and Pull Guard.
- **No platform API in the Push Guard or the core.** Plain Git only; no GitHub/GitLab clients, no rules for protected branches (the remote's job).
- **No author/committer identity rules**; agent identity comes from the push credential.
- **No signature verification on the wall** (`META-UNSIGNED` checks presence only).
- **No `refs/warden/pending`**: red pushes are stored as bundles (quarantine objects vanish, refs can't be written there).
- **No registry or network checks** during a push (R17 comes later as its own rule source), and no trufflehog for now.
- **No database** for the Push Guard: `push.jsonl` plus Git.
- **No approvals through GitHub issues or the agent's channel**; humans act on the wall host.
- Don't rename the binaries to `git-warden` (the name is taken on npm, PyPI and crates.io).

## Agent skills

### Issue tracker

GitHub issues in `pihme/git-warden`, via `gh`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default roles: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`, plus `bug` / `enhancement`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: decisions in `SPEC.md`, optional root `GLOSSARY.md`. See `docs/agents/domain.md`.
