# Push Guard: rules and reasoning

The reasoning behind every Push Guard rule. Configuration and operation (rule table, configuration, commands including `replay`) are in [docs/push-guard.md](push-guard.md), the overview for both guards in the [README](../README.md#configuration-and-operation); what the implementation settled or changed is in [SPEC.md](../SPEC.md). Design of both guard posts and the risk register (risk IDs R1–R22): [SPEC.md](../SPEC.md).

Focus for now: only the Push Guard, only deterministic rules. **No AI at all:** the Push Guard has to decide fast, pass or fail, while the push is running, and the same input must always give the same answer. No LLM, no platform API, nothing but Git. Every rule below can be decided from the pushed objects and the remote's current refs alone, with the same result every time. Testable requirements for QA are numbered as `REQ-PG-*` in [Requirements](#requirements).

## Assumption: one Push Guard per agent

For now, each monitored agent gets its own Push Guard. Its configuration names the agent (`agent.name`), and that name goes into every log entry, warning and statistic so they can be traced back later. Everything "per agent" below therefore means "per Push Guard".

## Rights

**The warden has no more rights on the platform than the agent would have.** Whatever the remote refuses the agent, it refuses the warden too, so a human approval can't push past the remote's own protection; that goes through the human's own push or a merged PR. Protected branches are the remote's job; the Push Guard sticks to what Git itself offers and has no rule for them. If the remote rejects a forwarded push, the Push Guard rejects the agent's push and passes on the remote's message.

## Verdict

- Any `red` finding → **reject without details.** The response only says that the push was rejected and is waiting for a human; it doesn't name the rule. The owner is warned.
- Any `yellow` finding → **reject with an error message the agent can use** to make its change compatible: which rule fired, on which ref, path or line, and why. **`yellow` never goes to a human:** the agent fixes it itself. If it can't, it leaves the part out (drops the file or dependency, doesn't set the tag) and notes in its PR that this is still open. Only if it keeps getting it wrong does `RATE-YELLOW-STREAK` escalate to a human.
- Only `green` → forward exactly the checked SHAs to the remote, then accept the agent's push. Never `--force`: a rewrite, tag move or deletion goes out with `--force-with-lease` against the remote state just read, and only if the rule's `allow` explicitly exempts that ref or a human approved that SHA.
- **Fail-closed:** a rule that errors, times out or can't read its input (scanner missing, remote unreachable, object missing) counts as `red`, but with the message "internal error, try again later". The next push is checked from scratch.
- **Approval is per SHA:** a human approves `ref` + `new-oid`. The agent's next push of exactly that SHA to that ref is forwarded without re-checking. Any other SHA (rebase, amend, extra commit) is checked from scratch.

**Policy in short:** `red` is a rule violation and goes to a human, who changes the classification only in exceptional cases. `yellow` is the agent's job.

Why `red` stays silent: a `yellow` message helps an honest agent fix its change, but it also teaches a manipulated agent what the rules look for. For `red` cases that lesson would be too valuable, so the agent only learns that the push didn't go through.

## Requirements

Testable Push Guard behaviours for QA. **Requirement IDs** (`REQ-PG-*`) are stable: never renumber; only append. They are a separate layer from **rule IDs** (`REF-*`, `PATH-*`, …) and from SPEC **risk IDs** (`R1`–`R22`); do not reuse those namespaces.

