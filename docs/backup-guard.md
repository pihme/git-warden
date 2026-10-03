# Backup Guard: configuration and operation

The Backup Guard (`backup-guard`) keeps an append-only backup of every branch and tag of each configured remote. It has no rules: it runs [git-everref](https://github.com/daojyun/git-everref) on a timer and never decides anything. Design and decisions: [SPEC.md](../SPEC.md#backup-guard) and [what the implementation settled](../SPEC.md#backup-guard-what-the-implementation-settled); the risks it covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden). The settings both guards share are described the same way in [docs/push-guard.md](push-guard.md) and summarised in the [README](../README.md#configuration-and-operation).

## Prerequisites

- `git` 2.42 or newer.
- `git-everref` (MIT), pinned to `v1.0.0`, on `PATH` or at `everref.path`. `backup-guard` never downloads it; without it every run fails closed in the preflight. Install it however you provision the host, or with the optional pinned script:

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
| `everref.version` | unset | If set (e.g. `v1.0.0`), `git-everref --version` must report exactly this, else the preflight fails. A source build reports `dev`; leave this unset for one. |
| `notify.command` | unset | argv list; gets a warning as JSON on stdin (see [Warnings](#warnings)). |
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

Every run backs up every branch of the remote except those matching `exclude_branches`: regular expressions over the branch name without `refs/heads/`, anchored as `^(?:…)$`, e.g. `['dependabot/.*']`. New branches are protected on the run that first sees them; a protection is never removed, also not when the branch is deleted upstream (everref writes a tombstone) or excluded later. Tags can't be excluded; all tags are always backed up. A remote with no branch to back up is a failure, not an empty backup.

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

From a timer, every 15 minutes: [examples/backup/systemd](../examples/backup/systemd) has a `backup-guard.service` and `backup-guard.timer` (or a cron line calling the same command). Overlapping runs of the same repo wait for each other (one lock per repo).

What one run does:

1. **Preflight** (fails closed, before anything is touched): the configuration loads, `git` 2.42 or newer and `git-everref` are found, `git-everref --version` answers (and matches `everref.version` if set), at least one repo is configured.
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
- `state_dir/backup.jsonl`: one line per repo and run: `time`, `repo`, `ok`, everref version, number of branches and excluded branches, newly protected branches, branches everref refused, counts of `new`, `rewritten`, `deleted`, `retagged` and `regressed` events, everref's exit code, duration, and on failure the error and the tail of everref's output.
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

A failed repo sends `backup_failed` with the repo, the error, everref's exit code and the tail of its output:

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

`everref_exit` is `-1` when everref didn't run (preflight, `ls-remote` or setup failed).
