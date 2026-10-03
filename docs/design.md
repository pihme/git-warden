# Git Warden: design

The design of all three guard posts. Only the Push Guard is built so far; [SPEC.md](../SPEC.md) records what its implementation settled or changed, and [push-guard-rules.md](push-guard-rules.md) gives the reasoning per rule. Risk IDs (R1–R22) refer to [risks.md](risks.md).

## The idea in one paragraph

Git Warden sits **between AI agents and their Git remote**, whether that is GitHub, GitLab, Gitea/Forgejo or a bare repo over SSH, at three places:

- **Push Guard**, on the wall around the agent: agents push to it instead of the remote, and only it holds a write credential for the remote. It forwards a push or rejects it, with deterministic rules only, while the push is running.
- **Merge Guard**, in the CI pipeline: a required check before merging, with rules plus a sealed-off AI judge that can only make a verdict stricter.
- **Pull Guard**, a backup: a scheduled, pinned [git-everref](https://github.com/daojyun/git-everref) run that records every branch and tag of the remote in an append-only backup, so a force push or deletion upstream never loses anything. It decides nothing and needs no AI.

Push Guard and Merge Guard are separate programs that share one decision core as a library; the Pull Guard is an existing tool on a timer. **Policy: `red` goes to a human, `yellow` is the agent's job.** `red` is a rule violation; a human is warned and can approve the exact SHA, which should be the exception. `yellow` goes back to the agent with the rule message, and it fixes the change or leaves the part out and notes in its PR that this is still open. Only if it keeps getting it wrong does the escalation reach a human. Which rule is `red` and which `yellow` is configuration, per repo. When a guard rejects a push, the agent sees that the push didn't go through, but it can keep working.

## Goals

1. **Prevention where possible:** agent changes reach the remote only through the Push Guard.
2. **No data loss** (R1, R2, R19): whatever was ever in the Pull Guard's backup never disappears.
3. **Early detection** of what can't be undone (R4, R7, R13, R14).
4. **Minimal credentials:** Pull Guard read-only; Merge Guard only allowed to set a status check; Push Guard a narrowly scoped write credential that never gets into the agent's environment.
5. **Robust against persuasion** (R21): a persuaded judge at worst produces false alarms or lets a `yellow` case through, never a `red` case.
6. **The agent can always keep working:** a rejected push, a slow verdict or a warden outage can stop a push from reaching the remote, but never stops the agent's work. Its commits stay with it, and it pushes again later.
7. **Set up once, then peace:** the owner approves nothing in advance. They get warnings and look at them when it suits them.

## Principles

- **Keep it simple, and take the 80 % first:** v1 is only the Push Guard with deterministic rules. Its `red` rules (rewrites, deletions, tag moves, workflows, secrets) catch most of the damage without an LLM. Pull Guard and Merge Guard come as separate stages.
- **Bricks, not a stack:** the decision core is a library without platform knowledge. Each guard post can be left out or replaced; the Pull Guard already is an existing tool, which could be swapped for e.g. [Gitea Mirror](https://github.com/RayLabsHQ/gitea-mirror) with *Block & Approve*.
- **Batteries included, assembly required:** rules, path lists and limits are configuration with sensible defaults, not code.
- **Plain Git first:** the warden needs nothing but Git (smart HTTP or SSH, refs, objects, `fetch`, `push`), so any Git remote works. Even the default branch is visible through plain Git (`git ls-remote --symref <remote> HEAD`). Only the Merge Guard needs the platform API, because pull requests and checks exist only there; it uses the platform's existing MCP server ([GitHub](https://github.com/github/github-mcp-server), [GitLab](https://docs.gitlab.com/user/gitlab_duo/model_context_protocol/mcp_server/), [Gitea](https://gitea.com/gitea/gitea-mcp)) rather than a client of its own.

**Wall** means the isolation boundary around the agent's environment: whatever runs there (the Push Guard, its configuration and credentials) the agent can't reach or change. [Hermetarium](https://github.com/pihme/hermetarium) is one such environment, not a requirement.

## Prerequisites and other writers

1. **The agents' platform token can only open PRs, never write content.** The warden doesn't check this; it comes from how credentials are handed out. The GitHub MCP server has tools that write files through the API (`push_files`, `create_or_update_file`); with content write access an agent would bypass the Push Guard. On GitHub: a fine-grained token with "Pull requests: write" and "Contents: read" only.
2. **No assumption about other writers.** Collaborators, the owner and other agents may still push directly. The Push Guard doesn't see them; the Merge Guard checks their PRs, and the Pull Guard preserves whatever they overwrite or delete.

## Non-goals

- Revoking secrets, unpublishing packages, automatically restoring to the remote (a human does that).
- Fully backing up issues, PR discussions and releases (R22), not in v1.
- Monitoring package registries; a general SIEM.
- Settings outside Git (visibility, new repos, protection rules, webhooks, collaborators).
- What agents do through the platform API (PR texts, comments): that is up to the MCP server and the platform.

## Prior art (summary)

| Category | Examples | What's missing |
| --- | --- | --- |
| Mirror/backup | [Forgejo/Gitea mirror](https://forgejo.org/docs/latest/user/repo-mirror/), [ghorg](https://github.com/gabrie30/ghorg), [gickup](https://github.com/cooperspencer/gickup), [python-github-backup](https://github.com/josegonzalez/python-github-backup) | Sync blindly (R19). |
| Mirror with force-push protection | [Gitea Mirror](https://github.com/RayLabsHQ/gitea-mirror) *Block & Approve* | Pauses instead of preserving; force pushes only, no content check, fail-open. |
| Append-only ref backup | [git-everref](https://github.com/daojyun/git-everref) (MIT), [Amber](https://github.com/pmaxhogan/amber), [Software Heritage](https://www.softwareheritage.org/) | everref **is the Pull Guard**; at the scale of a large repo it still needs batched pushes and all-refs selection. Software Heritage: public repos only, its own schedule. |
| SaaS backup | Rewind/BackHub, GitProtect | No check before backing up; data sits with a third party. |
| Cryptographic ref history | [gittuf](https://github.com/gittuf/gittuf), [git-ratchet](https://github.com/project-oak/git-ratchet), [gitsign](https://github.com/sigstore/gitsign) | Needs discipline on every push; no content verdict. Good extra signal later. |
| Server-side push checks | Git `pre-receive` hooks, Gerrit `refs/for/…`, GitHub push protection and push rulesets | The pattern for the Push Guard; no verdict on content beyond secrets, GitHub side with admin bypass. |
| AI review | CodeRabbit, Copilot code review, PR-Agent, claude-code-security-review | PRs only; hold write permissions (R20); persuadable (R21). |

**Gap:** no tool we found combines a push proxy, a merge check and a never-destructive backup through one decision core: deterministic floor, the LLM may only tighten, fail-closed, judge without platform permissions.

## Shared decision core

Push Guard and Merge Guard use the same library function: `decide(delta, context, guard) → verdict`.

1. **Normalise the delta:** ref changes (`create`, `delete`, `ff`, `non_ff`, `tag_move`), changed paths, the cumulative diff against the remote's current state (Push Guard) or the PR base (Merge Guard), not just per commit; metadata and size.
2. **Deterministic rules** → findings `red` / `yellow` / `green`.
3. **`red`** → hold right away. No LLM is asked; only a human can approve the SHA.
4. **`yellow`** (and samples of `green`) → the **judge**, in the Merge Guard only; the Push Guard has no judge and rejects `yellow` with the rule message. The agent writes its own commit messages and PR texts, the easiest way to talk a judge into a verdict (R21), so the judge looks twice:
   - **Pass A, code only:** the diff and the deterministic findings, without PR text, commit messages and newly added comments. This makes persuasion harder, not impossible: text aimed at the judge can also sit in strings, docs or test data.
   - **Pass B, code and texts:** the texts are added, marked as "claimed by the author, unverified", so the judge can see whether they match the code.
   - In both passes, text addressed to a reviewer or an AI is itself a reason to hold.
   - `promote` only if **both** passes say so; any disagreement → hold.
5. **Monotonicity:** the LLM can turn `green`/`yellow` into hold, never `red` into promote.
6. **The judge is optional** (`judge.enabled`, globally or per repo); without it the core is the plain rule check of the Push Guard.
7. **Fail-closed:** LLM error, timeout, invalid output, budget exhausted, delta over the limit → hold.
8. **Output:** a fixed JSON schema (`decision` ∈ {`promote`, `hold`}, `reasons[]` from a fixed list, a truncated `note` shown to the human as an untrusted quote).
9. **Human approval** only through an authenticated channel, stored in the warden's own log. "Already reviewed by the security team" in repo text never counts.
10. **The approval is itself an attack surface:** the agent can write texts meant to get the owner to approve. Warnings show the facts first (findings, paths, ref changes) and the agent's text only as a marked, unverified quote.

### Rules (excerpt)

| Level | Trigger |
| --- | --- |
| **red** | `non_ff`, `delete`, `tag_move`; CI configuration (`.github/workflows/**`, `.gitlab-ci.yml`, …), `CODEOWNERS`, `LICENSE`; agent configuration (`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.cursor/rules/**`, `.github/copilot-instructions.md`); secrets ([gitleaks](https://github.com/gitleaks/gitleaks)); invisible Unicode and bidi characters ([Trojan Source](https://trojansource.codes/)); delta over the limit; judge error |
| **yellow** | Lockfiles and manifests, `.npmrc`, Dockerfiles, build scripts; binary files (also in test directories, cf. xz); long base64/hex literals; mass deletion; a missing signature where commits used to be signed; backdated commits; unusual volume; new external scripts on the Pages branch |
| **green** | Fast-forward, uncritical paths, no findings |

The full Push Guard rule set is in [push-guard-rules.md](push-guard-rules.md). Later the core also checks new dependencies against the registry (exists? age? downloads?) for R17; that needs network access, so it is not in the Push Guard.

**Trade-off:** in many repos agents maintain `AGENTS.md`. With the default above every such change needs a human; a repo can move these paths to `yellow` in its configuration.

## The three guard posts

```
Push Guard     on the wall
  ├── Git smart HTTP (or SSH) endpoint for agents → rules + secret scan → forward or reject
  ├── pending/<repo>/<id>.bundle   rejected pushes, kept for a human
  ├── push.jsonl                   append-only event log
  └── write credential per repo, only here, never in the agent's environment
      (no AI judge: rules only, decides in seconds)
Merge Guard    in the CI pipeline (how exactly it runs is still open)
  └── PR → core (rules + judge) → required check "warden"
Pull Guard     backup host (neither the wall nor the agents' machine)
  ├── timer every 15 min → git everref run --all   (bridge mode, pinned version)
  ├── bridge clone   origin = the remote, read-only credential
  ├── backup repo    append-only: lineages, tombstones, journals
  └── offsite        periodic git bundle of the backup repo
      (no rules, no AI judge: preserves, decides nothing)
Judge          used by the Merge Guard only: no shell, no tools, no repo checkout, model API only
Notifier → human (shared by all three)
```

The wall lets the agents' Git traffic through only to the Push Guard; Git traffic to the remotes' hosts is blocked.

### Push Guard

Built: see the [README](../README.md) and [SPEC.md](../SPEC.md). In short: the agent pushes to the Push Guard, which decides in its `pre-receive` step. It reads the remote's refs fresh, runs the rules and gitleaks over the new commits (the earliest possible moment: afterwards a secret is already on the remote), and then forwards exactly the checked SHAs with its own credential (never `--force`), rejects `yellow` with the rule message, or rejects `red` silently and warns the owner.

- **Credentials:** no more rights on the platform than the agent would have, so the remote's own protection applies to the warden too. In plain Git an SSH deploy key with write access per repo; on GitHub optionally an App with only `contents:write` + `metadata:read` and no `workflows` permission, whose tokens expire after an hour, so GitHub rejects workflow changes even if the rules fail.
- **Making it non-bypassable:** agents hold **no** other write credential for the remote (a `gh` token in the agent's home directory has to go), and the wall blocks Git traffic to the remotes' hosts. A local `pre-push` hook is no guard: `git push --no-verify` skips it.
- **Bypass risks:** an overlooked token (`.git-credentials`, environment variable, CI secret); agents outside the wall; other writers pushing directly. The Push Guard itself is a target (R20) because it holds a write credential.
- **Failure mode:** Push Guard down → the agent's push fails, its commits stay local, it pushes again later; a heartbeat alerts the owner. Nothing goes to the remote unchecked.

### Merge Guard

Needs the platform API throughout: plain Git has no pull requests and no checks. It sees what the Push Guard can't: CI results, and PRs from writers who never went through the Push Guard. Two ways to run it, still open: a CI job in the repo (with `pull_request_target`, so a PR can't rewrite its own check) or an external check through the platform API. External variant, with GitHub as the example:

- **Trigger:** App webhooks `pull_request` (opened, synchronize, reopened), with a merge queue also `merge_group`; the receiver runs outside the repo, so an agent that can edit workflows can't switch it off.
- **Inputs:** base and head SHAs fetched read-only; the cumulative diff; PR metadata (removed for pass A). The PR code is never executed.
- **Can block:** the merge, via a check run `warden` that a ruleset requires, pinned to the warden's App so no other actor can fake it.
- **Credentials:** `contents:read`, `pull_requests:read`, `checks:write`, `metadata:read`.
- **Bypass risks:** admins bypass rulesets unless the bypass list says otherwise; the owner can change the ruleset; direct pushes skip PRs if the ruleset doesn't require one. Private repos on GitHub's free plan have no rulesets, and merge queues need an organisation.
- **Failure mode:** warden down → the check stays pending and the merge waits (fail-closed); work on the branches continues.

### Pull Guard

The Pull Guard is not a program of its own. It is a scheduled [git-everref](https://github.com/daojyun/git-everref) run in bridge mode: on each run everref fetches the remote and records every branch and tag in an append-only backup repo. No rules, no AI judge, no database. Browsing and restoring is tooling that may come later, with a web UI.

**How everref works** (v1.0.0, [design](https://github.com/daojyun/git-everref/blob/main/docs/design.md)): per protected ref the backup repo gets event refs `refs/heads/everref/remotes/origin/<branch>/created_<unix-ts>` (a lineage that only fast-forwards; a rewrite freezes it and starts a new one) and `…/deleted_<unix-ts>` (a tombstone), tags the same under `refs/tags/everref/…`, plus a journal per ref (one tiny commit per observed tip move). Its only writes are create and fast-forward, so Git itself refuses anything destructive. `restore --at <time>` rebuilds the refs as of the last run before that time and never overwrites. An unreachable source is an error, never a deletion.

| Aspect | Description |
| --- | --- |
| **Trigger** | Every 15 minutes: a systemd timer generated by everref, or cron calling `git everref -C <clone> run --all`. |
| **Can block** | Nothing. A force push or deletion upstream becomes a new lineage or a tombstone; the old tip stays reachable. |
| **Warnings** | v1 warns only when a run fails (non-zero exit). Later, with the tooling: rewrite of the default or a release branch, a tag moved or deleted, many branches deleted at once, default branch changed. |
| **Credentials** | Read-only for the remote. The backup repo is local. |
| **Version** | Pinned and checksum-verified when the host is provisioned: release `v1.0.0`, `git-everref_1.0.0_linux_amd64.tar.gz` SHA-256 `0f87be25d89a31b23d1851edc917751023d56ab10925f1fe695afd8b31c33861`, or built from tag `v1.0.0` (commit `f16c812`). Git Warden doesn't download it. |
| **Storage** | Kept forever (everref never deletes a backup ref); `gc` is safe. Periodically `git bundle create --all` of the backup repo to offsite storage with write-once retention. |
| **Limits** | A state that exists only between two runs is never seen; history rewritten before the first run; LFS objects and submodule targets are not in the backup. |
| **Failure mode** | Fetch error, expired credential, disk full → non-zero exit → warning, never a silent skip. A missed run → heartbeat alerts. |

The backup is a dedicated bare repo, never a repo on a hosting platform: everref's event refs sit under `refs/heads/` and `refs/tags/`, so a clone would show them as branches.

#### Measured on pytorch/pytorch, September 2026

From GitHub's repository activity API (complete for branches, without tag creations; pytorch's bot moves its `ciflow/*` tags by delete and re-create, which was modelled). GH Archive carried only about 4–9 % of the ref events in 2026, too few for this. pytorch has about 9,800 branches and 11,500 tags.

| Run interval | Changed or deleted tips per month | Peak per run | Of these rewritten, moved or deleted | Missed intermediate states |
| --- | --- | --- | --- | --- |
| 15 min | 68,691 | 297 | 48,675 | 4,450 |
| 1 h | 59,088 | 524 | 42,265 | 14,542 |
| 1 day | 24,300 | 1,339 | 19,303 | 51,449 |

At 15 minutes, microsoft/vscode has about 6,400 changed tips per month and rust-lang/rust about 2,000.

**Git itself is not the bottleneck.** A prototype on a pytorch mirror: a run that finds nothing takes about 3 s; one `update-ref --stdin` transaction with 500 updates under 0.3 s. With 1,000,000 snapshot refs (about 14 months of pytorch), reftable needs 47 MB and packed refs 107 MB, listing all refs takes 1.6–1.9 s and a run 5.5–5.8 s; a clone of the mirror transfers none of them. Without packing the files backend breaks down (4.7 GB of loose refs), so a backup host packs refs regularly or uses reftable.

**everref v1.0.0 doesn't keep up with a repo that size yet:** `add` runs one `ls-remote` per ref (51 s for 200 branches), and `run` writes every event ref and journal entry with its own `git push` (about 4.7 s each): about two days for a first run of pytorch and about 8 hours for a normal weekday's changes. Size is not the problem. For small repos with a handful of branches v1.0.0 works as it is.

#### Gaps to contribute upstream

In this order, offered to git-everref rather than worked around:

1. **Batched writes:** one `git push --atomic` per run and backup remote instead of one push per ref.
2. **All refs of a remote:** protect all branches and tags of a remote, including ones that appear later.
3. **Batched reads:** one `ls-remote` / `fetch` per remote and run (also in `add`).
4. **Exclude patterns** for bot churn (e.g. `refs/tags/ciflow/*`).
5. **Optional single journal per remote** (one commit per run): "the whole repo at time T" in one lookup.
6. **Query commands:** state at T, and the diff between T1 and T2, without restoring.
7. **`--version` of a source build** should carry the tag, not `dev`.

**Risks of depending on everref:** one maintainer and a young project. It is MIT-licensed, so it can be forked; the version is pinned, and Git Warden relies only on its CLI and documented ref layout. Event order trusts the host clock.

**Fallback, if upstream doesn't take the changes:** a bare mirror fetches into `refs/incoming/*`, then one `git update-ref --stdin` transaction updates `refs/heads` and `refs/tags` and creates `refs/snapshots/<timestamp>/heads|tags/<name>` for every tip that is rewritten, moved or deleted (fast-forwarded tips stay reachable anyway). An append-only timeline ref (one commit per run whose tree mirrors the ref namespace) answers "where did X point at T". Never `--mirror` with `--prune`, which would delete the snapshots. The prototype numbers above come from this design.

## Rollout stages

1. **Push Guard** with rules and secret scan: forward or reject, no LLM. This includes removing every other write credential from the agents' machines, otherwise the Push Guard is decoration.
2. **Pull Guard:** a scheduled git-everref run; large repos once the first upstream gaps are closed.
3. **Merge Guard:** rules plus the AI judge, where the platform has required checks and PRs are used at all.

Each stage is useful on its own; none requires the next.

## Data model

| Entity | Fields (excerpt) |
| --- | --- |
| `Repo` | `name` (chosen by the owner), `remote` (any Git URL), `default_branch`, `guards[]` |
| `Event` | `id`, `guard` (`push`/`merge`/`pull`), `repo`, `actor` (agent identity or human), `received_at` |
| `RefChange` | `event_id`, `ref`, `old_sha`, `new_sha`, `kind` |
| `Finding` | `event_id`, `rule_id`, `severity`, `ref`, `path`, `evidence` (facts only) |
| `Verdict` | `event_id`, `source` (`rules`/`llm_a`/`llm_b`/`human`), `decision`, `reasons[]`, `model`, `tokens`, `cost` |

No database: the Push Guard keeps `push.jsonl`, its Git repositories and bundles of rejected pushes; the Pull Guard keeps everything as Git refs in the backup repo plus offsite bundles.

## Prompt injection resilience

The persuasion techniques documented so far and the countermeasure for each are under [R21 in risks.md](risks.md#r21-in-detail-how-ai-gatekeepers-were-persuaded). In short: redacting metadata, two passes, monotonicity, no tools or permissions for the judge, no repo configuration loaded as instructions, normalising invisible characters, fixed output without links or images, size and budget limits that lead to hold. Assumption: the attacker can practise offline against a copy, so the core never relies on the LLM alone.

## Failure modes (overall)

| Failure mode | Consequence | Countermeasure |
| --- | --- | --- |
| A guard down | Push Guard: pushes fail, agents keep working locally. Merge Guard: merges wait. Pull Guard: backups pause | Nothing goes to the remote unchecked; heartbeat |
| Push Guard or Pull Guard host compromised | Write credential or backup gone | Separate hosts, neither on the agents' machine; offsite write-once bundles; rotatable write keys |
| Alert fatigue | A human waves things through | `red` used sparingly, digests, facts first |
| False negative | A subtle backdoor gets through | The backup keeps the history; human review still needed |
| Judge persuaded (Merge Guard) | `yellow` → promote | Monotonicity, two passes, strong model |
| Notification channel spoofed | False approval | Authenticated channel; approvals never through issues |
| Private repos sent to the LLM provider | Data reaches a third party | `judge.enabled: false` per repo |
| Budget exhausted | Verdicts don't arrive | Merges wait and the owner is warned; pushes and backups are unaffected |

## Risk coverage per guard post

Legend: **✅** prevents (Push, Merge) or preserves (Pull) · **◐** partly · **–** no. Push Guard values apply **only if agents have no other write credential for the remote**. The Pull Guard only preserves, so it detects nothing on its own.

| ID | Risk | Push | Merge | Pull | Reasoning |
| --- | --- | --- | --- | --- | --- |
| R1 | Destroying history | ✅ | – | ✅ | Prevented for agents (non-FF and delete are `red`); Pull preserves against other writers and stolen tokens. |
| R2 | Deleting or damaging code | ◐ | ◐ | ✅ | Covered by the backup; prevention only for mass deletion. |
| R3 | Subtle bugs and backdoors | ◐ | ◐ | – | Partly; an AI verdict can be outwitted (R21). |
| R4 | Secrets in commits | ◐ | ◐ | – | Push: prevention for recognisable formats (the secret never reaches the remote); scanner gaps remain. |
| R5 | Licence | ◐ | ◐ | – | A `LICENSE` change is `red`; copied-in third-party code is hard to detect. |
| R6 | Spam | ✅ | – | – | Rate limit per agent; the PR-only token allows no issues. |
| R7 | CI/CD workflows | ✅ | ◐ | – | `red`, plus an App without `workflows` permission; Merge alone isn't enough (workflows already run on the branch push). |
| R8 | Moving tags | ✅ | – | ✅ | Prevented; Pull keeps old tags. |
| R9 | Releases and packages | ◐ | – | – | The PR-only token can't create releases; registries are outside. |
| R10 | Dependency confusion | ◐ | ◐ | – | Registry and manifest changes are `yellow`; resolution at build time is invisible. |
| R11 | GitHub Pages | ◐ | ◐ | – | Pages pushes are checked; subtle phishing can slip through. |
| R12 | Token reach | ✅ | – | – | By architecture: an agent only pushes to repos assigned to it. |
| R13 | Settings, visibility, access | ✅ | – | – | By architecture (the agents' token can only open PRs), not by a verdict. |
| R14 | Exfiltration | ◐ | – | – | No gists or repos without a token; network exfiltration is the wall's job. |
| R15 | Prompt injection of the working agent | ◐ | ◐ | – | The cause lies outside; its consequences in the repo are checked. |
| R16 | Local CLIs, token theft | ◐ | – | – | No write token left on the agents' machine; malware there is not covered. |
| R17 | Slopsquatting | ◐ | ◐ | – | Registry check of new dependencies (later). |
| R18 | Mistakes without an attacker | ✅ | ◐ | ✅ | Prevented or preserved for Git; databases and cloud not. |
| R19 | Naive mirrors | – | – | ✅ | The Pull Guard's purpose. |
| R20 | Compromised bot with write access | ◐ | ◐ | ✅ | The Push Guard holds a write credential and is a target (per repo, contents only); Merge only `checks:write`; Pull read-only. |
| R21 | AI gatekeeper persuaded | ◐ | ◐ | – | Damage limited to `yellow` cases; not solved. |
| R22 | Non-Git data | ◐ | – | – | The PR-only token can't touch issues or releases; a backup comes later. |

**Honestly:** the Push Guard lifts R1, R7, R8, R12 and R13 from "detect" to "prevent" only because agents no longer have their own write credential for the remote. That is half architecture and only half verdict.

## Open questions

Decided:

- **Notification and approval:** warnings go through a configurable command (`notify.command`), so the channel is the owner's choice. Approval happens on the wall host with `push-guard approve`, never through issues.
- **Pull Guard = git-everref:** no own backup product and no AI judge; missing features are contributed upstream. Cadence: every 15 minutes by default.
- **Retention:** pending bundles, the backup and offsite bundles are kept forever for now; disk space is cheap compared with lost history.
- **gittuf and git-ratchet:** not in the first stages; possibly later as an extra signal.

Still open (none of it blocks the Push Guard):

- Where the Push Guard and the Pull Guard run (not on the agents' machine either way).
- The notification channel behind `notify.command`.
- The offsite target with write-once retention for the bundles.
- For the Merge Guard: LLM budget, and whether private repos may be sent to an LLM provider at all.
- How the Merge Guard runs: CI job or external check.
- Whether git-everref takes the upstream gaps; until then large repos stay out of the Pull Guard.
