# Push Guard: requirements

Testable requirements, the decisions they record, and what is still open. Why each rule exists: [push-guard-rules.md](push-guard-rules.md). How to run the guard: [push-guard.md](push-guard.md). Design: [SPEC.md](../SPEC.md#push-guard).

## Requirements

Testable Push Guard behaviours for QA. **Requirement IDs** (`REQ-PG-*`) are stable: never renumber; only append. They are a separate layer from **rule IDs** (`REF-*`, `PATH-*`, …) and from SPEC **risk IDs** (`R1`–`R22`); do not reuse those namespaces.

Each requirement is one shall/must behaviour. Where a rule ID applies, it is named. Settled quirks from [SPEC.md § Push Guard rules](../SPEC.md#push-guard-rules) are included so tests match the implementation.

| ID | Shall / must | Rule / notes |
| --- | --- | --- |
| `REQ-PG-001` | Any `red` finding shall reject the push **without naming the rule**; the response only states rejection and that a human is needed; the owner shall be warned. Named fixed-message exceptions (not silent red): `RATE-LIMIT` (`rate limited, try later`, `REQ-PG-036`) and remote-moved-since-approval (`Remote moved since approval`, `REQ-PG-003`). | Verdict |
| `REQ-PG-002` | Any `yellow` finding shall reject with an agent-usable message (which rule, on which ref/path/line, and why). `yellow` shall **not** go to a human. | Verdict |
| `REQ-PG-003` | Only `green` shall forward **exactly the checked SHAs** to the remote, then accept the agent's push. Forwarding shall never use `--force`. A rewrite, tag move or deletion shall use `--force-with-lease`, and only if the rule's `allow` exempts that ref or a human approved that SHA. Allow-list exemptions lease against the remote state just read. Human-approved rewrites/tag-moves/deletes lease against the **old OID recorded with the approval** (tip at check/approval time). If the remote tip differs at repush, reject with the fixed agent message `Remote moved since approval` (named fixed-message exception, analogous to `REQ-PG-036` / rate-limit — not a silent red / not "waiting for a human"); consume/invalidate the approval; a new check and approval are required. | Verdict |
| `REQ-PG-004` | A rule that errors, times out or cannot read its input shall count as `red` with the message `internal error, try again later` (**fail-closed**). The next push shall be checked from scratch. | Verdict |
| `REQ-PG-005` | Human approval shall be **per SHA**: approve `ref` + `new-oid` (a unique prefix of a rejected push is enough). If the prefix matches more than one distinct rejected SHA of that ref, approve shall refuse and ask for more digits (same SHA rejected twice is not ambiguous). The agent's next push of exactly that SHA to that ref shall be forwarded without re-checking. Any other SHA shall be checked from scratch. | Verdict |
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
| `REQ-PG-026` | `CONTENT-PAGES-SCRIPT` shall fire `yellow` on the Pages branch (`gh-pages` or configured) when a `<script src>` or `<link>` points to a new external host. An empty `pages_branch` disables the rule entirely (no default to `gh-pages`). | `CONTENT-PAGES-SCRIPT` |
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
| `REQ-PG-041` | `deny` shall win over `allow`: when a rule is evaluated for a subject, a `deny` match means the rule fires even if `allow` also matches. Subject-scanning rules (`REF-NAMESPACE`, `PATH-*`) are evaluated for every relevant subject and therefore fire on `deny` alone. Event rules (`REF-DELETE`, `REF-NON-FF`, `REF-TAG-NEW`, `REF-TAG-MOVE`, `MODE-*`, `META-*`, `SIZE-*`, `CONTENT-*`) apply `deny` only when their trigger has already fired; a `deny` match without that trigger does not fire the rule. Once triggered (or subject-scanned), a rule fires if `deny` matches, or if it is triggered and `allow` does not match. | Configuration |
| `REQ-PG-042` | Patterns shall match the **whole** subject: each pattern is wrapped as `^(?:…)$` so partial matches do not exempt or deny unintended subjects. | Configuration |
| `REQ-PG-043` | Subjects shall be: full ref name for `REF-*`; path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*`; ref name for per-ref `SIZE-*` totals; branch (without `refs/heads/` for branches; full ref for tags) for `META-*`. `REF-COUNT` and `RATE-*` have no subject; `allow`/`deny` shall not apply to them. | Configuration |
| `REQ-PG-044` | Path and content rules shall run over the **cumulative diff**; metadata rules shall run over **each new commit**. | What a rule sees |
| `REQ-PG-045` | Remote state shall come from a fresh `git ls-remote --symref` per push; the agent's `old-oid` shall not be trusted. | What a rule sees |
| `REQ-PG-046` | Every limit in [Limit bounds](push-guard-rules.md#limit-bounds) shall be checked against its range when the configuration loads. A value outside the range shall be refused with an error naming the rule, the key and the range (`check-config` exits `1`); it shall never be clamped. | Configuration (2026-10-11) |
| `REQ-PG-047` | `timeout` shall be from `1s` to `1h`. Anything else shall be refused when the configuration loads. | Configuration (2026-10-11) |
| `REQ-PG-048` | A line in `push.jsonl` of up to 64 MiB (counted without its newline) shall be read. A longer line shall make the push fail closed (internal error). | Limits (2026-10-11) |
| `REQ-PG-049` | A line of up to 16 MiB in the diff of a push (counted without the leading `+` or `-` and the newline) shall be read. A longer line shall make the push fail closed (internal error). | Limits (2026-10-11) |
| `REQ-PG-050` | Output of a program the Push Guard starts and evaluates (gitleaks' report and its stdout and stderr, `gitleaks version`, `git version`) shall be read up to 1 MiB per file or stream. More shall make the check fail closed, never be evaluated cut short. | Limits (2026-10-11) |
| `REQ-PG-051` | Output of a program the Push Guard starts only to show or log it (`notify.command`) shall be kept up to its last 1 MiB, stdout and stderr together; the rest shall be dropped and the program shall run on normally. | Limits (2026-10-11) |
| `REQ-PG-052` | Text shall be shortened to these fixed lengths: a SHA in messages to 12 characters, an error text from a program to 300 characters (for `notify.command` its last 300, since the cause is usually at the end; for the others their first 300), a commit subject in `replay` to 72 characters; an alert shall quote at most 20 commits and at most 500 characters per commit message. | Limits (2026-10-11) |
| `REQ-PG-053` | These fixed time limits shall apply: `gitleaks version` and `git version` 30 s each; after a program is killed, at most 5 s more for its output pipes to close; the HTTP server 30 s to read request headers and 30 s to shut down. | Limits (2026-10-11) |

## Decided

- **Agent configuration** (`AGENTS.md` & co.) stays in `PATH-RED` by default. A repo where agents maintain it moves the pattern with `PATH-RED: match_remove` plus `PATH-YELLOW: match`.
- **Scanner: gitleaks.** It runs offline, its flags make the repo's own exceptions ineffective (see [Scanner configuration](push-guard-rules.md#scanner-configuration)), and it is MIT-licensed. trufflehog verifies findings against live services, which needs network access during the push; it can come later as a second scanner.
- **Defaults are tuned before the first live run:** the Push Guard gets a `replay` mode (`push-guard replay`) that runs the rules over a repo's existing history without forwarding anything; the hits on the first repos show which defaults are too strict.

- **Clarified during tests (2026-10-05).** The details and commits are in [Clarifications during tests](push-guard-testspec.md#clarifications-during-tests).
  - An approved rewrite, tag move or deletion leases against the remote tip recorded with the approval. If the remote moved since then, the push is rejected with `Remote moved since approval` and the approval is dropped. The owner gets no warning for this (`REQ-PG-003`).
  - `deny` makes only subject-scanning rules (`REF-NAMESPACE`, `PATH-*`) fire on its own; event rules apply it only when they already fire (`REQ-PG-041`).
  - A SHA prefix that fits more than one distinct rejected SHA approves nothing (`REQ-PG-005`).
  - Limits are checked for the kind the rule reads when the config loads (`REQ-PG-032`). Count and duration limits must not be negative (zero was allowed as the strictest finite bound; since 2026-10-11 the ranges in [Limit bounds](push-guard-rules.md#limit-bounds) decide, and most of them start at 1). A negative `max_age` would make `META-BACKDATED` fire on every commit.
  - Paths with spaces are seen by every content rule (`REQ-PG-021`, `022`, `025`, `026`).
  - A `.gitleaksignore` in the scanned directory makes the scan fail closed (`REQ-PG-020`).
  - An empty `pages_branch` switches `CONTENT-PAGES-SCRIPT` off (`REQ-PG-026`).

- **Limits (Peter, 2026-10-11).** Every configurable limit has a lower and an upper bound and is refused outside it, never clamped ([Limit bounds](push-guard-rules.md#limit-bounds), `REQ-PG-046`, `047`). Fixed internal limits are requirements with their exact value (`REQ-PG-048`–`053`). The maximum diff line drops from 256 MiB to 16 MiB so it can be tested. Output of programs the guard starts is bounded: what is evaluated fails closed above 1 MiB, what is only shown keeps its last 1 MiB. Output of the guard's own `git` calls is not bounded this way; its size follows the repository, and `REQ-PG-049` bounds the diff lines.
- **Retention:** pending bundles of rejected pushes are kept forever for now. Disk space is cheap compared with lost history.

## Open questions

None of these blocks the Push Guard.

- Where the Push Guard and the Backup Guard run (not on the agents' machine either way).
- The notification channel behind `notify.command`.
