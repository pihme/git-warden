# Pull Guard: configuration and operation

The Pull Guard (`pull-guard`) keeps an append-only backup of every branch and tag of each configured remote. It has no rules: it runs [git-everref](https://github.com/daojyun/git-everref) on a timer and never decides anything. Design and decisions: [SPEC.md](../SPEC.md#pull-guard) and [what the implementation settled](../SPEC.md#pull-guard-what-the-implementation-settled); the risks it covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden).

## Prerequisites

- `git` 2.42 or newer.
- `git-everref` (MIT), pinned to `v1.0.0`, on `PATH` or at `everref.path`. `pull-guard` never downloads it; without it every run fails closed in the preflight. Install it however you provision the host, or with the optional pinned script:

  ```bash
  scripts/install-everref.sh /usr/local/bin   # v1.0.0, SHA-256 checked; default prefix ~/.local/bin
  ```

- A read-only credential per remote: an SSH deploy key without write access, or a token file with read access only. Never reuse the Push Guard's write credential.
- A host that is neither the wall nor the agents' machine.

## Configuration directory

Separate from the Push Guard's configuration (keys differ, and unknown keys are errors):

```
warden-pull/
  defaults.yaml            # optional: everref, notify, state_dir, timeout
  repos/
    hermetarium/           # folder name = repo name
      pull.yaml            # required: remote (+ credential)
```

See [examples/pull](../examples/pull).

### `defaults.yaml`

| Key | Default | Meaning |
| --- | --- | --- |
| `everref.path` | `git-everref` on `PATH` | The everref binary; a relative path is resolved against the configuration directory. |
| `everref.version` | unset | If set (e.g. `v1.0.0`), `git-everref --version` must report exactly this, else the preflight fails. A source build reports `dev`; leave this unset for one. |
| `notify.command` | unset | argv list; gets a `pull_failed` warning as JSON on stdin when a repo's run fails. |
| `state_dir` | `<config>/state` | Bridge clones, backup repos, locks, `pull.jsonl`. |
| `timeout` | `30m` | Per repo and run; everref is killed when it is exceeded and the run counts as failed. |

### `repos/<name>/pull.yaml`

| Key | Required | Meaning |
| --- | --- | --- |
| `remote` | yes | Any Git URL: `ssh://`, `user@host:path`, `https://`, or an absolute local path / `file://` URL. |
| `credential` | for SSH and HTTPS | Path to a read-only SSH private key, or to a file holding a read-only token. Never the value itself. None for local remotes. |
| `known_hosts` | no | SSH only: a known_hosts file for this remote instead of the user's own. Unknown or changed host keys are always refused. |
| `exclude_branches` | no | Regular expressions over the branch name without `refs/heads/`, anchored as `^(?:…)$`, e.g. `['dependabot/.*']`. Excluded branches are not backed up. Tags can't be excluded; all tags are always backed up. |

Check it with:

```bash
pull-guard check-config --config /etc/warden-pull            # preflight, files, credentials
pull-guard check-config --config /etc/warden-pull --remote   # plus ls-remote per repo
```

## Running it

```bash
pull-guard run --config /etc/warden-pull                 # every repo
pull-guard run --config /etc/warden-pull hermetarium     # just this one
```

From a timer, every 15 minutes: [examples/pull/systemd](../examples/pull/systemd) has a `pull-guard.service` and `pull-guard.timer` (or a cron line calling the same command). Overlapping runs of the same repo wait for each other (one lock per repo).

What one run does:

1. **Preflight** (fails closed, before anything is touched): the configuration loads, `git` and `git-everref` are found, `git-everref --version` answers (and matches `everref.version` if set), at least one repo is configured.
2. Per repo: set up or check `state_dir/repos/<name>/bridge` and `state_dir/repos/<name>/backup.git`, and switch on bridged tags once (`git-everref tags --remote backup on --source origin`). A bridge whose `origin` no longer matches `remote` fails the run.
3. `git ls-remote` with the repo's credential; every branch not in `exclude_branches` that isn't protected yet is protected with `git-everref add origin/<branch>… --remote backup`.
4. `git-everref -C <bridge> run --all`: everref fetches the remote and records every protected branch and all tags in the backup repo.
5. One JSON line in `state_dir/pull.jsonl`; on failure a `pull_failed` warning to `notify.command`.

Exit codes: `0` every repo backed up, `1` a repo failed or the preflight failed, `2` usage error.

## State

- `state_dir/repos/<name>/backup.git`: the backup, a bare repo. Event refs `refs/heads/everref/remotes/origin/<branch>/created_<unix-ts>` (lineages) and `…/deleted_<unix-ts>` (tombstones), the same for tags under `refs/tags/everref/remotes/origin/tags/<tag>/…`, and journals under `refs/heads/everref/journal/…`. Nothing in it is ever deleted or rewritten.
- `state_dir/repos/<name>/bridge`: the clone everref runs in; its git config holds everref's protections (`[everref "remotes/origin/<branch>"]`) and tag setting (`[everref-remote "backup"]`).
- `state_dir/pull.jsonl`: one line per repo and run: `time`, `repo`, `ok`, everref version, number of branches and excluded branches, newly protected branches, branches everref refused, counts of `new`, `rewritten`, `deleted`, `retagged` and `regressed` events, everref's exit code, duration, and on failure the error and the tail of everref's output.
- `state_dir/locks/<name>.lock`.

Back the backup repos up offsite (`git bundle create <file> --all`), pack refs regularly (`git -C <backup.git> pack-refs --all`), and don't move or edit the bridge by hand.

## Looking and restoring

With everref's own commands in the bridge clone, e.g.:

```bash
b=/var/lib/warden-pull/repos/hermetarium/bridge
git-everref -C "$b" status --all
git-everref -C "$b" log origin/main
git-everref -C "$b" restore --help      # rebuild refs as of a time; never overwrites
```

Restoring to the remote is always a human's step.

## Warning

```json
{
  "kind": "pull_failed",
  "guard": "pull",
  "time": "2026-10-03T10:31:39Z",
  "repo": "hermetarium",
  "error": "list remote refs: …",
  "everref_exit": -1,
  "output": "tail of everref's output, if it ran"
}
```

`everref_exit` is `-1` when everref didn't run (preflight, `ls-remote` or setup failed).
