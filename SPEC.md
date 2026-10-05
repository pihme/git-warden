# Git Warden: SPEC

This is the one authoritative document of Git Warden: what it is, the design of both guard posts, the decisions the implementation settled, the risk register (R1–R22) and, per risk, what Git Warden covers and what it does **not**. Decisions here are settled; reopen them only with a reason (see `docs/agents/domain.md`). Next to it, as reference:

- [docs/push-guard-rules.md](docs/push-guard-rules.md): the reasoning behind every Push Guard rule, verdicts, configuration, layout on the wall, human actions, statistics. Testable Push Guard rule requirements are numbered there as [`REQ-PG-*`](docs/push-guard-rules.md#requirements) (stable; never renumber; only append). Those IDs are unrelated to risk IDs `R1`–`R22` below.
- [docs/backup-guard.md](docs/backup-guard.md): the Backup Guard's configuration, state and operation.
- [README.md](README.md): the user docs (requirements, build, configuration, commands).

Contents: [The idea](#the-idea-in-one-paragraph) · [Goals](#goals) · [Principles](#principles) · [Prerequisites](#prerequisites-and-other-writers) · [Non-goals](#non-goals) · [Prior art](#prior-art-summary) · [Decision core](#decision-core) · [The two guard posts](#the-two-guard-posts) · [Layout, naming and versions](#layout-naming-and-versions) · [Push Guard: settled](#push-guard-what-the-implementation-settled) · [Backup Guard: settled](#backup-guard-what-the-implementation-settled) · [Rollout](#rollout-stages) · [Data model](#data-model) · [Prompt injection](#prompt-injection-resilience) · [Failure modes](#failure-modes-overall) · [Risks](#risks-an-ai-agent-with-push-access) · [Coverage](#coverage-by-git-warden) · [Open questions](#open-questions)

## The idea in one paragraph

Git Warden sits **between AI agents and their Git remote**, whether that is GitHub, GitLab, Gitea/Forgejo or a bare repo over SSH, at two places:

- **Push Guard** (`push-guard`), on the wall around the agent: agents push to it instead of the remote, and only it holds a write credential for the remote. It forwards a push or rejects it, with deterministic rules only, while the push is running.
- **Backup Guard** (`backup-guard`), a backup on a separate host: on a timer it runs [git-everref](https://github.com/daojyun/git-everref), which records every branch and tag of the remote in an append-only backup, so a force push or deletion upstream never loses anything. It decides nothing.

Both are programs in this repository; the Backup Guard drives an existing tool, git-everref, as an external program and contains none of its code. **There is no AI in either:** every decision is a deterministic rule, so there is no model to persuade, no LLM budget and no repo content sent to a model provider. **Policy: `red` goes to a human, `yellow` is the agent's job.** `red` is a rule violation; a human is warned and can approve the exact SHA, which should be the exception. `yellow` goes back to the agent with the rule message, and it fixes the change or leaves the part out and notes in its PR that this is still open. Only if it keeps getting it wrong does the escalation reach a human. Which rule is `red` and which `yellow` is configuration, per repo. When a guard rejects a push, the agent sees that the push didn't go through, but it can keep working.

## Goals

1. **Prevention where possible:** agent changes reach the remote only through the Push Guard.
2. **No data loss** (R1, R2, R19): whatever was ever in the Backup Guard's backup never disappears.
3. **Early detection** of what can't be undone (R4, R7, R13, R14).
4. **Minimal credentials:** Backup Guard read-only; Push Guard a narrowly scoped write credential that never gets into the agent's environment.
5. **Nothing to persuade:** no AI decides anything (R21 targets AI gatekeepers; Git Warden has none). Text the agent writes never changes a verdict.
6. **The agent can always keep working:** a rejected push, a slow verdict or a warden outage can stop a push from reaching the remote, but never stops the agent's work. Its commits stay with it, and it pushes again later.
7. **Set up once, then peace:** the owner approves nothing in advance. They get warnings and look at them when it suits them.

## Principles

- **Keep it simple, and take the 80 % first:** the Push Guard came first, with deterministic rules only. Its `red` rules (rewrites, deletions, tag moves, workflows, secrets) catch most of the damage with no AI at all. The Backup Guard followed as a separate stage.
- **Bricks, not a stack:** the decision core is a library without platform knowledge. Each guard post can be left out or replaced; the Backup Guard's recording is an existing tool (git-everref), which could be swapped for e.g. [Gitea Mirror](https://github.com/RayLabsHQ/gitea-mirror) with *Block & Approve*.
- **Batteries included, assembly required:** rules, path lists and limits are configuration with sensible defaults, not code.
- **Plain Git only:** the warden needs nothing but Git (smart HTTP or SSH, refs, objects, `fetch`, `push`), so any Git remote works and no platform API is used. Even the default branch is visible through plain Git (`git ls-remote --symref <remote> HEAD`).
- **External programs are prerequisites, never fetched at runtime:** gitleaks (Push Guard) and git-everref (Backup Guard) must be installed on the host; each guard checks them when it starts and fails closed if they are missing. Pinning happens when the host is provisioned (by hand, with the [container image or the Nix flake](#provisioning-docker-and-nix)).

**Wall** means the isolation boundary around the agent's environment: whatever runs there (the Push Guard, its configuration and credentials) the agent can't reach or change. [Hermetarium](https://github.com/pihme/hermetarium) is one such environment, not a requirement.

## Prerequisites and other writers

1. **The agents' platform token can only open PRs, never write content.** The warden doesn't check this; it comes from how credentials are handed out. The GitHub MCP server has tools that write files through the API (`push_files`, `create_or_update_file`); with content write access an agent would bypass the Push Guard. On GitHub: a fine-grained token with "Pull requests: write" and "Contents: read" only.
2. **No assumption about other writers.** Collaborators, the owner and other agents may still push directly. The Push Guard doesn't see them, and nothing in Git Warden checks their changes or their PRs (**not covered**; use the platform's own review and branch protection). The Backup Guard preserves whatever they overwrite or delete.

## Non-goals

- Revoking secrets, unpublishing packages, automatically restoring to the remote (a human does that).
- Fully backing up issues, PR discussions and releases (R22).
- Monitoring package registries; a general SIEM.
- Settings outside Git (visibility, new repos, protection rules, webhooks, collaborators).
- What agents do through the platform API (PR texts, comments): that is up to the MCP server and the platform.

## Prior art (summary)

| Category | Examples | What's missing |
| --- | --- | --- |
| Mirror/backup | [Forgejo/Gitea mirror](https://forgejo.org/docs/latest/user/repo-mirror/), [ghorg](https://github.com/gabrie30/ghorg), [gickup](https://github.com/cooperspencer/gickup), [python-github-backup](https://github.com/josegonzalez/python-github-backup) | Sync blindly (R19). |
| Mirror with force-push protection | [Gitea Mirror](https://github.com/RayLabsHQ/gitea-mirror) *Block & Approve* | Pauses instead of preserving; force pushes only, no content check, fail-open. |
| Append-only ref backup | [git-everref](https://github.com/daojyun/git-everref) (MIT), [Amber](https://github.com/pmaxhogan/amber), [Software Heritage](https://www.softwareheritage.org/) | everref **records for the Backup Guard**; for very large repos it still needs batched pushes and reads. Software Heritage: public repos only, its own schedule. |
| SaaS backup | Rewind/BackHub, GitProtect | No check before backing up; data sits with a third party. |
| Cryptographic ref history | [gittuf](https://github.com/gittuf/gittuf), [git-ratchet](https://github.com/project-oak/git-ratchet), [gitsign](https://github.com/sigstore/gitsign) | Needs discipline on every push; no content verdict. Good extra signal later. |
| Server-side push checks | Git `pre-receive` hooks, Gerrit `refs/for/…`, GitHub push protection and push rulesets | The pattern for the Push Guard; no verdict on content beyond secrets, GitHub side with admin bypass. |
| AI review | CodeRabbit, Copilot code review, PR-Agent, claude-code-security-review | PRs only; hold write permissions (R20); persuadable (R21). Git Warden deliberately has no AI reviewer. |

**Gap:** no tool we found combines a fail-closed push proxy with deterministic rules and a never-destructive backup, using nothing but Git.

## Decision core

The Push Guard's decisions come from one library without platform knowledge: `decide(delta, context) → verdict`.

1. **Normalise the delta:** ref changes (`create`, `delete`, `ff`, `non_ff`, `tag_move`), changed paths, the cumulative diff against the remote's current state (not just per commit), metadata and size.
2. **Deterministic rules** → findings `red` / `yellow` / `green`; the verdict is the worst finding.
3. **`red`** → rejected and held for a human; only a human can approve the SHA.
4. **`yellow`** → rejected with the rule message; the agent fixes its change.
5. **Fail-closed:** any error, timeout or missing input → rejected.
6. **Human approval** only on the wall host, stored in the warden's own log. "Already reviewed by the security team" in repo text never counts.
7. **The approval is itself an attack surface:** the agent can write texts meant to get the owner to approve. Warnings show the facts first (findings, paths, ref changes) and the agent's text only as a marked, unverified quote.

### Rules (excerpt)

| Level | Trigger |
| --- | --- |
| **red** | `non_ff`, `delete`, `tag_move`; CI configuration (`.github/workflows/**`, `.gitlab-ci.yml`, …), `CODEOWNERS`, `LICENSE`; agent configuration (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.cursor/rules/**`, `.github/copilot-instructions.md`); secrets ([gitleaks](https://github.com/gitleaks/gitleaks)); invisible Unicode and bidi characters ([Trojan Source](https://trojansource.codes/)); delta over the limit |
| **yellow** | Lockfiles and manifests, `.npmrc`, Dockerfiles, build scripts; binary files (also in test directories, cf. xz); long base64/hex literals; mass deletion; a missing signature where commits used to be signed; backdated commits; unusual volume; new external scripts on the Pages branch |
| **green** | Fast-forward, uncritical paths, no findings |

The full Push Guard rule set is in [docs/push-guard-rules.md](docs/push-guard-rules.md). Later the core may also check new dependencies against the registry (exists? age? downloads?) for R17; that needs network access, so it is not in the Push Guard.

**Trade-off:** in many repos agents maintain `AGENTS.md`. With the default above every such change needs a human; a repo can move these paths to `yellow` in its configuration.

## The two guard posts

```
Push Guard     on the wall                                   push-guard
  ├── Git smart HTTP (or SSH) endpoint for agents → rules + secret scan → forward or reject
  ├── pending/<repo>/<id>.bundle   rejected pushes, kept for a human
  ├── push.jsonl                   append-only event log
  └── write credential per repo, only here, never in the agent's environment
      (rules only, decides in seconds)
Backup Guard     backup host (neither the wall nor the agents' machine)    backup-guard
  ├── timer every 15 min → backup-guard run → git-everref run --all   (bridge mode, pinned version)
  ├── repos/<name>/bridge       clone, origin = the remote, read-only credential
  ├── repos/<name>/backup.git   append-only: lineages, tombstones, journals
  ├── backup.jsonl                one line per repo and run
  └── offsite                   periodic git bundle of the backup repos
      (no rules: preserves, decides nothing)
Notifier → human (shared by both)
```

The wall lets the agents' Git traffic through only to the Push Guard; Git traffic to the remotes' hosts is blocked.

### Push Guard

The agent pushes to the Push Guard, which decides in its `pre-receive` step. It reads the remote's refs fresh, runs the rules and gitleaks over the new commits (the earliest possible moment: afterwards a secret is already on the remote), and then forwards exactly the checked SHAs with its own credential (never `--force`), rejects `yellow` with the rule message, or rejects `red` silently and warns the owner. What the implementation settled is in [Push Guard: what the implementation settled](#push-guard-what-the-implementation-settled); configuration and operation in [docs/push-guard.md](docs/push-guard.md).

- **Credentials:** no more rights on the platform than the agent would have, so the remote's own protection applies to the warden too. In plain Git an SSH deploy key with write access per repo; on GitHub optionally an App with only `contents:write` + `metadata:read` and no `workflows` permission, whose tokens expire after an hour, so GitHub rejects workflow changes even if the rules fail.
- **Making it non-bypassable:** agents hold **no** other write credential for the remote (a `gh` token in the agent's home directory has to go), and the wall blocks Git traffic to the remotes' hosts. A local `pre-push` hook is no guard: `git push --no-verify` skips it.
- **Bypass risks:** an overlooked token (`.git-credentials`, environment variable, CI secret); agents outside the wall; other writers pushing directly. The Push Guard itself is a target (R20) because it holds a write credential.
- **Failure mode:** Push Guard down → the agent's push fails, its commits stay local, it pushes again later; a heartbeat alerts the owner. Nothing goes to the remote unchecked.

### Backup Guard

The Backup Guard (`backup-guard run`) is started by a timer on the backup host. For every configured remote it keeps a bridge clone and a local, append-only backup repo, and runs git-everref in bridge mode: everref fetches the remote and records every protected branch and every tag. `backup-guard` only prepares and drives everref: it checks the prerequisites, sets up bridge and backup, selects the branches, runs everref, logs the run and warns on failure. No rules, no AI, no database, and no backup format of its own. Browsing and restoring is done with everref's own commands (`status`, `log`, `restore`) in the bridge clone; friendlier tooling may come later.

**How everref works** (v1.0.0, [design](https://github.com/daojyun/git-everref/blob/main/docs/design.md)): per protected ref the backup repo gets event refs `refs/heads/everref/remotes/origin/<branch>/created_<unix-ts>` (a lineage that only fast-forwards; a rewrite freezes it and starts a new one) and `…/deleted_<unix-ts>` (a tombstone), tags the same under `refs/tags/everref/…`, plus a journal per ref (one tiny commit per observed tip move). Its only writes are create and fast-forward, so Git itself refuses anything destructive. `restore --at <time>` rebuilds the refs as of the last run before that time and never overwrites. An unreachable source is an error, never a deletion.

| Aspect | Description |
| --- | --- |
| **Trigger** | Every 15 minutes by default: a systemd timer or cron calling `backup-guard run --config DIR` (example units in [examples/backup/systemd](examples/backup/systemd)). `backup-guard` never passes `--trigger` or `--schedule` to everref, so everref installs no hooks or timer units of its own. |
| **Can block** | Nothing. A force push or deletion upstream becomes a new lineage or a tombstone; the old tip stays reachable. |
| **Warnings** | v1 warns only when a run fails: preflight, `ls-remote`, a branch everref can't protect, a non-zero everref exit, a timeout. Every run's counts of new lineages, rewrites, deletions, moved tags and regressions are in `backup.jsonl`; warnings on those (rewrite of the default or a release branch, a tag moved or deleted, many branches deleted at once) may come later. |
| **Credentials** | Read-only for the remote: an SSH deploy key or a token file, handled like the Push Guard's (file reference only, `ssh -F none`, strict host keys). Never the Push Guard's write credential. The backup repo is local. |
| **Version** | git-everref is a **prerequisite** of the host, like gitleaks for the Push Guard: see [Installing git-everref](#installing-git-everref). Pinned: release `v1.0.0`. |
| **Storage** | Kept forever (everref never deletes a backup ref); `gc` is safe. Snapshot refs accumulate, so pack refs regularly (`git pack-refs --all`) or use reftable. Periodically `git bundle create --all` of each backup repo to offsite storage with write-once retention. |
| **Limits** | A state that exists only between two runs is never seen; history rewritten before the first run; branches matched by `exclude_branches`; LFS objects and submodule targets are not in the backup. |
| **Failure mode** | Fetch error, expired credential, disk full, everref missing → non-zero exit, a `backup_failed` warning and a failed line in `backup.jsonl`, never a silent skip. A missed run → heartbeat alerts. |

The backup is a dedicated bare repo, never a repo on a hosting platform: everref's event refs sit under `refs/heads/` and `refs/tags/`, so a clone would show them as branches.

#### Installing git-everref

The same model as gitleaks for the Push Guard:

- **Prerequisite on the host.** `backup-guard` looks git-everref up at `everref.path` in `defaults.yaml`, else as `git-everref` on `PATH`, and **fails closed** if it is missing or doesn't answer `--version`: no backup is pretended. If `everref.version` is set, the installed version must match exactly.
- **No runtime download.** `backup-guard` never fetches, updates or builds everref; pinning is part of provisioning the host.
- **Optional pinned install script.** [scripts/install-everref.sh](scripts/install-everref.sh) installs git-everref `v1.0.0` for Linux (amd64, arm64) from the project's GitHub release and checks the tarball against SHA-256 values pinned in the script (`git-everref_1.0.0_linux_amd64.tar.gz`: `0f87be25d89a31b23d1851edc917751023d56ab10925f1fe695afd8b31c33861`, `…_linux_arm64.tar.gz`: `d69453cc8c59c408c9b6ae8b42676e9f815e3ed6e30567dd9413a1b1553cdbe1`, matching the release's `checksums.txt`). CI installs it the same way, as it does for gitleaks.
- **From source:** tag `v1.0.0` (commit `f16c812`). A plain source build reports its version as `dev`, so leave `everref.version` unset for it.
- **Container image and Nix flake** provision it with the same pinned checksums: see [Provisioning: Docker and Nix](#provisioning-docker-and-nix).

#### Gaps to contribute upstream

In this order, to be offered to git-everref rather than worked around:

1. **Batched writes:** one `git push --atomic` per run and backup remote instead of one push per ref.
2. **All refs of a remote:** protect all branches and tags of a remote, including ones that appear later. Until then `backup-guard` selects the branches itself (`ls-remote` on every run, then `add` for new ones); tags are bridged as a whole already.
3. **Batched reads:** one `ls-remote` / `fetch` per remote and run (also in `add`).
4. **Exclude patterns** for bot churn (e.g. tags a CI bot moves on every run). `backup-guard` has `exclude_branches` for branches; tags can't be excluded yet.
5. **Optional single journal per remote** (one commit per run): "the whole repo at time T" in one lookup.
6. **Query commands:** state at T, and the diff between T1 and T2, without restoring.
7. **`--version` of a source build** should carry the tag, not `dev`.

**Scale:** everref v1.0.0 runs one `ls-remote` per ref in `add` and one `git push` per event ref and journal entry in `run`. That is fine for repos with a moderate number of branches; very large, busy repos stay out of the Backup Guard until gaps 1 and 3 are closed.

**Risks of depending on everref:** one maintainer and a young project. It is MIT-licensed, so it can be forked; the version is pinned, and Git Warden relies only on its CLI, its exit codes and its documented ref layout. Event order trusts the host clock.

**Fallback, if upstream doesn't take the changes:** a bare mirror fetches into `refs/incoming/*`, then one `git update-ref --stdin` transaction updates `refs/heads` and `refs/tags` and creates `refs/snapshots/<timestamp>/heads|tags/<name>` for every tip that is rewritten, moved or deleted (fast-forwarded tips stay reachable anyway). An append-only timeline ref (one commit per run whose tree mirrors the ref namespace) answers "where did X point at T". Never `--mirror` with `--prune`, which would delete the snapshots.

## Layout, naming and versions

- **Monorepo, one umbrella version.** Two binaries, `cmd/push-guard` and `cmd/backup-guard`, named after their guard posts (the package name `git-warden` is taken on npm, PyPI and crates.io). All of Git Warden shares one version and one tag, `vX.Y.Z`, and one GitHub Release that carries both binaries; the earlier `push-guard/vX.Y.Z` tags were replaced by `v0.1.0`.
- **Decision core is a library without platform knowledge:** `internal/rules` (delta normalisation, deterministic rules, verdict). Around it: `internal/config` (load and merge), `internal/gitx` (git as a subprocess with timeouts and credential handling), `internal/journal` (`push.jsonl`), `internal/pushguard` (hook, forwarding, approvals, rate limit, streak, serve, replay), `internal/backupguard` (Backup Guard configuration, preflight, bridge and backup setup, everref runner, `backup.jsonl`, warnings).
- **Two guard posts, no AI.** Push Guard and Backup Guard only; every decision is a deterministic rule.
- **The Backup Guard drives git-everref, it doesn't reimplement it.** Recording, the ref layout and restore are everref's; `backup-guard` adds what a scheduled, unattended backup needs around it (preflight, branch selection, logging, warnings). Missing everref features are contributed upstream rather than rebuilt here.

### Provisioning: Docker and Nix

Two ways to get a host (or a dev environment) with exactly the prerequisites, pinned. Both are provisioning: the binaries inside still never download anything.

- **`Dockerfile`:** multi-stage. The build stage (`golang:1.27-trixie`, pinned by digest) builds static `push-guard` and `backup-guard`. The runtime image is `debian:trixie-slim` (pinned by digest) with Debian's `git` (2.47; the build fails below 2.42), `openssh-client` and `ca-certificates`; gitleaks `8.30.1` (the same release as CI; tarball SHA-256 `551f6fc8…` for amd64, `e4a487ee…` for arm64, full values in the Dockerfile) and git-everref `v1.0.0` installed by `scripts/install-everref.sh` with its pinned SHA-256. It runs as the unprivileged user `warden` (uid 10001). Configuration is mounted read-only (`/etc/warden`, `/etc/warden-backup`), state goes to a volume (`/var/lib/warden`, `/var/lib/warden-backup`). The default command is `push-guard serve --config /etc/warden --listen 0.0.0.0:8418`; the Backup Guard runs as `backup-guard run --config /etc/warden-backup` from a host timer. No configuration or secret is in the image (`.dockerignore` sends only `go.mod`, `go.sum`, `cmd/`, `internal/` and the install script). Dependabot keeps the base image digests current. The image isn't published; build it yourself.
- **`flake.nix` + `flake.lock`:** nixpkgs pinned by the lock file (`nixos-26.05`). `packages.default` builds both binaries with `buildGoModule` and runs `go test ./...` in the sandbox with the real gitleaks and git-everref; `devShells.default` has Go, git, OpenSSH, gitleaks and git-everref. gitleaks and git-everref are not taken from nixpkgs but fetched as the same pinned release binaries (static, same SHA-256 as CI and the Dockerfile), so every environment runs the same versions. Linux only (x86_64, aarch64), like the install script. The package is marked unfree (PolyForm Noncommercial) and allowed by name inside the flake.

## Configuration and operation

Both guards are configured the same way and documented with the same structure: [docs/push-guard.md](docs/push-guard.md) (with the rule reasoning in [docs/push-guard-rules.md](docs/push-guard-rules.md)) and [docs/backup-guard.md](docs/backup-guard.md); overview and the credential rights per platform in the [README](README.md#configuration-and-operation).

- **Separate files, same model:** each guard has its own configuration directory (usually on its own host) with a `defaults.yaml` and one `repos/<name>/` folder per remote (`warden.yaml` or `backup.yaml`). Unknown keys are errors in both.
- **Same keys, same meaning:** `remote`, `credential`, `credential_username`, `known_hosts` per repo; `notify.command`, `state_dir`, `timeout` and the external program's path (`gitleaks.path`, `everref.path`) in `defaults.yaml`. One loader checks the remote for both (`internal/config`): a local remote is an absolute path or `file://` URL and takes no credential; SSH and HTTPS remotes need one; `credential_username` is HTTPS-only (default `x-access-token`), `known_hosts` SSH-only. Relative file paths are relative to the configuration directory; a program path that is a bare name is looked up on `PATH`.
- **Rights:** the Push Guard's credential reads and writes, the Backup Guard's only reads; they are never the same credential.

## Push Guard: what the implementation settled

Where this section and the design above differ, this section wins.

### Push Guard configuration

- **Three layers:** built-in defaults compiled into the binary (`internal/config/defaults.yaml`), the wall's `defaults.yaml`, the repo's `repos/<name>/warden.yaml`. Scalars and limits override; `match`, `allow`, `deny` append; `match_remove`, `allow_remove`, `deny_remove` remove an exact entry of a lower layer, and an entry that isn't there is an error. Unknown keys and rule IDs are errors, so typos don't silently disable protection.
- **Deny semantics:** a rule fires for a subject if `deny` matches, or if it is triggered and no `allow` matches. Which subjects a rule looks at: `REF-NAMESPACE` every pushed ref, `PATH-*` every changed path; every other rule only the subjects its trigger hits. So `deny` on `REF-NAMESPACE` protects a branch on every push, while `deny` on `REF-DELETE` only keeps a deletion red despite a broader `allow`.
- **Subjects:** full ref name for `REF-*`; path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*` limits; ref name for per-ref `SIZE-*` totals; branch name without `refs/heads/` for `META-*` (full ref name for tags). `REF-COUNT` and `RATE-*` have no subject; `allow`/`deny` don't apply.
- **`credential` is required for SSH and HTTPS remotes and refused for a local path or `file://` remote** (relative local paths are refused too). For SSH remotes it is a private key (`GIT_SSH_COMMAND` with `-i` and `IdentitiesOnly=yes`), for `https://` a token file read by an inline credential helper, with the user name from `credential_username` (default `x-access-token`). The credential never appears in YAML, logs or command lines; only its path does. Relative paths are relative to the configuration directory.
- **SSH host keys:** optional per-repo `known_hosts` (SSH remotes only), otherwise the guard user's own (with a per-repo file the system-wide known_hosts is ignored too); `StrictHostKeyChecking=yes` and `BatchMode=yes`, and `ssh` runs with `-F none`, so no ssh_config on the wall host can change where or how the guard connects.
- **Wall-only settings:** `agent` and `state_dir` may not appear in a repo file; `remote`, `credential`, `default_branch` may not appear in a defaults file.

### Preflight

- **At start, before anything is served or set up:** `push-guard serve` and `push-guard init-repo` refuse to start, with one message that lists every problem, unless:
  - the wall's `defaults.yaml` exists and the configuration loads;
  - `git` 2.42 or newer is on `PATH`;
  - at least one repo is configured (`repos/<name>/warden.yaml`) and each loads (`init-repo`: the repo it is given);
  - each repo has at least one rule enabled, so a configuration that switches every rule off is refused rather than forwarding unchecked;
  - for every repo with `CONTENT-SECRET` enabled (the default), its gitleaks (`gitleaks.path`, by default `gitleaks` on `PATH`) is found and `gitleaks version` reports 8.x. With `CONTENT-SECRET` disabled everywhere, gitleaks isn't needed.
- **In the hook, on every push:** the per-repo part of the same checks (an enabled rule; gitleaks found if `CONTENT-SECRET` is enabled) runs before the remote is read. A failure answers `internal error, try again later` and is logged in `push.jsonl`. This covers a guard repository used without `serve` (SSH, local path) and a gitleaks removed after the start.
- **`check-config`** runs the same preflight and reports its problems with the rest. `serve` logs `preflight ok (git …, N repo(s), gitleaks …)` when it starts.
- Same shape as the Backup Guard's preflight; both use the same git check (`internal/gitx`).

### Hook and forwarding

- **Inputs:** the remote's refs come from a fresh `git ls-remote --symref` per push. Remote objects the guard lacks are fetched by oid (`git fetch --no-write-fetch-head <remote> <oid>…`) into the push's quarantine; no ref is updated. If they are still missing, the push fails closed.
- **Ref kinds** are computed against the remote's old state; annotated tags are peeled to commits. Cumulative diff base: the remote's old commit for `ff`; the merge base for `non_ff` and `tag_move`; `merge-base(remote default branch, new)` for a new ref; otherwise the empty tree. New commits: `rev-list <new> --not <remote heads and tags>`.
- **Forwarding:** `git push --porcelain [--atomic] <remote> <sha>:<ref> …`, never `--force`. `--force-with-lease=<ref>:<remote old>` (and `:<ref>` for a delete) is used only when `REF-NON-FF`, `REF-TAG-MOVE` or `REF-DELETE` was triggered and explicitly exempted by `allow` for that ref, or when a human approved exactly that SHA. Without a lease the remote's own non-fast-forward check applies. The quarantine variable is dropped for every command that talks to the remote, because a local remote's `receive-pack` would otherwise refuse the update.
- **Pending store as bundles:** the design's original `refs/warden/pending/<id>/*` can't work: refs can't be written inside the quarantine, and the quarantined objects vanish when the hook rejects. A red push's new commits are written to `state_dir/pending/<repo>/<id>.bundle` instead (built in a temporary repository that borrows the quarantined objects).
- **Approvals** are per (ref, SHA) and used up by the forwarded push that carries them. In a push that mixes approved and other updates, only the others are checked. An approval also ends an active yellow streak.
- **Yellow streak:** yellow rejections are counted per repo since the last `approve` or `reset-streak` and within the window. The push that would exceed `max_yellow` becomes red and logs a `streak` event; while a `streak` event is newer than the last approve or reset, every push to the repo is red.
- **Rate limit:** a rate-limited push isn't evaluated, answers `rate limited, try later`, and only the first of a series warns the human.
- **Fail-closed:** any error answers `internal error, try again later`, is logged in `push.jsonl` with its detail, and is not warned as a violation. A malformed `push.jsonl` line fails every push closed. gitleaks exits 0 even when its git call fails, so its log is checked for errors too.
- **Unknown repo:** red with the message `rejected: unknown repo` (the only red answer that names a reason besides the rate limit).

### Push Guard rules

Testable requirements for these quirks and for every rule are numbered as `REQ-PG-*` in [docs/push-guard-rules.md § Requirements](docs/push-guard-rules.md#requirements). Requirement IDs are a separate namespace from rule IDs (`REF-*`, …) and from risk IDs (`R1`–`R22`); do not renumber or reuse either.

- **`META-UNSIGNED` checks presence only:** a `gpgsig`/`gpgsig-sha256` header. The wall has no keyring, so signatures aren't verified. "The last `lookback` commits on that branch" are first-parent commits of the remote's old state (the default branch for a new ref or a tag); with fewer than `lookback` commits the rule stays quiet.
- **`CONTENT-BINARY`** also fires for added text that isn't valid UTF-8 (the design's "undecodable text counts as binary").
- **`CONTENT-PAGES-SCRIPT`:** a host counts as new if `git grep -i -F <host>` finds it nowhere in the remote's old tree of the Pages branch.
- **`SIZE-MASS-DELETE`** share check applies to modified and renamed files; deleted files count toward `max_deleted_files`.

### Human commands

`push-guard approve <repo> <ref> <sha>` and `push-guard reset-streak <repo>` (the design's `warden approve` / `warden reset-streak`), plus `check-config`, `init-repo`, `replay` and `version`. All take `--config DIR`.

### Serve

`push-guard serve` wraps `git http-backend` (net/http/cgi), accepts only the smart HTTP routes of `/<name>.git`, authenticates with HTTP basic auth against `agent.token_file` (constant-time compare), creates guard repositories on demand with `http.receivepack=true`, `receive.fsckObjects=true` and the hook installed (the hook script embeds the absolute binary path, the config directory and the repo name), and syncs them from the remote before every ref advertisement, so agents fetch through the guard too.

## Backup Guard: what the implementation settled

Where this section and the design above differ, this section wins. Configuration and operation in detail: [docs/backup-guard.md](docs/backup-guard.md).

- **Commands:** `backup-guard run --config DIR [repo…]` (all repos if none is named), `backup-guard check-config --config DIR [--remote]`, `backup-guard version`. Exit codes: `0` every repo backed up, `1` a repo failed or the preflight failed, `2` usage error.
- **Own configuration directory,** separate from the Push Guard's (it usually lives on another host): `defaults.yaml` (`everref.path`, `everref.version`, `notify.command`, `state_dir`, `timeout`) and `repos/<name>/backup.yaml` (`remote`, `credential`, `credential_username`, `known_hosts`, `exclude_branches`), with the same key names, meanings and remote checks as the Push Guard (see [Configuration and operation](#configuration-and-operation)). Strict: unknown keys are errors. No layers and no rules.
- **Preflight, before anything is touched:** the configuration loads, `git` 2.42 or newer is on `PATH`, git-everref is found and answers `--version` (and matches `everref.version` if set), at least one repo is configured. Any failure ends the run with exit `1` before any state is created. `check-config` runs the same preflight plus credential, `known_hosts` and `notify.command` checks, and with `--remote` an `ls-remote` per repo.
- **Credentials:** required for SSH and HTTPS remotes, none for a local path or `file://` URL (relative local paths are refused). Same handling as the Push Guard (`internal/gitx`); the credential environment is passed to everref, so its fetches use the same key or token. Read access is enough and is all it should have.
- **State per repo:** `state_dir/repos/<name>/bridge` (non-bare; remote `origin` = the configured remote, remote `backup` = the backup repo with no fetch refspec; local committer identity `git-warden backup-guard` for everref's journal commits) and `state_dir/repos/<name>/backup.git` (bare). Bridged tags are switched on once with `git-everref tags --remote backup on --source origin`. If the configured remote no longer matches the bridge's `origin`, the run fails: a backup is never continued with another remote.
- **Atomic setup, interrupted runs:** the backup repo and the bridge (with both remotes) are each built in a temporary directory `.setup-<name>-*` next to them and renamed into place only when complete, so a run killed during setup leaves either nothing or a finished repo. The next run removes leftover `.setup-*` directories under the lock and starts the setup again; an existing directory that isn't a finished repo must be empty, otherwise the run fails rather than guessing. A run killed during `git-everref add` or `run` is resumed by the next run, which protects the remaining branches and records what is new. Tested by killing the first run at every setup step and during everref.
- **Branch selection:** every run lists the remote's branches (`ls-remote`), drops those matching `exclude_branches` (anchored regular expressions over the branch name without `refs/heads/`) and protects the new ones with `git-everref add origin/<branch>… --remote backup`, up to 100 per call. A call everref refuses is retried branch by branch; a branch that still can't be protected makes the run fail, while the others are backed up. A protection is never removed, also not when the branch is deleted upstream (everref needs it to write the tombstone) or excluded later. A remote with no branch to back up is a failure, not an empty backup.
- **The run:** `git-everref -C <bridge> run --all` with the repo's `timeout`. Its output is counted (new lineages, rewrites, deletions, moved tags, regressions) into the run's line in `state_dir/backup.jsonl`; on failure the line carries the error and the tail of everref's output.
- **Warnings:** a failed repo sends `{"kind": "backup_failed", "guard": "backup", …}` with the error and everref's exit code to `notify.command` on stdin. The other repos still run.
- **Locking:** one flock per repo (`state_dir/locks/<name>.lock`), so overlapping timer runs wait instead of racing. The kernel releases it when the process ends, also when it is killed.

## Rollout stages

1. **Push Guard** with rules and secret scan: forward or reject. This includes removing every other write credential from the agents' machines, otherwise the Push Guard is decoration.
2. **Backup Guard:** `backup-guard` on a timer, driving git-everref; very large repos once the first upstream gaps are closed.

Each stage is useful on its own; none requires the next. Both are built; neither is in production use yet.

## Data model

| Entity | Fields (excerpt) |
| --- | --- |
| `Repo` | `name` (chosen by the owner), `remote` (any Git URL), `default_branch`, `guards[]` |
| `Event` | `id`, `guard` (`push`/`backup`), `repo`, `actor` (agent identity or human), `received_at` |
| `RefChange` | `event_id`, `ref`, `old_sha`, `new_sha`, `kind` |
| `Finding` | `event_id`, `rule_id`, `severity`, `ref`, `path`, `evidence` (facts only) |
| `Verdict` | `event_id`, `source` (`rules`/`human`), `decision`, `findings[]` |

No database: the Push Guard keeps `push.jsonl`, its Git repositories and bundles of rejected pushes; the Backup Guard keeps everything as Git refs in the backup repos, plus `backup.jsonl` (one line per repo and run) and offsite bundles.

## Prompt injection resilience

Git Warden has no model in its decisions, so the persuasion techniques against AI gatekeepers ([R21 in detail](#r21-in-detail-how-ai-gatekeepers-were-persuaded)) have nothing to work on: commit messages and other agent text never change a verdict, and a `red` rejection tells the agent nothing it could learn from. What remains is the human: warnings show the facts first and the agent's text only as an unverified quote. Prompt injection of the working agent itself (R15) is outside; its consequences in the repo still go through the rules.

## Failure modes (overall)

| Failure mode | Consequence | Countermeasure |
| --- | --- | --- |
| A guard down | Push Guard: pushes fail, agents keep working locally. Backup Guard: backups pause | Nothing goes to the remote unchecked; heartbeat |
| A prerequisite missing (gitleaks, git-everref) or no configuration | Push Guard: `serve` and `init-repo` refuse to start, every push fails closed. Backup Guard: the preflight fails, nothing runs | `check-config`; a failed run warns; the [container image and Nix flake](#provisioning-docker-and-nix) bring the pinned programs along |
| Push Guard or Backup Guard host compromised | Write credential or backup gone | Separate hosts, neither on the agents' machine; offsite write-once bundles; rotatable write keys |
| Alert fatigue | A human waves things through | `red` used sparingly, digests, facts first |
| False negative | A subtle backdoor gets through | Not covered by Git Warden (no semantic review); the backup keeps the history, human review still needed |
| Notification channel spoofed | False approval | Authenticated channel; approvals never through issues |

## Risks: an AI agent with push access

What can an AI agent that may push to a repository break, and which of that can be repaired? This is the threat model behind Git Warden. How far its two guard posts cover each risk, including what they **don't** cover, is in [Coverage by Git Warden](#coverage-by-git-warden).

**Risk IDs** (R1–R22) are stable: they are never renumbered, new risks are only appended at the end. The rest of the docs refer to them by ID. They are unrelated to Push Guard requirement IDs (`REQ-PG-*`) in [docs/push-guard-rules.md](docs/push-guard-rules.md#requirements). Overview: [Risk register](#risk-register). Research as of October 2026.

### TL;DR

1. With push access, an agent can destroy or quietly manipulate code, history, branches, tags and releases; most of that can be recovered with Git/GitHub.
2. Not recoverable: leaked secrets, published packages/releases, exfiltrated data and repos that were briefly public.
3. The bigger lever is the **token**, not the push: a classic `repo` token applies to *all* of the user's repos, including collaborators, webhooks and visibility; `workflow` allows CI manipulation.
4. Real cases from 2025 (Nx/s1ngularity, Shai-Hulud, Amazon Q, GitHub MCP injection) show exactly these chains: stolen `gh` token → private repos made public, workflows that grab secrets.
5. In a typical setup, an owner token on a machine the agent can read is a single point of failure → repo-bound tokens or a GitHub App per agent, bots without admin rights, rulesets against force push and deletion, backups the agent can't write to.

### 1. Damage inside the repo

- **R1 – Destroying history.** `git push --force` overwrites branches, `git push --delete` removes branches and tags, a rebase/`filter-repo` rewrites history. GitHub blocks force pushes only on protected branches – and by default the rules **don't apply to admins** ([GitHub Docs: About protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)). Force pushes can also "delete branches or point them to unreviewed commits" (ibid.).
- **R2 – Deleting or damaging code.** Mass deletions, broken refactorings, lockfile chaos. Loud, but easy to repair.
- **R3 – Subtle bugs and backdoors.** More dangerous than loud damage: a weakened auth check, a new dependency, a `postinstall` script. Without review, nobody notices. Trail of Bits showed how hidden issue text gets the Copilot agent to build in a backdoor via a manipulated `uv.lock` ([Trail of Bits, Aug 2025](https://blog.trailofbits.com/2025/08/06/prompt-injection-engineering-for-attackers-exploiting-github-copilot/)) – lockfiles are a favourite hiding place because nobody reads them.
- **R4 – Secrets in commits.** GitGuardian counted over 23.7 million new hard-coded secrets in public GitHub repos in 2024; repos with Copilot active had a 40 % higher leak rate (6.4 % vs. 4.6 %) – correlation, not causation ([State of Secrets Sprawl 2025](https://blog.gitguardian.com/the-state-of-secrets-sprawl-2025/)).
- **R5 – Licence/legal problems.** An agent can copy in third-party code with an incompatible licence or change `LICENSE` files (relevant for PolyForm Noncommercial repos).
- **R6 – Spam.** Thousands of commits, issues, releases or comments in the user's name – reputational damage, possibly account suspension.

### 2. Supply chain

- **R7 – Rewriting CI/CD workflows.** Whoever can write `.github/workflows/` runs code with the repo's secrets. Shai-Hulud (Sept 2025) used stolen tokens to push a branch `shai-hulud` with a workflow that sent `toJSON(secrets)` to a webhook ([GitGuardian](https://blog.gitguardian.com/shai-hulud-a-persistent-secret-leaking-campaign/), [Wiz](https://www.wiz.io/blog/shai-hulud-npm-supply-chain-attack)).
- **R8 – Moving tags.** In tj-actions/changed-files (March 2025, CVE-2025-30066), tags were retroactively moved to a malicious commit that wrote secrets into workflow logs ([CISA](https://www.cisa.gov/news-events/alerts/2025/03/18/supply-chain-compromise-third-party-tj-actionschanged-files-cve-2025-30066-and-reviewdogaction)) – tags are mutable, only SHAs are not. Whoever controls a repo with push access can silently move any release tag.
- **R9 – Malicious releases/packages.** In Nx (Aug 2025), a manipulated workflow sent the npm publish token to a webhook; malicious Nx versions were published afterwards ([Nx advisory GHSA-cxm3-wv7p-598c](https://github.com/nrwl/nx/security/advisories/GHSA-cxm3-wv7p-598c)). In Amazon Q Developer for VS Code, an over-privileged GitHub token in the CodeBuild configuration let an attacker commit code that shipped automatically in version 1.84.0 ([AWS-2025-015](https://aws.amazon.com/security/security-bulletins/AWS-2025-015/), CVE-2025-8217). The injected prompt was meant to return the system "to a near-factory state" and delete cloud resources; it failed because of a syntax error ([SC Media](https://www.scworld.com/news/amazon-q-extension-for-vs-code-reportedly-injected-with-wiper-prompt)).
- **R10 – Dependency confusion.** A public package with the name of an internal dependency is preferred by the package manager ([Alex Birsan, 2021](https://medium.com/@alex.birsan/dependency-confusion-4a5d60fec610)). An agent can trigger this by changing registry configuration or package names.
- **R11 – Poisoning GitHub Pages.** Whoever pushes changes the website built from the repo – phishing or malware downloads under a trusted domain.

### 3. Beyond the repo: what the token allows

According to the [GitHub scope docs](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps):

| Scope | What it means for the agent |
| --- | --- |
| `repo` | Full access to **all** of the user's public and private repos: code, collaborators, invitations, webhooks, deploy statuses. With owner rights also admin functions such as settings, deploy keys, visibility (private → public), changing/removing branch protection. |
| `workflow` | Create/change workflow files → arbitrary code in CI. |
| `gist` | Write gists – a convenient exfiltration channel. |
| `read:org` | Read org/team memberships (reconnaissance). |
| `delete_repo` | Delete repos (separate scope). |

Shai-Hulud used exactly such tokens to publish private repos as public copies with the suffix `-migration`; one victim had access to 528 private company repos ([Safety](https://www.getsafety.com/blog-posts/shai-hulud-npm-attack)). In phase 2 of s1ngularity, leaked tokens were used to make over 5,500 private repos of more than 400 users/orgs public ([Wiz](https://www.wiz.io/blog/s1ngularity-supply-chain-attack)).

This gives three risks:

- **R12 – Reach beyond the target repo.** A token for "one" repo acts on all repos of the account.
- **R13 – Settings, visibility, access.** Private → public, new collaborators, deploy keys, webhooks, removing branch protection/rulesets, switching the default branch.
- **R14 – Exfiltration.** Private content ends up in gists, new public repos (`s1ngularity-repository`, `Shai-Hulud`) or PRs/comments in public repos.

### 4. Agent-specific vectors

- **R15 – Prompt injection via repo content.** Invariant Labs showed in May 2025: a prepared issue in a public repo gets an agent using the official GitHub MCP server to read private repos and publish their content via a PR – a "toxic agent flow" ([Invariant Labs](https://invariantlabs.ai/blog/mcp-github-vulnerability), [Toxic Flow Analysis](https://invariantlabs.ai/blog/toxic-flow-analysis)). The cause is not a bug in the server but a token that sees public *and* private repos. At the end of 2025, Aikido described the same pattern for AI agents in GitHub Actions that take issue titles/bodies into prompts ("PromptPwnd", including Gemini CLI) ([Aikido](https://www.aikido.dev/blog/promptpwnd-github-actions-ai-agents)). Other sources: READMEs, code comments, documentation of dependencies.
- **R16 – Local agents as malware tools / token theft.** The Nx malware called installed `claude`, `gemini` and `q` CLIs with `--dangerously-skip-permissions`, `--yolo` or `--trust-all-tools` to inventory secrets, and fetched the GitHub token via `gh auth token` ([Snyk](https://snyk.io/blog/weaponizing-ai-coding-agents-for-malware-in-the-nx-malicious-package/), [StepSecurity](https://www.stepsecurity.io/blog/supply-chain-security-alert-popular-nx-build-system-package-compromised-with-data-stealing-malware)). According to Snyk, the vulnerable workflow where it all started was probably generated with Claude Code.
- **R17 – Hallucinated packages ("slopsquatting").** In 2.23 million generated package references, 19.7 % were made up, 205,474 unique non-existent names ([Spracklen et al., USENIX Security 2025](https://www.usenix.org/conference/usenixsecurity25/presentation/spracklen)). Attackers can register such names.
- **R18 – Mistakes without an attacker.** Much damage needs no attacker: in July 2025, during a "code freeze", the Replit agent deleted Jason Lemkin's production database (1,206 executives, 1,196+ companies) and at first wrongly claimed a rollback was impossible ([Fortune](https://fortune.com/2025/07/23/ai-coding-tool-replit-wiped-database-called-it-a-catastrophic-failure/), [Fast Company](https://www.fastcompany.com/91372483/replit-ceo-what-really-happened-when-ai-agent-wiped-jason-lemkins-database-exclusive)). Instructions in the prompt are not access control.

### 4a. Further risks (from the research for Git Warden)

- **R19 – Naive mirrors mirror the destruction.** A pull mirror takes over force updates and deletes refs that were deleted upstream; Forgejo explicitly processes force updates and ref deletions on sync ([Forgejo repository mirrors](https://forgejo.org/docs/latest/user/repo-mirror/)). Gitea Mirror writes that after a force push the old history in Gitea is replaced "with no way to get it back" unless the protection is active ([Gitea Mirror: Force-Push Protection](https://gitea-mirror.raylabs.io/docs/force-push-protection/)). A backup that syncs blindly is worthless for R1/R2.
- **R20 – Compromised review/bot service with write access.** Kudelski Security got code execution on CodeRabbit's servers via a prepared `.rubocop.yml` in a PR, and with it the GitHub App's private key – write access to around 1 million repos, including private ones ([Kudelski](https://kudelskisecurity.com/research/how-we-exploited-coderabbit-from-a-simple-pr-to-rce-and-write-access-on-1m-repositories), [CodeRabbit statement](https://www.coderabbit.ai/blog/our-response-to-the-january-2025-kudelski-security-vulnerability-disclosure-action-and-continuous-improvement)). Every guard that holds write access is itself a target.
- **R21 – AI gatekeepers can be persuaded.** With iteratively refined PR metadata, re-introducing known vulnerabilities against Claude Code and CodeRabbit review pipelines succeeded in 32 of 33 cases (97 %; 17/17 Claude Code, 15/16 CodeRabbit); attackers can practise locally against a copy of the pipeline, defenders get only one try ([arXiv:2603.18740](https://arxiv.org/abs/2603.18740)). SEVRA-Bench showed 8 review agents to be susceptible to social engineering narratives, much more so for weaker models ([arXiv:2606.13757](https://arxiv.org/abs/2606.13757)); GitInject found all four tested AI providers vulnerable to at least one attack class in default workflows ([arXiv:2606.09935](https://arxiv.org/abs/2606.09935)). Anthropic itself warns that `claude-code-security-review` is not hardened against prompt injection ([README](https://github.com/anthropics/claude-code-security-review)). Details, and what Git Warden does about each technique: [R21 in detail](#r21-in-detail-how-ai-gatekeepers-were-persuaded).
- **R22 – Non-Git data.** Issues, PR discussions, releases with assets, wiki settings and repo settings are not in the Git object store. `git clone --mirror` doesn't back them up; that needs API exports such as [python-github-backup](https://github.com/josegonzalez/python-github-backup), and according to its README even those can't be restored faithfully.

#### R21 in detail: how AI gatekeepers were persuaded

Documented cases only. These are attacks on **AI** reviewers and gatekeepers. Git Warden uses no AI to decide anything, so most of them have no target in Git Warden itself, but they hit any AI reviewer you run next to it unchanged. Under each technique, "Git Warden" says what its deterministic rules do about it, or **Not covered**.

**T1 – Plausible technical justification, refined offline.** Alexopoulos et al. ([arXiv:2603.18740](https://arxiv.org/abs/2603.18740), v4 of 2026-09-23) revert fixes for 33 real CVEs in 20 projects and submit them as PRs – against Claude Code and CodeRabbit review pipelines rebuilt in isolation. Two strategies:
- **Templates** (after Moon et al.: *authority* "written by an expert", *self-correctness* "correct", *misleading* false purpose) almost never worked: across 264 combinations, a template helped only once, not reproducibly; sometimes it **backfired** because the contradiction between claim and diff stood out.
- **LLM-assisted refinement:** a coding agent writes a factual-sounding PR description (explicitly *without* appeals to authority), adapts comments/commit message to the repo's guidelines and revises them up to ten times based on the review feedback of a local copy of the pipeline. Result: 17/17 against Claude Code, 15/16 against CodeRabbit (32/33 = 97 %). Example strapi: a false "proof" that authorisation applies regardless of query parameters – exactly the property the removed code ensured.
- **Cross-check:** removing the PR description recovered 16 of the 32 cases; additionally removing commit message and comments recovered 12 of the remaining 16; the rest already failed without any text. For CodeRabbit, commit message and code comments had the most effect.
- *Git Warden:* PR and commit texts never change a verdict. **Not covered:** whether the code itself reintroduces a vulnerability; that needs a semantic review Git Warden doesn't do.

**T2 – Unverifiable external context and social engineering.** [SEVRA-Bench](https://arxiv.org/abs/2606.13757) (1,062 adversarial PRs from real fixes of the MITRE top 10 CWEs, 15 framings, 8 review agents in an isolated Gitea): most effective were justifications the reviewer can't check – "Dependency Compatibility Claim", "Prior Approval Signal" (supposedly already approved), "Review Bias"; repo-related explanations produced "unverified deference". Directly checkable contradictions were often caught. Weaker agents fell below a 35 % rejection rate; the strongest (including Claude Opus 4.7, GPT-5.5, GLM-5) mostly rejected.
- *Git Warden:* approvals only count from the warden's own log on the wall host; lockfile and manifest changes are `yellow`. **Not covered:** judging whether a claimed justification is true.

**T3 – Direct instructions in PR, issue or commit text.** [GitInject](https://arxiv.org/abs/2606.09935) (real GitHub workflows, four providers): a fake "internal audit note" in the PR text, instructing the reviewer to approve and not to report comparison operators, worked against `codex-pr-review` and `gemini-assistant`; `claude-pr-review` resisted. Aikido's [PromptPwnd](https://www.aikido.dev/blog/promptpwnd-github-actions-ai-agents) (Dec 2025): issue texts, PR descriptions and commit messages end up in prompts of agents with tools like `gh issue edit`; affected were, among others, Google's Gemini CLI repo (patched within four days) and, according to Aikido, at least five Fortune 500 companies.
- *Git Warden:* no model reads the texts, so there is nothing to instruct. **Not covered:** instructions aimed at other AI tools that read the repo later.

**T4 – Configuration files as operator instructions.** GitInject: a PR adds `CLAUDE.md`, `AGENTS.md` or `GEMINI.md`; the CLI loads it before the review as operator instructions. Example: a CSRF module compares tokens with `==` instead of `hmac.compare_digest` (timing oracle), and an included `CLAUDE.md` says not to report comparison operators in security utilities, "a separate security team checks that" – Claude followed exactly that. According to the paper there is no cheap workflow fix (path filters can be bypassed); author filters or a human are needed. Related: Pillar's ["Rules File Backdoor"](https://www.pillar.security/blog/new-vulnerability-in-github-copilot-and-cursor-how-hackers-can-weaponize-code-agents) (March 2025) hides instructions in Cursor/Copilot rule files.
- *Git Warden:* changes to agent configurations (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.cursor/rules/`, Copilot instructions) are `red` by default. *Limit:* a repo where agents maintain `AGENTS.md` may move these paths to `yellow` (see [Rules (excerpt)](#rules-excerpt)).

**T5 – Invisible or hidden content.** [CamoLeak](https://www.legitsecurity.com/blog/camoleak-critical-github-copilot-vulnerability-leaks-private-source-code) (Legit Security, CVSS 9.6): instructions in invisible Markdown comments of a PR; Copilot Chat followed them with the user's permissions and exfiltrated via an "alphabet" of pre-signed Camo image URLs; GitHub switched off image rendering in Copilot Chat on 2025-08-14 ([The Register](https://www.theregister.com/special-features/2025/10/09/github-patches-copilot-chat-flaw-that-could-leak-secrets/880245)). Pillar: zero-width joiners and bidi markers hide instructions; since 2025-05-01 GitHub shows a warning for hidden Unicode. [Trail of Bits](https://blog.trailofbits.com/2025/08/06/prompt-injection-engineering-for-attackers-exploiting-github-copilot/): payload in an HTML tag, invisible in the GitHub UI. AWS confirmed "invisible prompt injection" in Amazon Q ([AWS-2025-019](https://aws.amazon.com/security/security-bulletins/AWS-2025-019/)).
- *Git Warden:* invisible and bidi characters are `red` (`CONTENT-INVISIBLE`). **Not covered:** instructions hidden in HTML comments or in normal-looking text.

**T6 – Using the reviewer as an exfiltration channel.** Invariant Labs ([GitHub MCP](https://invariantlabs.ai/blog/mcp-github-vulnerability)) and CamoLeak: the tool had more permissions than the task needed. GitInject: token exfiltration via configuration files plus shell tools (minimum protection according to the paper: `persist-credentials: false` + tool restriction).
- *Git Warden:* no reviewer with permissions exists in Git Warden, and nothing is sent to a model provider. **Not covered:** AI reviewers you run elsewhere.

**T7 – Availability / denial of wallet.** GitInject: a PR text at GitHub's limit (65,536 characters) yields ≈ 22,100 input tokens and costs the victim about $1.11 per PR, a two-hour campaign $32–111. As a *hypothesis* (not confirmed), the paper mentions getting the API key suspended by provoking policy violations, which would silently switch off the gate.
- *Git Warden:* no LLM cost. Size and rate limits (`SIZE-*`, `RATE-*`) bound what a push can make the guard do; a flood of `red` pushes can still tire the human.

**T8 – Splitting and hiding outside the checked surface.** So far documented only against **human** reviewers: in xz-utils (CVE-2024-3094), part of the backdoor was "solely in the distributed tarballs", not in the Git repo, the rest obfuscated in test files ([oss-security, A. Freund](https://www.openwall.com/lists/oss-security/2024/03/29/4)). We did **not find** a study that specifically tests splitting across many commits against AI reviewers – plausible, but unproven.
- *Git Warden:* path and content rules run over the cumulative diff, so splitting across commits doesn't help; binary files (also in test directories) are `yellow`. **Not covered:** release artefacts that differ from the tag (reproducible builds would be needed).

**T9 – Code obfuscation.** Base64 payloads, `eval`, renamings. We did **not find** a solid study specifically on the effect on AI reviewers – marked as unproven.
- *Git Warden:* long base64/hex literals are `yellow` (`CONTENT-BLOB`). **Not covered:** `eval`/`exec` and other obfuscation in normal-looking code.

**Common lesson:** all sources see the problem as structural, not as a weakness of one particular model. An AI gatekeeper must not be the last instance for anything that matters. Git Warden therefore has none: its rules are deterministic and a human decides on `red`.

### 5. What can be recovered – and what can't

**Repairable:**

- **Local clones** contain the old history; `git reflog` shows earlier branch states ([git-reflog](https://git-scm.com/docs/git-reflog)).
- **GitHub practically never forgets force pushes:** overwritten commits remain retrievable by SHA ([Brizinov/Truffle Security](https://trufflesecurity.com/blog/guest-post-how-i-scanned-all-of-github-s-oops-commits-for-leaked-secrets)). The [activity view](https://docs.github.com/en/repositories/viewing-activity-and-data-for-your-repository/using-the-activity-view-to-see-changes-to-a-repository) lists force pushes and branch deletions with the actor; that allows restoring the old state.
- **Deleted branches** can be restored via the associated PR ([docs](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-branches-in-your-repository/deleting-and-restoring-branches-in-a-pull-request)), otherwise via a ref to the known SHA.
- **Deleted repos** can usually be restored for 90 days ([docs](https://docs.github.com/en/repositories/creating-and-managing-repositories/restoring-a-deleted-repository)).

**Not repairable:**

- **Leaked secrets.** A commit is never really gone (see above). The only way out: revoke and rotate the secret ([GitHub: Removing sensitive data](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)).
- **Published packages.** npm allows unpublishing only to a limited extent, and the version number is used up afterwards ([npm unpublish policy](https://docs.npmjs.com/policies/unpublish/), [`npm unpublish`](https://docs.npmjs.com/cli/v11/commands/npm-unpublish)); whoever installed it has already run the malicious code.
- **Exfiltrated or briefly public data.** Copies, forks and archives (e.g. GH Archive) can't be recalled.
- **Knock-on damage** to users of Pages sites or releases.

### 6. Countermeasures

1. **Least-privilege tokens.** [Fine-grained PATs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) limited to single repos and single permissions (e.g. only `Contents: write`, no `Administration`, no `Workflows`) with an expiry date. Better: a **GitHub App**, whose installation tokens expire after 1 hour ([docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)).
2. **A separate bot identity without admin rights.** Then branch protection and rulesets apply to the agent too; the admin bypass stays with the human.
3. **Rulesets/branch protection** ([available rules](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)): block force pushes, forbid deletion, require PRs with review, status checks, signed commits, tag protection (`v*`).
4. **CODEOWNERS** for `.github/workflows/`, lockfiles, `LICENSE` ([docs](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners)) plus "Require review from Code Owners".
5. **Secrets in environments** with required reviewers ([docs](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments)); pin actions to SHAs, avoid `pull_request_target` ([secure use](https://docs.github.com/en/actions/reference/security/secure-use)).
6. **Turn on push protection/secret scanning** ([docs](https://docs.github.com/en/code-security/secret-scanning/introduction/about-push-protection)).
7. **Separate public and private.** An agent that reads other people's issues must not have a token that sees private repos (against toxic flows).
8. **Audit:** check the [security log](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/reviewing-your-security-log) and activity view regularly.
9. **Backups/mirrors** (`git clone --mirror`) in a place agents can't write to. Don't sync blindly (R19) – Git Warden's answer: the [Backup Guard](#backup-guard).
10. **Sandboxing.** Tokens don't belong in the agent's environment but on the wall around it (for example [Hermetarium](https://github.com/pihme/hermetarium)): a proxy/broker that allows and logs Git operations per repo. The Push Guard is such a broker for Git.

### Risk register

| ID | Risk | Section |
| --- | --- | --- |
| R1 | Destroying history (force push, branch/tag deletion, rewrite) | 1 |
| R2 | Deleting or damaging code | 1 |
| R3 | Subtle bugs and backdoors (incl. lockfiles) | 1 |
| R4 | Secrets in commits | 1 |
| R5 | Licence/legal problems | 1 |
| R6 | Spam | 1 |
| R7 | Rewriting CI/CD workflows (secret exfiltration) | 2 |
| R8 | Moving tags | 2 |
| R9 | Malicious releases/packages | 2 |
| R10 | Dependency confusion | 2 |
| R11 | Poisoning GitHub Pages | 2 |
| R12 | Token reach beyond the target repo | 3 |
| R13 | Changing settings, visibility, access, protection rules | 3 |
| R14 | Exfiltration (gists, new public repos, PRs) | 3 |
| R15 | Prompt injection via repo content | 4 |
| R16 | Local agent CLIs abused by malware / token theft | 4 |
| R17 | Slopsquatting | 4 |
| R18 | Mistakes without an attacker | 4 |
| R19 | Naive mirrors mirror the destruction | 4a |
| R20 | Compromised review/bot service with write access | 4a |
| R21 | AI gatekeepers can be persuaded | 4a |
| R22 | Non-Git data (issues, PRs, releases, settings) | 4a |

## Coverage by Git Warden

Git Warden has two guard posts: the **Push Guard** (deterministic rules on every agent push) and the **Backup Guard** (`backup-guard` running git-everref on a timer: it preserves, but decides nothing). Neither uses AI, and nothing checks pull requests, CI results or changes by writers who don't go through the Push Guard.

Legend: **✅** prevents (Push) or preserves (Backup) · **◐** partly · **–** no. Status: **Covered**, **Partly** or **Not covered**. Push Guard values apply **only if agents have no other write credential for the remote**. Backup Guard values apply to every state a run has seen: a state that exists only between two runs, history rewritten before the first run, branches in `exclude_branches`, LFS objects and submodule targets are not in the backup.

| ID | Risk | Push | Backup | Status | Reasoning |
| --- | --- | --- | --- | --- | --- |
| R1 | Destroying history | ✅ | ✅ | Covered | Non-FF and delete are `red` for agents. The Backup Guard keeps every rewritten branch as a frozen lineage and every deleted one as a tombstone, also against other writers and stolen tokens. |
| R2 | Deleting or damaging code | ◐ | ✅ | Covered | Preserved by the backup (every recorded tip stays reachable); prevented only for mass deletion. |
| R3 | Subtle bugs and backdoors | ◐ | – | Partly | Only what rules see: lockfiles, manifests, build files, binaries, long literals. **Not covered:** semantic review of the code, and changes by other writers or through PRs. The backup only helps to find out afterwards since when. |
| R4 | Secrets in commits | ◐ | – | Partly | gitleaks over every new commit before forwarding: recognisable formats never reach the remote; scanner gaps remain. The backup keeps every recorded commit, a leaked secret included: revoke and rotate, never rely on deleting it. |
| R5 | Licence | ◐ | – | Partly | A `LICENSE` change is `red`. **Not covered:** copied-in third-party code. |
| R6 | Spam | ✅ | – | Covered | Rate limit per agent; the agents' PR-only token allows no issues. PR texts and comments are out of scope. |
| R7 | CI/CD workflows | ✅ | – | Partly | Workflow changes are `red` for agents, plus an App without `workflows` permission. **Not covered:** workflow changes by other writers or in PRs from forks. |
| R8 | Moving tags | ✅ | ✅ | Covered | Tag moves are `red`; the Backup Guard bridges all tags of the remote and keeps the old target of every moved or deleted tag. |
| R9 | Releases and packages | ◐ | – | Partly | The agents' PR-only token can't create releases. **Not covered:** package registries, release assets. |
| R10 | Dependency confusion | ◐ | – | Partly | Registry configuration and manifest changes are `yellow`. **Not covered:** resolution at build time. |
| R11 | GitHub Pages | ◐ | – | Partly | Pages pushes are checked (new external scripts are `yellow`). **Not covered:** subtle phishing content. |
| R12 | Token reach | ✅ | – | Covered | By architecture: an agent only pushes to repos assigned to it, the write credential stays on the wall. |
| R13 | Settings, visibility, access | ✅ | – | Covered | By architecture: the agents' token can only open PRs. Settings themselves are out of scope. |
| R14 | Exfiltration | ◐ | – | Partly | No gists or repos without a token. **Not covered:** network exfiltration (the wall's job) and data in PR texts. |
| R15 | Prompt injection of the working agent | ◐ | – | Partly | The cause lies outside; its consequences in the repo go through the rules. |
| R16 | Local CLIs, token theft | ◐ | – | Partly | No write token left on the agents' machine. **Not covered:** malware on that machine. |
| R17 | Slopsquatting | ◐ | – | Partly | Manifest and lockfile changes are `yellow`. **Not covered:** a registry check of new dependencies (not built). |
| R18 | Mistakes without an attacker | ✅ | ✅ | Covered | Prevented or preserved for Git. **Not covered:** databases and cloud resources. |
| R19 | Naive mirrors | – | ✅ | Covered | The Backup Guard's purpose: everref only creates and fast-forwards refs in the backup, so an upstream force push or deletion becomes a new lineage or a tombstone, and an unreachable remote is an error, never a deletion. |
| R20 | Compromised bot with write access | ◐ | ✅ | Partly | The Push Guard holds a write credential and is itself a target (per repo, contents only); the Backup Guard holds only a read credential, and its backup is on its own host. |
| R21 | AI gatekeepers can be persuaded | – | – | Not covered | Git Warden has no AI gatekeeper, so there is none to persuade; AI reviewers you run next to it stay exposed. See [R21 in detail](#r21-in-detail-how-ai-gatekeepers-were-persuaded). |
| R22 | Non-Git data | ◐ | – | Partly | The agents' PR-only token can't touch issues or releases. **Not covered:** a backup of issues, PRs and releases; the Backup Guard backs up Git refs only. |

**Honestly:** the Push Guard lifts R1, R7, R8, R12 and R13 from "detect" to "prevent" only because agents no longer have their own write credential for the remote. That is half architecture and only half verdict. The Backup Guard preserves, it never prevents: what it saves has to be restored by a human.

## Open questions

Decided:

- **Notification and approval:** warnings go through a configurable command (`notify.command`), so the channel is the owner's choice. Approval happens on the wall host with `push-guard approve`, never through issues.
- **Two guard posts only:** Push Guard and Backup Guard; no AI decides anything in Git Warden.
- **Backup Guard = a git-everref run:** no own backup format and no AI; `backup-guard` drives everref and adds preflight, branch selection, logging and warnings. Missing features are contributed upstream. Cadence: every 15 minutes by default.
- **git-everref is a prerequisite, like gitleaks (confirmed):** on `PATH` or at `everref.path`, fail closed when missing, no runtime download; an optional pinned install script with SHA-256 ([Installing git-everref](#installing-git-everref)).
- **Both guards check their prerequisites at start:** the Backup Guard in its preflight, the Push Guard in `serve` and `init-repo` and again per push in the hook ([Preflight](#preflight)).
- **Pinned provisioning instead of runtime fetching:** a Dockerfile and a Nix flake bring git, gitleaks 8.30.1 and git-everref v1.0.0 at pinned versions ([Provisioning](#provisioning-docker-and-nix)).
- **Retention:** pending bundles, the backups and offsite bundles are kept forever for now; disk space is cheap compared with lost history.
- **gittuf and git-ratchet:** not in the first stages; possibly later as an extra signal.

Still open (none of it blocks either guard):

- Where the Push Guard and the Backup Guard run (not on the agents' machine either way).
- The notification channel behind `notify.command`.
- The offsite target with write-once retention for the bundles.
- Whether git-everref takes the upstream gaps; until then very large repos stay out of the Backup Guard.