Each requirement is one shall/must behaviour. Where a rule ID applies, it is named. Settled quirks from [SPEC.md § Push Guard rules](../SPEC.md#push-guard-rules) are included so tests match the implementation.

| ID | Shall / must | Rule / notes |
| --- | --- | --- |
| `REQ-PG-001` | Any `red` finding shall reject the push **without naming the rule**; the response only states rejection and that a human is needed; the owner shall be warned. | Verdict |
| `REQ-PG-002` | Any `yellow` finding shall reject with an agent-usable message (which rule, on which ref/path/line, and why). `yellow` shall **not** go to a human. | Verdict |
| `REQ-PG-003` | Only `green` shall forward **exactly the checked SHAs** to the remote, then accept the agent's push. Forwarding shall never use `--force`. A rewrite, tag move or deletion shall use `--force-with-lease` against the remote state just read, and only if the rule's `allow` exempts that ref or a human approved that SHA. | Verdict |
| `REQ-PG-004` | A rule that errors, times out or cannot read its input shall count as `red` with the message `internal error, try again later` (**fail-closed**). The next push shall be checked from scratch. | Verdict |
| `REQ-PG-005` | Human approval shall be **per SHA**: approve `ref` + `new-oid`. The agent's next push of exactly that SHA to that ref shall be forwarded without re-checking. Any other SHA shall be checked from scratch. | Verdict |
| `REQ-PG-006` | `REF-DELETE` shall fire `red` when a branch or tag is deleted. | `REF-DELETE` |
| `REQ-PG-007` | `REF-NON-FF` shall fire `red` when a branch moves to a commit that does not contain its old commit. | `REF-NON-FF` |
| `REQ-PG-008` | `REF-TAG-MOVE` shall fire `red` when an existing tag is pointed elsewhere. | `REF-TAG-MOVE` |
| `REQ-PG-009` | `REF-TAG-NEW` shall fire `yellow` when a new tag is created. | `REF-TAG-NEW` |
| `REQ-PG-010` | `REF-NAMESPACE` shall fire `red` when a ref is outside `refs/heads/*` and `refs/tags/*`. | `REF-NAMESPACE` |
| `REQ-PG-011` | `REF-COUNT` shall fire `red` when a push contains more than `max_refs` refs (default 10). | `REF-COUNT` |
| `REQ-PG-012` | `PATH-RED` shall fire `red` when a changed path (added, modified, deleted or renamed) matches the rule's `match` list. | `PATH-RED` |
| `REQ-PG-013` | `PATH-YELLOW` shall fire `yellow` when a changed path matches the rule's `match` list. | `PATH-YELLOW` |
| `REQ-PG-014` | A rename shall be checked under **both** the old and the new path name for path rules. | `PATH-RED`, `PATH-YELLOW` |
| `REQ-PG-015` | A path that matches both `PATH-RED` and `PATH-YELLOW` shall be treated as `red` (`PATH-RED` overrides yellow). | `PATH-RED`, `PATH-YELLOW` |
| `REQ-PG-016` | `MODE-EXEC` shall fire `yellow` when a file becomes executable (mode `100755`). | `MODE-EXEC` |
| `REQ-PG-017` | `MODE-SYMLINK` shall fire `yellow` when a symlink (mode `120000`) is added or changed. | `MODE-SYMLINK` |
| `REQ-PG-018` | `MODE-SUBMODULE` shall fire `yellow` when a gitlink (mode `160000`) is added or changed. | `MODE-SUBMODULE` |
| `REQ-PG-019` | `CONTENT-SECRET` shall fire `red` when gitleaks finds a secret in the new commits (`gitleaks git --log-opts="<range>"`). Every commit in the range shall count, including a secret added and later removed. | `CONTENT-SECRET` |
| `REQ-PG-020` | `CONTENT-SECRET` shall run only with wall scanner configuration (`--config`, `--gitleaks-ignore-path`, `--ignore-gitleaks-allow`), never with the repo's `.gitleaks.toml`, `.gitleaksignore` or inline `gitleaks:allow`. | `CONTENT-SECRET` |
| `REQ-PG-021` | `CONTENT-SCANNER-ALLOW` shall fire `red` when an added line introduces an inline secret-scanner exception (`gitleaks:allow`, `trufflehog:ignore`). | `CONTENT-SCANNER-ALLOW` |
| `REQ-PG-022` | `CONTENT-INVISIBLE` shall fire `red` on Bidi controls (U+202A–U+202E, U+2066–U+2069), zero-width characters (U+200B–U+200D, U+2060), U+FEFF outside the first byte, or Unicode tag characters (U+E0000–U+E007F) in added lines. | `CONTENT-INVISIBLE` |
| `REQ-PG-023` | `CONTENT-BINARY` shall fire `yellow` when a binary file is added or changed (`git diff --numstat` reports `-`), including under test directories. | `CONTENT-BINARY` |
| `REQ-PG-024` | `CONTENT-BINARY` shall also fire for added text that is not valid UTF-8 (undecodable text counts as binary). | `CONTENT-BINARY` (SPEC settled) |
| `REQ-PG-025` | `CONTENT-BLOB` shall fire `yellow` on a literal of 200+ characters of base64 or 100+ characters of hex on one added line. | `CONTENT-BLOB` |
| `REQ-PG-026` | `CONTENT-PAGES-SCRIPT` shall fire `yellow` on the Pages branch (`gh-pages` or configured) when a `<script src>` or `<link>` points to a new external host. | `CONTENT-PAGES-SCRIPT` |
| `REQ-PG-027` | For `CONTENT-PAGES-SCRIPT`, a host shall count as **new** if `git grep -i -F <host>` finds it nowhere in the remote's old tree of the Pages branch. | `CONTENT-PAGES-SCRIPT` (SPEC settled) |
| `REQ-PG-028` | `META-UNSIGNED` shall fire `yellow` when a commit has no signature and the last `lookback` commits (default 20) on that branch were all signed. Only signature **presence** counts; keys are not verified. | `META-UNSIGNED` |
| `REQ-PG-029` | For `META-UNSIGNED`, the lookback shall be first-parent commits of the remote's old state (default branch for a new ref or a tag). With fewer than `lookback` commits the rule shall stay quiet. | `META-UNSIGNED` (SPEC settled) |
| `REQ-PG-030` | `META-BACKDATED` shall fire `yellow` when the committer date is earlier than a parent's committer date, or more than 24 h before the push. Only the committer date shall be used. | `META-BACKDATED` |
| `REQ-PG-031` | `META-FUTURE` shall fire `yellow` when the author or committer date is more than 10 min after the push. | `META-FUTURE` |
| `REQ-PG-032` | `SIZE-LIMIT` shall fire `red` when the delta exceeds the hard limits: `max_files` 2,000, `max_lines` 50,000, `max_bytes` 50 MB of new objects, or `max_file_bytes` 10 MB for a single file (defaults; configurable). | `SIZE-LIMIT` |
| `REQ-PG-033` | `SIZE-LARGE` shall fire `yellow` when the soft limits are exceeded: `max_files` 200, `max_lines` 5,000, `max_commits` 100, or `max_file_bytes` 1 MB (defaults; configurable). | `SIZE-LARGE` |
| `REQ-PG-034` | `SIZE-MASS-DELETE` shall fire `yellow` when more than `max_deleted_files` (default 20) files are deleted, or more than `max_deleted_share` (default 50 %) of a file's lines are removed in files over `min_lines` (default 100). | `SIZE-MASS-DELETE` |
| `REQ-PG-035` | For `SIZE-MASS-DELETE`, the share check shall apply to **modified and renamed** files; fully deleted files shall count only toward `max_deleted_files`. | `SIZE-MASS-DELETE` (SPEC settled) |
| `REQ-PG-036` | `RATE-LIMIT` shall fire `red` when an agent exceeds `max_pushes` (default 30) pushes per `window` (default 1 h). The response shall say `rate limited, try later`. | `RATE-LIMIT` |
| `REQ-PG-037` | `RATE-YELLOW-STREAK` shall fire `red` when an agent exceeds `max_yellow` (default 5) `yellow` rejections in the same repo (all refs) within `window` (default 24 h). A human shall be warned; further pushes by that agent to that repo shall be `red` (no rule details) until a human approves one of its SHAs there or resets the streak. A `green` push shall **not** reset the streak. | `RATE-YELLOW-STREAK` |
| `REQ-PG-038` | Every rule shall be **on by default** and shall be disableable via `enabled: false`. | Configuration |
| `REQ-PG-039` | Each rule's `color` shall be configurable (`red` or `yellow`, default as in the rule tables). | Configuration |
| `REQ-PG-040` | `allow` shall be regexes over the rule's subject; a match means the rule does **not** fire for that subject. | Configuration |
| `REQ-PG-041` | `deny` shall win over `allow`: a `deny` match means the rule fires even if `allow` also matches. A rule fires if `deny` matches, or if it is triggered and `allow` does not match. | Configuration |
| `REQ-PG-042` | Patterns shall match the **whole** subject: each pattern is wrapped as `^(?:…)$` so partial matches do not exempt or deny unintended subjects. | Configuration |
| `REQ-PG-043` | Subjects shall be: full ref name for `REF-*`; path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*`; ref name for per-ref `SIZE-*` totals; branch (without `refs/heads/` for branches; full ref for tags) for `META-*`. `REF-COUNT` and `RATE-*` have no subject; `allow`/`deny` shall not apply to them. | Configuration |
| `REQ-PG-044` | Path and content rules shall run over the **cumulative diff**; metadata rules shall run over **each new commit**. | What a rule sees |
| `REQ-PG-045` | Remote state shall come from a fresh `git ls-remote --symref` per push; the agent's `old-oid` shall not be trusted. | What a rule sees |

## What a rule sees

For each ref in the push (`old-oid new-oid ref-name` from `pre-receive`):

| Input | How |
| --- | --- |
| Ref change kind | `create` (old = zero), `delete` (new = zero), `ff` (`git merge-base --is-ancestor old new`), `non_ff` (otherwise), `tag_move` (an existing tag points elsewhere); computed against the remote's old state, annotated tags peeled to commits |
| Remote state | `git ls-remote --symref <remote>`, read fresh for every push (the agent's `old-oid` is not trusted) |
| New commits | `git rev-list <new> --not <all remote refs>` |
| Cumulative diff | `remote-ref..new` for `ff`; from the merge base for `non_ff` and `tag_move`; for a new ref, `merge-base(default branch, new)..new` |
| Added lines | `+` lines of the cumulative diff, per path |
| Commit metadata | author, committer, dates, signature present or not, parents (`git cat-file commit`) |

Path rules and content rules run over the **cumulative diff**, so splitting a change across many small commits doesn't help. Metadata rules run over **each new commit**.

## Rules

IDs are stable so findings, approvals and config can refer to them. Every list, pattern and limit is configuration with the default shown. **Every rule is on by default and can be disabled** (`REQ-PG-038`), and most can be narrowed with allow and deny lists (`REQ-PG-040`, `REQ-PG-041`; see [Configuration](#configuration)). Testable requirements for each rule are in [Requirements](#requirements) and in the **Requirements** column of the tables below.

### Refs

| ID | Level | Trigger | Requirements |
| --- | --- | --- | --- |
| `REF-DELETE` | red | A branch or tag is deleted. | `REQ-PG-006` |
| `REF-NON-FF` | red | A branch moves to a commit that doesn't contain its old commit (force push, rewritten history). | `REQ-PG-007` |
| `REF-TAG-MOVE` | red | An existing tag is pointed elsewhere. | `REQ-PG-008` |
| `REF-TAG-NEW` | yellow | A new tag (tags can trigger releases). | `REQ-PG-009` |
| `REF-NAMESPACE` | red | A ref outside `refs/heads/*` and `refs/tags/*` (e.g. `refs/notes/*`, `refs/warden/*`, `refs/pull/*`). | `REQ-PG-010` |
| `REF-COUNT` | red | More than `max_refs` 10 refs in one push. | `REQ-PG-011` |

### Paths

Paths are classified generally, whatever the file is: a lockfile, a workflow or an agent config are all just entries in a list. Two rules, each with a default blocklist in the configuration:

| ID | Level | Trigger | Requirements |
| --- | --- | --- | --- |
| `PATH-RED` | red | A changed path matches the rule's `match` list (the blocklist). | `REQ-PG-012` |
| `PATH-YELLOW` | yellow | A changed path matches the rule's `match` list (the blocklist). | `REQ-PG-013` |

The subject is every path that is added, modified, deleted or renamed. **A rename is checked under both names** (`REQ-PG-014`), so `git mv` can't move a file out of a list or into one unnoticed. A path on both lists is `red` (`REQ-PG-015`). `allow` and `deny` work as for every rule (`REQ-PG-040`, `REQ-PG-041`): `allow` exempts a path from the blocklist, `deny` keeps it in even if `allow` matches. Like every list, both are regex (see [Configuration](#configuration)); paths have no leading slash, and `(.*/)?` means "in any directory".

Default blocklists:

```yaml
PATH-RED:
  match:
    # CI configuration
    - '\.github/workflows/.*'
    - '\.github/actions/.*'
    - '\.gitlab-ci\.yml'
    - '\.(forgejo|gitea)/workflows/.*'
    - '\.circleci/.*'
    - 'Jenkinsfile'
    # Ownership and licence
    - '(.*/)?CODEOWNERS'
    - '(LICENSE|COPYING)(\..*)?'
    # Secret scanner configuration (see Scanner configuration)
    - '\.gitleaks\.toml'
    - '\.gitleaksignore'
    - '\.trufflehog.*'
    # Agent configuration
    # (?i): case-insensitive, because on macOS and Windows a tool also opens agents.md as AGENTS.md
    - '(?i)(.*/)?(AGENTS|CLAUDE|GEMINI)\.md'
    - '(?i)\.cursor/rules/.*'
    - '(?i)\.github/copilot-instructions\.md'
