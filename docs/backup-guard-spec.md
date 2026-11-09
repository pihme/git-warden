# Backup Guard: requirements

Testable requirements, the decisions they record, and what is still open. How to run the guard: [backup-guard.md](backup-guard.md). Design: [SPEC.md](../SPEC.md#backup-guard).

## Requirements

Testable Backup Guard behaviours for QA. **Requirement IDs** (`REQ-BG-*`) are stable: never renumber; only append; never reuse an ID. They are a separate layer from the Push Guard's `REQ-PG-*` ([push-guard-spec.md § Requirements](push-guard-spec.md#requirements)) and from SPEC **risk IDs** (`R1`–`R22`); do not reuse those namespaces.

Each requirement is one shall/must behaviour. Settled points from [SPEC.md § Backup Guard: what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled) are included, so tests match the implementation; where [the operation manual](backup-guard.md) leaves a detail open, the requirement states what `backup-guard` does today. Test names follow the Push Guard's: `TestREQ_BG_<nnn>_…`.

| ID | Shall / must | Area / notes |
| --- | --- | --- |
| `REQ-BG-001` | `backup-guard run` shall run a preflight before it sets up anything for a repo. While the preflight fails, no lock, bridge or backup repository shall be created; the only thing written is the failure record in `state_dir/backup.jsonl` (`REQ-BG-015`). | Preflight (amended 2026-10-06) |
| `REQ-BG-002` | A failed preflight shall end `backup-guard run` with exit code `1` before any repo runs. | Preflight |
| `REQ-BG-003` | The preflight shall fail if the configuration directory does not exist or is not a directory. | Preflight |
| `REQ-BG-004` | The preflight shall fail if `defaults.yaml` exists but does not load (`REQ-BG-017`, `REQ-BG-019`, `REQ-BG-021`, `REQ-BG-022`). | Preflight |
| `REQ-BG-005` | The preflight shall fail if `git` is not on `PATH`. | Prerequisites |
| `REQ-BG-006` | The preflight shall fail if `git version` reports a version older than 2.42. Version 2.42 and newer shall pass. | Prerequisites |
| `REQ-BG-007` | The preflight shall fail if the output of `git version` can't be parsed as `git version <major>.<minor>…`. | Prerequisites |
| `REQ-BG-008` | git-everref shall be looked up at `everref.path` if it is set, otherwise as `git-everref` on `PATH`. | Prerequisites |
| `REQ-BG-009` | The preflight shall fail if git-everref is not found. The error shall point to `scripts/install-everref.sh` and `everref.path`. | Prerequisites |
| `REQ-BG-010` | The preflight shall fail if `git-everref --version` exits non-zero or doesn't answer within 30 s. | Prerequisites |
| `REQ-BG-011` | The preflight shall fail if the first line of `git-everref --version` doesn't have the form `<name> version <version>`. The version is the last field of that line. | Prerequisites |
| `REQ-BG-012` | The preflight shall fail unless the major version git-everref reports equals the major version of `everref.version`. Minor and patch versions shall not be compared (`v1.4.2` satisfies `1.0.0`). | Prerequisites (amended 2026-10-06) |
| `REQ-BG-013` | If `everref.version` is not set, it shall default to `1.0.0`, so git-everref must report major version 1. | Prerequisites (amended 2026-10-06) |
| `REQ-BG-014` | The preflight shall fail if no repo is configured (no `repos/<name>/backup.yaml`). | Preflight |
| `REQ-BG-015` | A failed preflight of `backup-guard run` shall append one line to `state_dir/backup.jsonl` with `ok` false, `preflight` true, no `repo`, `everref_exit` `-1` and the error, and shall send a `backup_failed` warning with `preflight` true to `notify.command` if it is set. | Preflight (amended 2026-10-06) |
| `REQ-BG-016` | `defaults.yaml` shall be optional. Without it, or for a key it leaves out, the built-in default shall apply: `everref.path` `git-everref` on `PATH`, `everref.version` `1.0.0`, `notify.command` unset, `state_dir` `<config>/state`, `timeout` `30m`. | Configuration (amended 2026-10-06) |
| `REQ-BG-017` | `defaults.yaml` shall accept only `everref.path`, `everref.version`, `notify.command`, `state_dir` and `timeout`. Any other key (a per-repo key such as `remote`, a Push Guard key such as `gitleaks.path`, a typo) shall be an error. | Configuration (no layers) |
| `REQ-BG-018` | An `everref.path` without a `/` shall be looked up on `PATH`. One with a `/` shall be used as is if it is absolute, otherwise relative to the configuration directory. | Configuration |
| `REQ-BG-019` | An empty `everref.path` shall be an error. | Configuration |
| `REQ-BG-020` | A relative `state_dir` shall be relative to the configuration directory. | Configuration |
| `REQ-BG-021` | An empty `state_dir` shall be an error. | Configuration |
| `REQ-BG-022` | `timeout` shall be a Go duration greater than zero (e.g. `30m`). Anything else shall be an error. | Configuration |
| `REQ-BG-023` | A repo shall be a directory `repos/<name>/` that contains `backup.yaml`. A directory under `repos/` without `backup.yaml` shall not count as a repo. | Repo configuration |
| `REQ-BG-024` | A repo name shall match `[a-z0-9._-]+` and shall not start with `.`. Loading a repo with any other name shall fail. | Repo configuration |
| `REQ-BG-025` | `backup.yaml` shall accept only `remote`, `credential`, `credential_username`, `known_hosts` and `exclude_branches`. Any other key (a wall-wide key such as `state_dir`, a Push Guard key such as `rules`, a typo) shall be an error. | Repo configuration (no layers) |
| `REQ-BG-026` | `remote` shall be required. | Repo configuration |
| `REQ-BG-027` | A remote shall be classified as SSH (`ssh://`, `git+ssh://`, `ssh+git://` or `[user@]host:path`), HTTPS (`https://`, `http://`) or local (an absolute path or a `file://` URL). Any other `<scheme>://` shall be an error. | Repo configuration |
| `REQ-BG-028` | A local remote given as a relative path shall be refused. | Repo configuration |
| `REQ-BG-029` | An SSH or HTTPS remote without `credential` shall be refused. | Repo configuration |
| `REQ-BG-030` | A local remote with `credential` shall be refused. | Repo configuration |
| `REQ-BG-031` | `credential_username` shall be refused for a remote that isn't HTTPS. | Repo configuration |
| `REQ-BG-032` | `credential_username` shall be refused unless it is 1 to 128 characters of letters, digits and `. _ + @ -`. | Repo configuration |
| `REQ-BG-033` | `known_hosts` shall be refused for a remote that isn't SSH. | Repo configuration |
| `REQ-BG-034` | Relative `credential` and `known_hosts` paths shall be relative to the configuration directory. | Repo configuration |
| `REQ-BG-035` | Each `exclude_branches` entry shall be a regular expression anchored as `^(?:…)$` and matched against the branch name without `refs/heads/`. | Branches and tags |
| `REQ-BG-036` | An `exclude_branches` entry that isn't a valid regular expression shall be an error. | Repo configuration |
| `REQ-BG-037` | A `backup.yaml` that doesn't load shall fail the preflight (exit `1`), and no repo shall run. | Preflight (amended 2026-10-06) |
| `REQ-BG-038` | For an SSH remote, every git and git-everref call for the repo shall use `ssh -F none -i <credential> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`. | Credentials |
| `REQ-BG-039` | With `known_hosts` set, ssh shall use only that file (`UserKnownHostsFile=<file>`, `GlobalKnownHostsFile=/dev/null`). Without it, ssh shall use the guard user's own known_hosts. | Credentials |
| `REQ-BG-040` | For an HTTPS remote, an inline credential helper shall read the token from the `credential` file when git asks and send it with the user name `credential_username` (default `x-access-token`). Credential helpers configured elsewhere shall not be used. | Credentials |
| `REQ-BG-041` | The token shall never appear in the remote URL, on a command line, in the bridge's or the backup's git config, in `backup.jsonl`, or in an error or warning. | Credentials |
| `REQ-BG-042` | git-everref shall get the same credential environment as the guard's own git calls, so its fetches authenticate with the same key or token. | Credentials |
| `REQ-BG-043` | Every run shall list the remote's refs with `git ls-remote`, using the repo's credential. If that fails, the repo's run shall fail. | Branches and tags |
| `REQ-BG-044` | Only `refs/heads/*` shall count as branches. Other refs on the remote (e.g. `refs/pull/*`, `refs/notes/*`) shall not be protected. | Branches and tags |
| `REQ-BG-045` | Every branch that matches no `exclude_branches` entry and isn't protected yet shall be protected with `git-everref add origin/<branch>… --remote backup` on the run that first sees it. | Branches and tags |
| `REQ-BG-046` | One `git-everref add` call shall name at most 100 branches. | Branches and tags |
| `REQ-BG-047` | If an `add` call fails, each of its branches shall be retried in an `add` call of its own. | Branches and tags |
| `REQ-BG-048` | A branch that still can't be protected shall make the repo's run fail and shall be listed in `add_failed` with everref's reason. | Branches and tags |
| `REQ-BG-049` | A branch that can't be protected shall not stop the others: the remaining branches shall still be protected, and `git-everref run --all` shall still run. | Branches and tags |
| `REQ-BG-050` | A protection shall not be removed when its branch is deleted upstream (everref needs it to write the tombstone). | Branches and tags |
| `REQ-BG-051` | A protection shall not be removed when its branch is matched by `exclude_branches` later. The branch stays protected and keeps being recorded by `run --all`. | Branches and tags |
| `REQ-BG-052` | Tags shall not be excludable: `exclude_branches` shall not apply to tags, and all tags of the remote shall be recorded. | Branches and tags |
| `REQ-BG-053` | Bridged tags shall be switched on with `git-everref tags --remote backup on --source origin` whenever the bridge's `everref-remote.backup.tags` doesn't contain `origin`. This shall be checked on every run, and the command shall not run again once the setting is there. | Branches and tags |
| `REQ-BG-054` | A remote with no branch outside `exclude_branches` shall make the repo's run fail. It shall not count as an empty backup. | Branches and tags |
| `REQ-BG-055` | A branch rewritten upstream (force push) shall get a new lineage `refs/heads/everref/remotes/origin/<branch>/created_<unix-ts>`, and its old tip shall stay reachable from the earlier lineage. | Branches and tags |
| `REQ-BG-056` | A branch deleted upstream shall get a tombstone `refs/heads/everref/remotes/origin/<branch>/deleted_<unix-ts>`, and its earlier lineage shall stay. | Branches and tags |
| `REQ-BG-057` | A tag moved upstream shall get a new lineage under `refs/tags/everref/remotes/origin/tags/<tag>/`, and its old target shall stay reachable. | Branches and tags |
| `REQ-BG-058` | An unreachable remote shall make the repo's run fail and shall leave the backup repository unchanged (an error, never a deletion). | Branches and tags |
| `REQ-BG-059` | The bridge shall be a non-bare repository at `state_dir/repos/<name>/bridge`, the backup a bare repository at `state_dir/repos/<name>/backup.git`. | Setup and state |
| `REQ-BG-060` | The bridge's remote `origin` shall be the configured `remote`. Its remote `backup` shall be the backup repository, with no fetch refspec. | Setup and state |
| `REQ-BG-061` | The bridge shall have the local committer identity `git-warden backup-guard` (`backup-guard@localhost`) and commit signing switched off, for everref's journal commits. | Setup and state |
| `REQ-BG-062` | If the bridge's `origin` URL differs from the configured `remote`, the repo's run shall fail. A backup shall never be continued with another remote. | Setup and state |
| `REQ-BG-063` | The backup repository and the bridge (with both remotes) shall each be built in a temporary directory `.setup-<name>-*` next to them and renamed into place only when complete. | Setup and state |
| `REQ-BG-064` | At the start of a repo's run, under its lock, leftover `.setup-*` directories shall be removed. | Setup and state |
| `REQ-BG-065` | An existing bridge or backup directory that isn't a finished repository shall be replaced if it is empty. If it isn't empty, the repo's run shall fail and the directory shall stay as it is. | Setup and state |
| `REQ-BG-066` | A run shall never delete a ref in the backup repository or move one to a commit that doesn't contain its previous commit: refs are only created and fast-forwarded. | Setup and state |
| `REQ-BG-067` | `backup-guard run --config DIR` without repo names shall back up every configured repo. With repo names it shall back up only those. | Run |
| `REQ-BG-068` | A repo name given to `run` that has no `repos/<name>/backup.yaml` shall end the command with exit code `1` and an `unknown repo` error before any repo runs. | Run |
| `REQ-BG-069` | Repos shall run one after the other. A failed repo shall not stop the others. | Run |
| `REQ-BG-070` | For each repo, the run shall call `git-everref -C <bridge> run --all`. | Run |
| `REQ-BG-071` | `backup-guard run` shall make one pass over the repos and exit. Scheduling is left to the host's timer or cron. | Trigger |
| `REQ-BG-072` | `backup-guard` shall never pass `--trigger` or `--schedule` to git-everref, so everref installs no hooks or timer units of its own. | Trigger |
| `REQ-BG-073` | The example timer `examples/backup/systemd/backup-guard.timer` shall start `backup-guard run` every 15 minutes. | Trigger |
| `REQ-BG-074` | `timeout` shall bound each repo's run (setup, `ls-remote`, `add` and `run`). When it is exceeded, git-everref shall be killed and the repo's run shall fail. | Run |
| `REQ-BG-075` | Each repo's run shall take an exclusive lock on `state_dir/locks/<name>.lock` without waiting. If another run holds it, the repo shall be skipped and count as failed (exit `1`); the other repos still run. | Locking (amended 2026-10-06) |
| `REQ-BG-076` | The lock shall be released when the process holding it ends, also when it is killed. | Locking |
| `REQ-BG-077` | A run killed at any step of the setup, or during `git-everref add` or `run`, shall be resumed by the next run: that run shall complete the backup without a manual step, lose no recorded ref, and leave nothing in `state_dir/repos/<name>/` besides `bridge` and `backup.git`. | Interrupted runs |
| `REQ-BG-078` | Every repo's run after a passed preflight shall append exactly one JSON line to `state_dir/backup.jsonl`, also when it fails. | State |
| `REQ-BG-079` | The line shall carry `time` (UTC), `guard` (`backup`), `repo`, `ok`, `everref` (version), `branches` (after `exclude_branches`), `excluded`, `added`, `add_failed`, `new`, `rewritten`, `deleted`, `retagged`, `regressed`, `everref_exit` (`-1` if `run` didn't run), `duration_ms`, and, where they apply, `error`, `output`, `skipped` and `notify`. The line of a failed preflight has no `repo` and carries `preflight` true. | State (amended 2026-10-06) |
| `REQ-BG-080` | The event counts shall come from the output of `git-everref run`: `[new]` counts as `new`, `[new lineage]` as `rewritten`, `[deleted]` as `deleted`, `[re-tagged]` as `retagged`, `[regressed]` as `regressed`. `[deleted; already recorded]` and `[fast-forward]` shall not be counted. | State |
| `REQ-BG-081` | When a repo's run fails because a git-everref call failed, `output` shall hold the tail of everref's output (the last 40 lines, at most 4000 bytes): of the failed `tags` or `run` call, otherwise of the `add` calls that still failed for single branches. | State (amended 2026-10-06) |
| `REQ-BG-082` | If the line can't be appended to `backup.jsonl`, the repo's run shall count as failed. | State |
| `REQ-BG-083` | A repo whose run fails shall send one `backup_failed` warning to `notify.command`, as JSON on stdin with `kind` (`backup_failed`), `guard` (`backup`), `time`, `repo`, `error`, `everref_exit` and, where they apply, `output` and `skipped`. | Warnings |
| `REQ-BG-084` | A repo whose run succeeds shall send no warning. | Warnings |
| `REQ-BG-085` | With `notify.command` unset or empty, no warning shall be sent, and the run's result shall not change. | Warnings |
| `REQ-BG-086` | If `notify.command` fails or doesn't finish within 1 minute, its error shall be recorded in the run's `notify` field. | Warnings |
| `REQ-BG-087` | `backup-guard version` (also `--version` and `-v`) shall print `backup-guard <version>` and exit `0`. | Commands |
| `REQ-BG-088` | `run` and `check-config` shall require `--config DIR`. Flags may come before, between or after repo names. | Commands |
| `REQ-BG-089` | Exit code `0` shall mean every repo was backed up (`run`) or no problem was found (`check-config`). | Commands |
| `REQ-BG-090` | Exit code `1` shall mean a repo failed, the preflight failed, or `check-config` found a problem. | Commands |
| `REQ-BG-091` | Exit code `2` shall mean a usage error: no command, an unknown command, a missing `--config`, an unknown flag, or positional arguments to `check-config`. | Commands |
| `REQ-BG-092` | `check-config` shall run the same preflight as `run` and exit `1` if it fails. | check-config |
| `REQ-BG-093` | `check-config` shall back nothing up and create no state. | check-config |
| `REQ-BG-094` | `check-config` shall load every repo's `backup.yaml` and report each one that doesn't load as a problem. | check-config |
| `REQ-BG-095` | `check-config` shall report a `credential` or `known_hosts` file that doesn't exist as a problem. | check-config |
| `REQ-BG-096` | `check-config` shall report a `notify.command` whose program isn't found as a problem. | check-config |
| `REQ-BG-097` | `check-config` shall only note, not count as a problem: `everref.version` not set (the default `1.0.0` applies), `notify.command` unset, a credential readable by group or others. | check-config (amended 2026-10-06) |
| `REQ-BG-098` | `check-config --remote` shall also run `ls-remote` per repo, with its credential and `timeout`. A failure shall be a problem. | check-config |
| `REQ-BG-099` | When the preflight passes, `check-config` shall list every problem it found and exit `1`, or print `configuration ok` and exit `0` if there is none. | check-config |
| `REQ-BG-100` | After a run, git-everref's own commands (`status`, `log`, `restore`) shall work in `state_dir/repos/<name>/bridge`. | Looking and restoring |
| `REQ-BG-101` | `backup-guard` shall never write to the configured remote: no command and no run pushes to it, so restoring to the remote stays a human's step. | Looking and restoring |
| `REQ-BG-102` | `backup-guard` shall never download, update or build git-everref, or any other program, at runtime. | No runtime downloads |
| `REQ-BG-103` | `scripts/install-everref.sh` shall install git-everref `v1.0.0` only if the release tarball matches the SHA-256 pinned in the script. On a mismatch it shall install nothing. | Provisioning |
| `REQ-BG-104` | `scripts/install-everref.sh` shall install nothing if the unpacked binary's `--version` isn't `everref version v1.0.0`. | Provisioning |
| `REQ-BG-105` | If the configuration directory or `defaults.yaml` doesn't load, the failed preflight shall only be reported as an error with exit `1`; nothing shall be recorded or warned (there is no known `state_dir` or `notify.command`). | Preflight (2026-10-06) |
| `REQ-BG-106` | If the failure record of a failed preflight can't be written (`state_dir` can't be created or `backup.jsonl` can't be appended to), the run shall still exit `1`, and the error shall name both the preflight failure and why it couldn't be recorded. | Preflight (2026-10-06) |
| `REQ-BG-107` | A folder under `repos/` that contains `backup.yaml` but whose name isn't a valid repo name (`REQ-BG-024`) shall fail the preflight. | Preflight (2026-10-06) |
| `REQ-BG-108` | Once `defaults.yaml` has loaded, the preflight shall report every problem it finds (git, git-everref, repos) in one error. | Preflight (2026-10-06) |
| `REQ-BG-109` | A repo skipped because another run holds its lock shall get a `backup.jsonl` line with `skipped` true and the error, and a `backup_failed` warning with `skipped` true. | Locking (2026-10-06) |
| `REQ-BG-110` | `check-config` shall not record a failed preflight: it shall send no warning and write no `backup.jsonl` line. | check-config (2026-10-06) |
| `REQ-BG-111` | A `notify.command` program without a `/` shall be looked up on `PATH`. One with a `/` shall be used as is if it is absolute, otherwise relative to the configuration directory. An empty program name shall be an error. | Warnings (2026-10-06) |
| `REQ-BG-112` | A git-everref that reports `dev` (a source build) shall fail the preflight unless `everref.version` is exactly `dev`. With `everref.version: dev`, only a git-everref that reports `dev` shall pass. | Prerequisites (2026-10-06) |
| `REQ-BG-113` | `everref.version` shall be `dev` or a version `[v]MAJOR[.MINOR[.PATCH]]`. Anything else, including an empty value, shall be an error. | Configuration (2026-10-06) |
| `REQ-BG-114` | `timeout` shall be from `1m` to `24h`. Anything else shall be an error (amends `REQ-BG-022`). | Configuration (2026-10-11) |
| `REQ-BG-115` | The event counts of `git-everref run` (`REQ-BG-080`) shall be taken while its output is read, so they stay right for any output size. Of that output only the last 1 MiB shall be kept for `output` (`REQ-BG-081`). | Limits (2026-10-11) |
| `REQ-BG-116` | The output of `git-everref --version` shall be read up to 1 MiB. More shall fail the preflight. | Limits (2026-10-11) |
| `REQ-BG-117` | Of the output of `git-everref add` and of `notify.command`, only the last 1 MiB, stdout and stderr together, shall be kept; the rest shall be dropped and the program shall run on normally. | Limits (2026-10-11) |
| `REQ-BG-118` | These fixed limits shall apply: an error text from a program shortened to 300 characters (for `notify.command` its last 300, for the others their first 300); after a program is killed, at most 5 s more for its output pipes to close. (The 40 lines and 4000 bytes of `output`, 100 branches per `add`, 30 s for `--version` and 1 minute for `notify.command` are `REQ-BG-081`, `046`, `010` and `086`.) | Limits (2026-10-11) |

## Decided

From [what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled); listed here with the requirements they lead to.

- **git-everref is a prerequisite, like gitleaks for the Push Guard.** It is on `PATH` or at `everref.path`, every run fails closed without it, and nothing is downloaded at runtime. The optional install script pins `v1.0.0` with SHA-256 (`REQ-BG-008`–`013`, `REQ-BG-102`–`104`).
- **No backup format of its own, no rules, no AI.** `backup-guard` drives git-everref and adds preflight, branch selection, logging and warnings; missing everref features go upstream ([Gaps to contribute upstream](../SPEC.md#gaps-to-contribute-upstream)) (`REQ-BG-045`, `REQ-BG-070`).
- **Cadence:** every 15 minutes by default, from the host's timer; everref schedules nothing itself (`REQ-BG-071`–`073`).
- **Retention:** the backups are kept forever for now, and so are offsite bundles of them; nothing in a backup repository is deleted or rewritten (`REQ-BG-066`). Disk space is cheap compared with lost history.
- **A backup is never continued with another remote** (`REQ-BG-062`), and **a remote with no branch to back up is a failure** (`REQ-BG-054`).
- **Limits (owner, 2026-10-11).** `timeout` has an upper bound (24h) besides being positive (`REQ-BG-114`). Output of git-everref and `notify.command` is bounded: event counts are taken while reading, what is only shown keeps its last 1 MiB, and an oversized `--version` answer fails the preflight (`REQ-BG-115`–`117`). Fixed internal limits are requirements with their exact value (`REQ-BG-118`).
- **Decided by the owner (2026-10-06)**, on the mismatches between spec and code found when the requirements were numbered:
  - **A failed preflight is recorded and warned** like a failed repo: one `backup.jsonl` line with `"preflight": true` and a `backup_failed` warning. Writing `state_dir` and that line is allowed; no repo is set up. If `defaults.yaml` doesn't load, or the line can't be written, the run still exits `1` with a clear error. `check-config` records nothing (`REQ-BG-001`, `015`, `105`, `106`, `110`).
  - **The preflight loads every `backup.yaml`**, like the Push Guard's, and refuses a repo folder with an invalid name; it reports every problem at once (`REQ-BG-037`, `107`, `108`).
  - **A branch excluded later stays in the backup:** a protection is never removed (`REQ-BG-051`).
  - **`everref.version` defaults to `1.0.0`, and only the major version is compared**, also for a version set by hand. A source build (`dev`) is refused unless `everref.version` is exactly `dev`: the fail-closed choice, since its version can't be checked. The install script stays pinned to `v1.0.0` (`REQ-BG-012`, `013`, `112`, `113`).
  - **A repo whose lock another run holds is skipped, not waited for.** It counts as failed (exit `1`), with a `"skipped": true` line in `backup.jsonl` and a warning; the other repos still run (`REQ-BG-075`, `109`).
  - Bug fixes so the code does what [the operation manual](backup-guard.md) already said: a relative `notify.command` is relative to the configuration directory (`REQ-BG-111`), and the warning for branches that couldn't be protected carries everref's output (`REQ-BG-081`).

## Open questions

None of these blocks the Backup Guard.

- Where the Push Guard and the Backup Guard run (not on the agents' machine either way).
- The notification channel behind `notify.command`.
- The offsite target with write-once retention for the bundles.
- Whether git-everref takes the upstream gaps; until then very large repos stay out of the Backup Guard.
