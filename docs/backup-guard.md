# Backup Guard: configuration and operation

The Backup Guard (`backup-guard`) keeps an append-only backup of every branch and tag of each configured remote. It has no rules: it runs [git-everref](https://github.com/daojyun/git-everref) on a timer and never decides anything. Design and decisions: [SPEC.md](../SPEC.md#backup-guard) and [what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled); the risks it covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden). The settings both guards share are described the same way in [docs/push-guard.md](push-guard.md) and summarised in the [README](../README.md#configuration-and-operation). Testable requirements for QA are numbered as `REQ-BG-*` in [Requirements](#requirements).

## Prerequisites

- `git` 2.42 or newer.
- `git-everref` (MIT), pinned to `v1.0.0`, on `PATH` or at `everref.path`. Any release with the major version of `everref.version` (default `1.0.0`, so any `v1.x.y`) is accepted. `backup-guard` never downloads it; without it every run fails closed in the preflight. Install it however you provision the host, or with the optional pinned script:

  ```bash
  scripts/install-everref.sh /usr/local/bin   # v1.0.0, SHA-256 checked; default prefix ~/.local/bin
  ```

  The [container image](../README.md#docker) and the [Nix flake](../README.md#nix) bring the same pinned release along.

- A read-only credential per remote: an SSH deploy key without write access, or a token file with read access only (see [Credentials and the rights they need](../README.md#credentials-and-the-rights-they-need)). Never reuse the Push Guard's write credential.
- A host that is neither the wall nor the agents' machine.

## Configuration directory

Separate from the Push Guard's configuration (keys differ, and unknown keys are errors):

```
warden-backup/
  defaults.yaml            # optional: everref, notify, state_dir, timeout
  repos/
    hermetarium/           # folder name = repo name
      backup.yaml          # required: remote (+ credential), plus what differs here
```

See [examples/backup](../examples/backup). Relative file paths in either file are relative to the configuration directory.

**No layers.** Each key has one place: the wall-wide settings in `defaults.yaml`, the per-remote ones in `backup.yaml`. Unknown keys are errors.

### `defaults.yaml`

| Key | Default | Meaning |
| --- | --- | --- |
| `everref.path` | `git-everref` on `PATH` | The git-everref binary: a bare name is looked up on `PATH`, a path is relative to the configuration directory. |
| `everref.version` | `1.0.0` | `[v]MAJOR[.MINOR[.PATCH]]` or `dev`. `git-everref --version` must report the same **major** version (`v1.4.2` satisfies `1.0.0`, `v2.0.0` doesn't), else the preflight fails. A source build reports `dev`, so its version can't be checked: it is refused unless this is set to exactly `dev`, which then accepts only a source build. |
| `notify.command` | unset | argv list; gets a warning as JSON on stdin (see [Warnings](#warnings)). The program is looked up like `everref.path`: a bare name on `PATH`, a path relative to the configuration directory. |
| `state_dir` | `<config>/state` | Bridge clones, backup repositories, `backup.jsonl`, locks. |
| `timeout` | `30m` | Per repo and run; everref is killed when it is exceeded and the run counts as failed. |

### `repos/<name>/backup.yaml`

| Key | Required | Meaning |
| --- | --- | --- |
| `remote` | yes | The remote: `ssh://…` or `user@host:path` (SSH), `https://…` or `http://…` (HTTPS), or a local repository as an absolute path or `file://` URL. |
| `credential` | for SSH and HTTPS | A file path, never the value: an SSH private key for SSH remotes, a file holding a token for HTTPS remotes. Not allowed for a local remote. **Read access is enough and all it should have**: [rights per platform](../README.md#credentials-and-the-rights-they-need). |
| `credential_username` | no | HTTPS only: the user name sent with the token. Default `x-access-token`. |
| `known_hosts` | no | SSH only: a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well). Unknown or changed host keys are always refused. |
| `exclude_branches` | no | See [Branches and tags](#branches-and-tags). |

The credential is used like this: SSH with `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`, so no ssh_config on the backup host can change where or how the guard connects; HTTPS through an inline credential helper that reads the token file when git asks, so the token never appears in a URL, on a command line or in a log. The same credential environment is passed to git-everref, so its own fetches authenticate the same way.

```yaml
remote: git@github.com:example/hermetarium.git  # any Git URL or an absolute local path
credential: /etc/warden-backup/keys/hermetarium # read-only deploy key, or token file for https://
known_hosts: /etc/warden-backup/known_hosts     # optional, SSH only
exclude_branches: ['dependabot/.*']
```

### Branches and tags

Every run backs up every branch of the remote except those matching `exclude_branches`: regular expressions over the branch name without `refs/heads/`, anchored as `^(?:…)$`, e.g. `['dependabot/.*']`. New branches are protected on the run that first sees them; a protection is never removed, also not when the branch is deleted upstream (everref writes a tombstone) or excluded later. `exclude_branches` therefore only keeps branches out that were never protected: **a branch excluded after a run has protected it stays in the backup, and every later run keeps recording it.** To stop backing it up, start a new backup for the repo (move its state directory away). Tags can't be excluded; all tags are always backed up. A remote with no branch to back up is a failure, not an empty backup.

Check it with:

```bash
backup-guard check-config --config /etc/warden-backup            # preflight, files, credentials
backup-guard check-config --config /etc/warden-backup --remote   # plus ls-remote per repo
```

## Running it

```bash
backup-guard run --config /etc/warden-backup                 # every repo
backup-guard run --config /etc/warden-backup hermetarium     # just this one
```

From a timer, every 15 minutes: [examples/backup/systemd](../examples/backup/systemd) has a `backup-guard.service` and `backup-guard.timer` (or a cron line calling the same command). Runs of the same repo never overlap (one lock per repo): if a run still holds a repo's lock, the next run doesn't wait, it skips that repo as failed (exit `1`, a line with `"skipped": true` in `backup.jsonl`, a `backup_failed` warning) and goes on with the others. A repo that is skipped on every run needs a longer timer interval or a shorter `timeout`.

What one run does:

1. **Preflight** (fails closed, before any repo is set up): `defaults.yaml` loads, `git` 2.42 or newer and `git-everref` are found, `git-everref --version` answers with the major version of `everref.version`, at least one repo is configured, and every `repos/<name>/backup.yaml` loads (which also checks the folder name). It reports every problem it finds at once. A failed preflight is recorded like a failed repo: one line with `"preflight": true` in `state_dir/backup.jsonl` and a `backup_failed` warning; no repo runs and no lock, bridge or backup repository is created. Only if `defaults.yaml` itself doesn't load is there nowhere to record it; the run then just exits `1` with the error, as it does when the line can't be written.
2. Per repo: set up or check `state_dir/repos/<name>/bridge` and `state_dir/repos/<name>/backup.git`, and switch on bridged tags once (`git-everref tags --remote backup on --source origin`). A bridge whose `origin` no longer matches `remote` fails the run. Both repositories are set up in a temporary directory and renamed into place only when complete, so a run killed during the setup leaves nothing half-made; the next run removes the leftovers and sets them up again.
3. `git ls-remote` with the repo's credential; every branch not in `exclude_branches` that isn't protected yet is protected with `git-everref add origin/<branch>… --remote backup`.
4. `git-everref -C <bridge> run --all`: everref fetches the remote and records every protected branch and all tags in the backup repo.
5. One JSON line in `state_dir/backup.jsonl`; on failure a `backup_failed` warning to `notify.command`.

**Interrupted runs** (killed, host rebooted, timeout) are picked up by the next run: the setup is atomic (step 2), protections and bridged tags are checked again on every run, and an interrupted `git-everref run` is simply run again; everref only creates and fast-forwards refs in the backup, so nothing it already recorded is lost. The per-repo lock is released by the kernel when its process dies.

## Commands

| Command | What it does |
| --- | --- |
| `backup-guard run --config DIR [repo…]` | Backs up the given repos, or all of them (from a timer). |
| `backup-guard check-config --config DIR [--remote]` | Preflight, files and credentials; `--remote` adds `ls-remote` per repo. |
| `backup-guard version` | Prints the version. |

Exit codes: `0` every repo backed up, `1` a repo failed or the preflight failed, `2` usage error.

## State

- `state_dir/repos/<name>/backup.git`: the backup, a bare repo. Event refs `refs/heads/everref/remotes/origin/<branch>/created_<unix-ts>` (lineages) and `…/deleted_<unix-ts>` (tombstones), the same for tags under `refs/tags/everref/remotes/origin/tags/<tag>/…`, and journals under `refs/heads/everref/journal/…`. Nothing in it is ever deleted or rewritten.
- `state_dir/repos/<name>/bridge`: the clone everref runs in; its git config holds everref's protections (`[everref "remotes/origin/<branch>"]`) and tag setting (`[everref-remote "backup"]`).
- `state_dir/backup.jsonl`: one line per repo and run: `time`, `guard` (`backup`), `repo`, `ok`, everref version, number of branches and excluded branches, newly protected branches, branches everref refused, counts of `new`, `rewritten`, `deleted`, `retagged` and `regressed` events, everref's exit code, duration, and on failure the error, the tail of everref's output, `skipped` for a repo whose lock another run held, and `notify` if `notify.command` failed. A failed preflight writes one line without `repo`, with `"preflight": true`.
- `state_dir/locks/<name>.lock`.

Back the backup repos up offsite (`git bundle create <file> --all`), pack refs regularly (`git -C <backup.git> pack-refs --all`), and don't move or edit the bridge by hand.

## Looking and restoring

With everref's own commands in the bridge clone, e.g.:

```bash
b=/var/lib/warden-backup/repos/hermetarium/bridge
git-everref -C "$b" status --all
git-everref -C "$b" log origin/main
git-everref -C "$b" restore --help      # rebuild refs as of a time; never overwrites
```

Restoring to the remote is always a human's step.

## Warnings

A failed repo sends `backup_failed` with the repo, the error, everref's exit code and the tail of its output (of the failed `tags` or `run` call, otherwise of the `add` calls for branches that couldn't be protected):

```json
{
  "kind": "backup_failed",
  "guard": "backup",
  "time": "2026-10-03T10:31:39Z",
  "repo": "hermetarium",
  "error": "list remote refs: …",
  "everref_exit": -1,
  "output": "tail of everref's output, if it ran"
}
```

`everref_exit` is `-1` when everref's `run` didn't run (preflight, `ls-remote` or setup failed, or the repo was skipped). A failed preflight sends the same warning without `repo` and with `"preflight": true`; a repo skipped because another run held its lock carries `"skipped": true`.

## Requirements

Testable Backup Guard behaviours for QA. **Requirement IDs** (`REQ-BG-*`) are stable: never renumber; only append; never reuse an ID. They are a separate layer from the Push Guard's `REQ-PG-*` ([push-guard-rules.md § Requirements](push-guard-rules.md#requirements)) and from SPEC **risk IDs** (`R1`–`R22`); do not reuse those namespaces.

Each requirement is one shall/must behaviour. Settled points from [SPEC.md § Backup Guard: what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled) are included, so tests match the implementation; where this page leaves a detail open, the requirement states what `backup-guard` does today. Test names follow the Push Guard's: `TestREQ_BG_<nnn>_…`.

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

## Decided

From [SPEC.md § Open questions](../SPEC.md#open-questions) and [what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled); listed here with the requirements they lead to.

- **git-everref is a prerequisite, like gitleaks for the Push Guard.** It is on `PATH` or at `everref.path`, every run fails closed without it, and nothing is downloaded at runtime. The optional install script pins `v1.0.0` with SHA-256 (`REQ-BG-008`–`013`, `REQ-BG-102`–`104`).
- **No backup format of its own, no rules, no AI.** `backup-guard` drives git-everref and adds preflight, branch selection, logging and warnings; missing everref features go upstream ([Gaps to contribute upstream](../SPEC.md#gaps-to-contribute-upstream)) (`REQ-BG-045`, `REQ-BG-070`).
- **Cadence:** every 15 minutes by default, from the host's timer; everref schedules nothing itself (`REQ-BG-071`–`073`).
- **Retention:** the backups are kept forever for now; nothing in a backup repository is deleted or rewritten (`REQ-BG-066`).
- **A backup is never continued with another remote** (`REQ-BG-062`), and **a remote with no branch to back up is a failure** (`REQ-BG-054`).
- **Decided by Peter (2026-10-06)**, on the mismatches between spec and code found when the requirements were numbered:
  - **A failed preflight is recorded and warned** like a failed repo: one `backup.jsonl` line with `"preflight": true` and a `backup_failed` warning. Writing `state_dir` and that line is allowed; no repo is set up. If `defaults.yaml` doesn't load, or the line can't be written, the run still exits `1` with a clear error. `check-config` records nothing (`REQ-BG-001`, `015`, `105`, `106`, `110`).
  - **The preflight loads every `backup.yaml`**, like the Push Guard's, and refuses a repo folder with an invalid name; it reports every problem at once (`REQ-BG-037`, `107`, `108`).
  - **A branch excluded later stays in the backup:** a protection is never removed (`REQ-BG-051`).
  - **`everref.version` defaults to `1.0.0`, and only the major version is compared**, also for a version set by hand. A source build (`dev`) is refused unless `everref.version` is exactly `dev`: the fail-closed choice, since its version can't be checked. The install script stays pinned to `v1.0.0` (`REQ-BG-012`, `013`, `112`, `113`).
  - **A repo whose lock another run holds is skipped, not waited for.** It counts as failed (exit `1`), with a `"skipped": true` line in `backup.jsonl` and a warning; the other repos still run (`REQ-BG-075`, `109`).
  - Bug fixes so the code does what this page already said: a relative `notify.command` is relative to the configuration directory (`REQ-BG-111`), and the warning for branches that couldn't be protected carries everref's output (`REQ-BG-081`).