PATH-YELLOW:
  match:
    # Git metadata
    - '(.*/)?\.gitattributes'
    - '\.gitmodules'
    - '\.lfsconfig'
    # Hooks that run on other people's machines
    - '\.husky/.*'
    - '\.githooks/.*'
    - 'lefthook\.yml'
    - '\.pre-commit-config\.yaml'
    # Dependencies and registries
    - '(.*/)?(package\.json|package-lock\.json|pnpm-lock\.yaml|yarn\.lock|go\.mod|go\.sum|Cargo\.toml|Cargo\.lock|pyproject\.toml|uv\.lock)'
    - '(.*/)?requirements[^/]*\.txt'
    - '(.*/)?(\.npmrc|\.yarnrc[^/]*|pip\.conf)'
    # Build
    - '(.*/)?Dockerfile[^/]*'
    - '(.*/)?docker-compose[^/]*\.ya?ml'
    - '(.*/)?Makefile'
    - '(scripts/)?[^/]*\.sh'
    - '\.github/dependabot\.yml'
```

### File modes

These depend on the file mode in the Git tree, not on the path, so they are separate rules. The subject is the path.

| ID | Level | Trigger | Requirements |
| --- | --- | --- | --- |
| `MODE-EXEC` | yellow | A file becomes executable (mode `100755`). | `REQ-PG-016` |
| `MODE-SYMLINK` | yellow | A symlink (mode `120000`) is added or changed. | `REQ-PG-017` |
| `MODE-SUBMODULE` | yellow | A gitlink (mode `160000`, a submodule) is added or changed. | `REQ-PG-018` |

### Content

Over added lines, after decoding as UTF-8 (undecodable text counts as binary — `REQ-PG-024`); `CONTENT-SECRET` scans the new commits, `CONTENT-BINARY` looks at whole files.

| ID | Level | Trigger | Requirements |
| --- | --- | --- | --- |
| `CONTENT-SECRET` | red | gitleaks finds something in the new commits: `gitleaks git --log-opts="<range>"`. Every commit counts, not just the final tree, so a secret added and removed again is still a hit. The scanner must ignore everything the repo says about it, see [Scanner configuration](#scanner-configuration). | `REQ-PG-019`, `REQ-PG-020` |
| `CONTENT-SCANNER-ALLOW` | red | An added inline exception for the secret scanner (`gitleaks:allow`, `trufflehog:ignore`). | `REQ-PG-021` |
| `CONTENT-INVISIBLE` | red | Bidi controls (U+202A–U+202E, U+2066–U+2069), zero-width characters (U+200B–U+200D, U+2060), U+FEFF outside the first byte, Unicode tag characters (U+E0000–U+E007F). | `REQ-PG-022` |
| `CONTENT-BINARY` | yellow | A binary file is added or changed (`git diff --numstat` reports `-`), including in test directories (cf. xz). Also non-UTF-8 text (see below). | `REQ-PG-023`, `REQ-PG-024` |
| `CONTENT-BLOB` | yellow | A literal of 200+ characters of base64 or 100+ characters of hex on one line. | `REQ-PG-025` |
| `CONTENT-PAGES-SCRIPT` | yellow | On `gh-pages` (or the configured Pages branch): a `<script src>` or `<link>` to a new external host. A host is new if it is absent from the remote's old Pages tree (`REQ-PG-027`). | `REQ-PG-026`, `REQ-PG-027` |

### Scanner configuration

gitleaks can be switched off from inside the repo: it skips lines with a `gitleaks:allow` comment, reads exceptions from a `.gitleaksignore`, and loads a `.gitleaks.toml` in the repo root as its config. An agent could push a secret together with its own exception. So the Push Guard runs the scanner only with configuration from the wall and never from the repo:

- `--config <wall path>` (takes precedence over a `.gitleaks.toml` in the repo),
- `--gitleaks-ignore-path <wall path>` (instead of the repo's `.gitleaksignore`),
- `--ignore-gitleaks-allow` (inline `gitleaks:allow` comments have no effect).

On top of that, adding such an exception is itself `red` (scanner files in `PATH-RED`, `CONTENT-SCANNER-ALLOW`). Real false positives are allowed by a human in the wall config, not in the repo.

**Scanner configuration per repo:** allowlisted paths and exceptions differ between repos, so the scanner config is per repo too, on the same principle as `defaults.yaml`. A shared base config on the wall, plus one file per repo that only adds paths or exceptions and pulls in the base with gitleaks' `[extend] path = "…"`. The repo's `.gitleaksignore` equivalent sits next to it. All of these files live on the wall, never in the repo.

### Commit metadata

| ID | Level | Trigger | Requirements |
| --- | --- | --- | --- |
| `META-UNSIGNED` | yellow | An unsigned commit where the last `lookback` 20 commits on that branch were all signed. Only the presence of a signature counts: the wall has no keyring and verifies no keys. Lookback is first-parent of the remote's old state; quiet if fewer than `lookback` commits (`REQ-PG-029`). | `REQ-PG-028`, `REQ-PG-029` |
| `META-BACKDATED` | yellow | Committer date earlier than a parent's committer date, or more than 24 h before the push. Only the committer date: a rebase or cherry-pick keeps the old author date on purpose. | `REQ-PG-030` |
| `META-FUTURE` | yellow | Author or committer date more than 10 min after the push. | `REQ-PG-031` |

Why dates matter: a backdated commit looks old and long since reviewed in `git log` and on the web, and slots in between older commits; a date in the future keeps it at the top of date-sorted lists.

No author or committer rules: both are plain text the agent sets freely with `git config`, so checking them proves nothing and would only get in the way of rebases and cherry-picks. Which agent pushed is known from its push credential and goes into `push.jsonl`; that is the one identity the agent can't fake.

### Size and volume

All values are configurable per rule (defaults shown).

| ID | Level | Trigger | Requirements |
| --- | --- | --- | --- |
| `SIZE-LIMIT` | red | Delta over the hard limit: `max_files` 2,000 changed files, `max_lines` 50,000 changed lines, `max_bytes` 50 MB of new objects, `max_file_bytes` 10 MB for a single file. | `REQ-PG-032` |
| `SIZE-LARGE` | yellow | Over the soft limit: `max_files` 200, `max_lines` 5,000, `max_commits` 100, `max_file_bytes` 1 MB. | `REQ-PG-033` |
| `SIZE-MASS-DELETE` | yellow | `max_deleted_files` 20 files deleted, or more than `max_deleted_share` 50 % of a file's lines removed in files over `min_lines` 100 lines. Share check: modified/renamed files; deleted files count toward `max_deleted_files` (`REQ-PG-035`). | `REQ-PG-034`, `REQ-PG-035` |
| `RATE-LIMIT` | red | More than `max_pushes` 30 pushes per agent per `window` 1 h (response says "rate limited, try later"). | `REQ-PG-036` |
| `RATE-YELLOW-STREAK` | red | More than `max_yellow` (default 5, configurable) `yellow` rejections for the same agent in the same repo, across all refs, within `window` (default 24 h, configurable for agents that work fast) (counting per ref would let the agent start from zero on a new branch). **A human is warned**, and further pushes by that agent to that repo are treated as `red` (no rule details) until a human approves one of its SHAs there or resets the streak. A `green` push doesn't reset it, otherwise the agent could slip a harmless commit in between. Guards against an agent using the `yellow` messages to rebuild its change until no rule fires. | `REQ-PG-037` |

## Configuration

Many `red` and `yellow` rules have legitimate uses, e.g. an agent cleaning up its own branches or moving snapshot tags. So each rule has, besides its limits and patterns:

- `enabled` (default `true`): a disabled rule never fires (`REQ-PG-038`).
- `color` (`red` or `yellow`, default as in the tables): the owner decides per rule and repo what is a violation for a human and what the agent fixes itself (`REQ-PG-039`).
- `allow`: regular expressions over the rule's **subject**; a match means the rule doesn't fire for it (`REQ-PG-040`).
- `deny`: regular expressions over the subject; a match means the rule fires even if `allow` matches. `deny` wins over `allow` (`REQ-PG-041`).

Patterns always match the **whole** subject (`REQ-PG-042`): the Push Guard wraps each one as `^(?:…)$` itself, because Go's `MatchString` also accepts partial matches. Otherwise `agent.*` would also allow deleting `refs/heads/main-agent-x`.

The subject depends on the rule group (`REQ-PG-043`): the full ref name for `REF-*`, the path for `PATH-*`, `MODE-*`, `CONTENT-*` and `SIZE-*` per file, the ref name for `SIZE-*` totals, the branch for `META-*`. `REF-COUNT` and `RATE-*` have no subject. A rule fires for a subject if `deny` matches, or if it is triggered and `allow` doesn't match. `REF-NAMESPACE` looks at every pushed ref and `PATH-*` at every changed path; every other rule only at the subjects its trigger hits.

```yaml
rules:
  REF-DELETE:
    allow: ['refs/heads/agent.*']          # agents may delete their own agent* branches
  REF-TAG-MOVE:
    allow: ['refs/tags/.*-SNAPSHOT']        # snapshot tags may move
  REF-NAMESPACE:
    deny: ['refs/heads/main', 'refs/heads/release/.*']   # protect branches: every push to them is red
  PATH-RED:
    match: ['scripts/release\.sh']         # this one script is red here, on top of the defaults
  PATH-YELLOW:
    allow: ['package-lock\.json']          # lockfile updates are fine in this repo
  CONTENT-BINARY:
    allow: ['docs/.*\.(png|svg)']          # images for the docs
  MODE-SYMLINK:
    color: red                             # no symlinks at all in this repo
  META-UNSIGNED:
    enabled: false                         # nobody signs here
