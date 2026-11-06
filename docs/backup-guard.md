# Backup Guard: configuration and operation

The Backup Guard (`backup-guard`) keeps an append-only backup of every branch and tag of each configured remote. It has no rules: it runs [git-everref](https://github.com/daojyun/git-everref) on a timer and never decides anything. Design and decisions: [SPEC.md](../SPEC.md#backup-guard) and [what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled); the risks it covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden). Settings the two guards share: [docs/configuration.md](configuration.md). Which credential to create: [docs/credentials.md](credentials.md). Testable requirements for QA are numbered as `REQ-BG-*` in [docs/backup-guard-spec.md](backup-guard-spec.md#requirements).

## Using it

This walk-through keeps a backup of **this repository**, [github.com/pihme/git-warden](https://github.com/pihme/git-warden). The files are [examples/backup](../examples/backup).

Words used below:

| Word | Meaning |
| --- | --- |
| Backup host | The machine the timer runs on. Not the agents' machine, and not the Push Guard's wall. |
| Bridge | A clone on the backup host. git-everref runs inside it. |
| Backup repository | The append-only Git repository that holds what each run recorded. |
| `notify.command` | An optional setting in `defaults.yaml`: a program that receives a failure as JSON. |
| Warning | That JSON. The Backup Guard sends one only when a run fails. |

### 1. Put a read-only credential on the backup host

On the backup host, create a credential that can read this repository and cannot write to it. Save it as a file there, for example `/etc/warden-backup/keys/git-warden`. Use a different file from the Push Guard's write key.

- For SSH, the file is a deploy key. On GitHub, create it without **Allow write access**.
- For HTTPS, the file holds a token that can read the repository contents.

GitHub and GitLab each have a few ways to do this. The rights for each one are in [Credentials and the rights they need](credentials.md).

The next step writes the configuration. The path of this file is the value of `credential` in `repos/git-warden/backup.yaml`.

### 2. Write the configuration

The Backup Guard reads one configuration directory on the backup host. This walk-through uses `/etc/warden-backup`. That directory holds `defaults.yaml` for the guard, and one folder under `repos/` for each remote. It is a different directory from the Push Guard's.

#### git-everref

git-everref is the program that records the branches and tags. Install v1.0.0 on the backup host (see [Prerequisites](#prerequisites)), then name the binary in `defaults.yaml`:

```yaml
# /etc/warden-backup/defaults.yaml
everref:
  path: /usr/local/bin/git-everref   # the binary you installed
  version: v1.0.0                    # the version you installed
```

#### The backup directory

Add `state_dir` to the same `defaults.yaml`. This is the directory where the backups are written. The first run creates it. Under it, each remote gets a bridge clone and a backup repository.

```yaml
state_dir: /var/lib/warden-backup
```

#### The remote and its credential

Each remote is one folder under `repos/`. The folder name is the name of that backup. This walk-through backs up this repository, so the folder is `repos/git-warden/` and the file inside it is `backup.yaml`:

```yaml
# /etc/warden-backup/repos/git-warden/backup.yaml
remote: git@github.com:pihme/git-warden.git
credential: /etc/warden-backup/keys/git-warden   # path of the read-only key from step 1
known_hosts: /etc/warden-backup/known_hosts      # optional, SSH only
```

`remote` is the Git URL to read. `credential` is the path of the key or token file. The YAML never contains the key itself.

A second remote is another folder next to this one, for example `repos/another-repo/backup.yaml`, with its own URL and its own read-only credential. Add as many folders as you have remotes.

#### A warning when a run fails

`notify.command` is optional. It is a setting in the same `defaults.yaml`. Set it when you want a program to be started each time a remote fails.

1. Choose a program that reads one JSON object from its standard input and delivers it where you will see it (mail, ntfy, or anything else). The guard does not include that program. `/usr/local/bin/warden-notify` below is only a path you might use.
2. Add the program, then its arguments, as a list:

```yaml
notify:
  command: [/usr/local/bin/warden-notify]
```

When a remote fails, the guard starts that program and writes the warning to its standard input. The JSON is under [Warnings](#warnings). A name without a slash is looked up on `PATH`. A path is the program file. Leave `notify` out and no program is started. The failure is still written to `state_dir/backup.jsonl`.

`defaults.yaml` for this walk-through is then:

```yaml
# /etc/warden-backup/defaults.yaml
everref:
  path: /usr/local/bin/git-everref
  version: v1.0.0
state_dir: /var/lib/warden-backup
notify:
  command: [/usr/local/bin/warden-notify]
```

### 3. Check it, then run it from a timer

```bash
backup-guard check-config --config /etc/warden-backup
backup-guard check-config --config /etc/warden-backup --remote   # also contacts GitHub
backup-guard run --config /etc/warden-backup git-warden
```

`git-warden` in that last command is the folder name from step 2, `repos/git-warden`. Leave the name off (`backup-guard run --config /etc/warden-backup`) to back up every remote in the configuration. The [systemd units](../examples/backup/systemd) do that every 15 minutes.

The command that names `git-warden` backs up that one remote:

1. Checks git, git-everref and both files before it creates anything.
2. Prepares `state_dir/repos/git-warden/bridge` and `state_dir/repos/git-warden/backup.git`.
3. Lists the branches on GitHub and starts recording any new one that is not excluded.
4. Runs git-everref, which fetches the remote and records every recorded branch and every tag.

The first run records the remote as it is now, so it fetches the whole history and takes longer than the runs after it. Later runs record only what changed. Git LFS file contents are not fetched. The pointer files in the commits are ordinary Git objects, so those are recorded. The files those pointers name are not.

A successful run sends no warning.

### 4. When a run fails

A failed run starts `notify.command` and writes the warning to its standard input. That warning is the only notice that something was wrong. A run that succeeds starts nothing.

Fix the cause (the credential, the host key, the disk, git-everref, the network) and the next timer run tries again. What GitHub still has is recorded then. A state that existed only between two successful runs was never seen, and is not in the backup.

The warning's JSON is under [Warnings](#warnings). The same facts are one line in `state_dir/backup.jsonl`.

### 5. Look at the backup

In the bridge clone:

```bash
b=/var/lib/warden-backup/repos/git-warden/bridge
git-everref -C "$b" status --all
git-everref -C "$b" log origin/main
```

`git-everref restore` rebuilds refs as of a time, in the bridge, and never overwrites. Copying a recovered state back to GitHub is your own `git push`. The Backup Guard does not do that.

## Prerequisites

- `git` 2.42 or newer.
- `git-everref` (MIT), pinned to `v1.0.0`, on `PATH` or at `everref.path`. Any release with the major version of `everref.version` (default `1.0.0`, so any `v1.x.y`) is accepted. `backup-guard` never downloads it; without it every run fails closed in the preflight. Install it however you provision the host, or with the optional pinned script:

  ```bash
  scripts/install-everref.sh /usr/local/bin   # v1.0.0, SHA-256 checked; default prefix ~/.local/bin
  ```

  The [container image](install.md#docker) and the [Nix flake](install.md#nix) bring the same pinned release along.

- A read-only credential per remote: an SSH deploy key without write access, or a token file with read access only (see [Credentials and the rights they need](credentials.md#credentials-and-the-rights-they-need)). Never reuse the Push Guard's write credential.
- A host that is neither the wall nor the agents' machine.

## Configuration directory

Separate from the Push Guard's configuration (keys differ, and unknown keys are errors):

```
warden-backup/
  defaults.yaml            # optional: everref, notify, state_dir, timeout
  repos/
    git-warden/            # folder name = repo name
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
| `credential` | for SSH and HTTPS | A file path, never the value: an SSH private key for SSH remotes, a file holding a token for HTTPS remotes. Not allowed for a local remote. **Read access is enough and all it should have**: [rights per platform](credentials.md#credentials-and-the-rights-they-need). |
| `credential_username` | no | HTTPS only: the user name sent with the token. Default `x-access-token`. |
| `known_hosts` | no | SSH only: a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well). Unknown or changed host keys are always refused. |
| `exclude_branches` | no | See [Branches and tags](#branches-and-tags). |

The credential is used like this: SSH with `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`, so no ssh_config on the backup host can change where or how the guard connects; HTTPS through an inline credential helper that reads the token file when git asks, so the token never appears in a URL, on a command line or in a log. The same credential environment is passed to git-everref, so its own fetches authenticate the same way.

```yaml
remote: git@github.com:pihme/git-warden.git    # any Git URL or an absolute local path
credential: /etc/warden-backup/keys/git-warden # read-only deploy key, or token file for https://
known_hosts: /etc/warden-backup/known_hosts    # optional, SSH only
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
backup-guard run --config /etc/warden-backup git-warden      # just this one
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
b=/var/lib/warden-backup/repos/git-warden/bridge
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
  "repo": "git-warden",
  "error": "list remote refs: …",
  "everref_exit": -1,
  "output": "tail of everref's output, if it ran"
}
```

`everref_exit` is `-1` when everref's `run` didn't run (preflight, `ls-remote` or setup failed, or the repo was skipped). A failed preflight sends the same warning without `repo`:

```json
{
  "kind": "backup_failed",
  "guard": "backup",
  "time": "2026-10-03T10:31:39Z",
  "error": "preflight: everref not found",
  "everref_exit": -1,
  "preflight": true
}
```

A repository skipped because another run still holds its lock adds `"skipped": true` and names the repository:

```json
{
  "kind": "backup_failed",
  "guard": "backup",
  "time": "2026-10-03T10:31:39Z",
  "repo": "git-warden",
  "error": "skipped: another backup-guard run holds /var/lib/warden-backup/locks/git-warden.lock",
  "everref_exit": -1,
  "skipped": true
}
```