```

**Protecting branches** works through `deny` on `REF-NAMESPACE`, because that rule looks at every ref name in every push. A `deny` on `REF-DELETE` or `REF-NON-FF` would only stop deleting and force-pushing; a normal fast-forward push wouldn't trigger it.

Configuration lives on the wall, never in the repo itself. Changing it is a human's job. **Each repo can have its own configuration:** shared defaults, plus a file per repo that overrides individual settings (rules, `enabled`, `color`, lists, limits). A setting the repo file doesn't mention keeps the default. **Lists are added to, never replaced:** `match`, `allow` and `deny` in a repo file extend the defaults, so a default protection (e.g. `deny` for `refs/heads/main`) can't vanish unnoticed. Removing a default entry needs an explicit `match_remove` / `deny_remove` / `allow_remove` with the exact pattern (a pattern that isn't there is an error). There are three layers: the built-in defaults of the program, then `defaults.yaml`, then the repo file.

### Repos

The Push Guard knows nothing about owners or platforms. **Each repo gets a short name** chosen by the owner (`[a-z0-9._-]+`, unique within one Push Guard) and **one Git URL** where its real remote lives. The name is all the agent sees: it pushes to `<push guard>/<name>.git`, and the Push Guard forwards to the URL. GitHub, GitLab, Gitea/Forgejo and a bare repo over SSH look the same here; `owner/repo` only appears inside the URL, if at all.

```yaml
# warden/repos/hermetarium/warden.yaml
remote: git@github.com:example/hermetarium.git # any Git URL: ssh://, https://, git@host:path, /srv/git/x.git
credential: /etc/warden/keys/hermetarium        # write credential for exactly this remote (SSH key or token file)
known_hosts: /etc/warden/known_hosts           # optional, SSH only; default: the guard user's own
default_branch: main                            # optional; otherwise read via ls-remote --symref
rules:
  PATH-YELLOW:
    allow: ['package-lock\.json']
```

`remote` and `credential` are required per repo (`credential` may only be left out for a local path or `file://` remote) and never come from `defaults.yaml`, so one repo can't accidentally push to another repo's remote. A push to a name that has no folder is rejected (`red`, "unknown repo"). The credential stays a file reference, never a value in the YAML, so config can be shown and diffed without leaking it. `known_hosts` (SSH only, optional) points to the host keys for this remote; an unknown or changed host key is refused, and `ssh` runs with `-F none`, so no ssh_config on the wall host can change where or how the guard connects.

### Layout on the wall

One file with defaults for all repos, and one folder per repo with only what differs there:

```
warden/
  defaults.yaml          # applies to every repo: agent.name, all rules with their defaults
  gitleaks.toml          # secret scanner rules for every repo
  repos/
    hermetarium/         # folder name = repo name = the path the agent pushes to
      warden.yaml        # remote + credential, plus only what differs from defaults.yaml
      gitleaks.toml      # optional: extra scanner rules; starts with [extend] path = "/etc/warden/gitleaks.toml"
      gitleaksignore     # optional: known false positives of the scanner in this repo
    fregoli/
      warden.yaml        # a repo without its own scanner files uses the shared gitleaks.toml as is
```

For a push to `hermetarium.git`, the Push Guard reads `defaults.yaml`, then applies `repos/hermetarium/warden.yaml` on top; the scanner runs with that folder's `gitleaks.toml` (or the shared one) and `gitleaksignore`. Only `warden.yaml` with `remote` and `credential` is required in a repo folder.

## Statistics

The Push Guard keeps statistics, derived from `push.jsonl`, so a human can improve the configuration. Per rule and repo:

| Count | Meaning |
| --- | --- |
| Hits | How often the rule fired, split into `red` and `yellow`. |
| Overruled | A human approved the SHA anyway (`red`, including the streak). |
| Fixed by the agent | The agent pushed a different SHA afterwards that went through (`yellow`). |

A human never confirms a rejection, they only overrule it by approving the SHA; the approval is logged with the rule IDs. A `red` hit nobody overrules simply stays rejected. A rule that is mostly overruled is a candidate for an `allow` entry, a higher limit, `color: yellow` or `enabled: false`; a `yellow` rule the agent rarely fixes is a candidate for `allow` or `color: red`. A `red` rule that is rarely overruled is doing its job. The statistics only suggest; changing the configuration stays a human's job.

## Not mechanical, so not here

Checking new dependencies against the registry (exists? how old? downloads?, R17) needs network access and a registry; it comes later as its own rule source. Anything that needs judgement (does the commit message match the code? is this a backdoor?) is **not covered** by Git Warden; see [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden).

## Human actions

A human acts only on `red`, and only on the wall host where the configuration lives; being able to run the command there is the authentication. Both actions go into `push.jsonl` with the rule IDs, so the statistics can count them:

- `push-guard approve <repo> <ref> <sha>`: the agent's next push of exactly that SHA to that ref is forwarded without re-checking (and resets the streak).
- `push-guard reset-streak <repo>`: ends a `RATE-YELLOW-STREAK` without approving anything.

The commits of a rejected `red` push are kept as a bundle `pending/<repo>/<id>.bundle` for the human to inspect. Refs can't be written inside the push's quarantine, and its objects vanish once the push is rejected, so a ref namespace like `refs/warden/pending` doesn't work here.

Warnings go out through `notify.command` in `defaults.yaml`: a command that gets the warning as JSON on stdin (facts first, the agent's texts only as a marked quote). Which channel sits behind it (email, ntfy, Signal, …) is the owner's choice and not part of the Push Guard.

## Decided

- **Agent configuration** (`AGENTS.md` & co.) stays in `PATH-RED` by default. A repo where agents maintain it moves the pattern with `PATH-RED: match_remove` plus `PATH-YELLOW: match`.
- **Scanner: gitleaks.** It runs offline, its flags make the repo's own exceptions ineffective (see [Scanner configuration](#scanner-configuration)), and it is MIT-licensed. trufflehog verifies findings against live services, which needs network access during the push; it can come later as a second scanner.
- **Defaults are tuned before the first live run:** the Push Guard gets a `replay` mode (`push-guard replay`) that runs the rules over a repo's existing history without forwarding anything; the hits on the first repos show which defaults are too strict.

## Open questions

None that block the Push Guard. Hosting and the notification channel are open in [SPEC.md](../SPEC.md#open-questions).
