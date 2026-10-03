# Git Warden

Git Warden puts guard posts between AI agents and their Git remote, whether that is GitHub, GitLab, Gitea/Forgejo or a bare repo over SSH. This monorepo will hold all parts; the first one is the **Push Guard**: agents push to it instead of the remote, it checks every push with deterministic rules while the push is running, and only it holds a write credential for the remote. A clean push is forwarded with exactly the checked SHAs; a `yellow` finding goes back to the agent with a message it can act on; a `red` finding is rejected without details and waits for a human.

## Status

First batch: the Push Guard only (`push-guard`). All rules of the spec, the pre-receive hook, the HTTP server, the human commands and `replay` are implemented and covered by offline tests against real temporary Git repositories. It has **not been run against a real remote yet**. The Merge Guard and Pull Guard come later as `cmd/merge-guard` and `cmd/pull-guard`. Design and decisions: [SPEC.md](SPEC.md).

## How it works

```
agent ── git push ──> push-guard (wall) ── checked SHAs only ──> remote
                       │ pre-receive hook: ls-remote, rules, gitleaks
                       ├ push.jsonl (every push, approval, reset)
                       └ pending/<repo>/<id>.bundle (red pushes)
```

1. The agent pushes to `<push guard>/<name>.git`. The guard's bare repository runs `push-guard pre-receive` as its hook.
2. The hook reads the remote's refs fresh (`git ls-remote`; the agent's idea of the old state is not trusted), fetches remote objects it lacks into the push's quarantine, and computes per ref the kind of change (`create`, `delete`, `ff`, `non_ff`, `tag_move`), the cumulative diff and the new commits.
3. All rules run; the verdict is the worst color found.
   - **green:** `git push --porcelain --atomic <remote> <sha>:<ref> …` with the guard's credential, never `--force`. Then the agent's push is accepted. If the remote rejects, the agent's push is rejected with the remote's message.
   - **yellow:** rejected with one line per finding (rule, ref, path, line, why). No human is involved; the agent fixes its change or leaves the part out.
   - **red:** rejected with only `rejected: waiting for a human (push <id>)`. The new commits are kept as a bundle and `notify.command` warns the owner.
   - **internal error** (scanner missing, remote unreachable, object missing, timeout): rejected with `internal error, try again later`, logged, counted as red, but not warned as a violation.

## Build and install

Requirements on the wall host: Go 1.24+ to build, `git` (2.42 or newer), and [gitleaks](https://github.com/gitleaks/gitleaks) 8.x on `PATH` or configured as `scanner.gitleaks`. Without gitleaks every push fails closed.

```bash
go build -o push-guard ./cmd/push-guard
go test ./...      # offline; secret-scan tests skip without gitleaks (CI pins one)
```

The live test (`TestLiveRemote`) runs only when `WARDEN_LIVE_REMOTE` (an `https://` URL) and `WARDEN_LIVE_TOKEN_FILE` are set. It pushes through a real guard to a fresh `testrun-<UTC timestamp>-<run>` branch on that remote: green create and fast-forward, red rewrite that leaves the remote alone, approval of that SHA going out with a lease, another writer moving the branch, and finally an allowed delete. CI runs it against this repository on pushes to `main` with the job's own token, and deletes leftover `testrun-*` branches older than a day. `TestSSHRemote` starts its own `sshd` (skipped if `sshd` or `ssh-keygen` is missing; CI installs it and sets `SSH_REQUIRED=1`).

Releases attach a static `push-guard-linux-amd64` binary.

## Configuration

Everything lives on the wall, never in a repo. One folder:

```
warden/
  defaults.yaml          # applies to every repo: agent, notify, state_dir, rule changes
  gitleaks.toml          # optional: scanner config for every repo
  gitleaksignore         # optional: known false positives for every repo
  repos/
    hermetarium/         # folder name = repo name = the path the agent pushes to
      warden.yaml        # required: remote + credential, plus what differs here
      gitleaks.toml      # optional: per-repo scanner config (use [extend] path = ".../gitleaks.toml")
      gitleaksignore     # optional
```

See [examples/warden](examples/warden). A repo file:

```yaml
remote: git@github.com:example/hermetarium.git  # any Git URL or a local path
credential: /etc/warden/keys/hermetarium        # SSH key, or token file for https://
known_hosts: /etc/warden/known_hosts           # optional, SSH only; default: the guard user's own
default_branch: main                            # optional; otherwise ls-remote --symref HEAD
rules:
  PATH-YELLOW:
    allow: ['package-lock\.json']
```

**Layers.** The built-in defaults ([internal/config/defaults.yaml](internal/config/defaults.yaml), compiled in) come first, then the wall's `defaults.yaml`, then the repo's `warden.yaml`. Scalars and limits override. The lists `match`, `allow` and `deny` are appended to, so a default can't vanish unnoticed; `match_remove`, `allow_remove` and `deny_remove` remove an exact entry from the layers below (an entry that isn't there is an error). Unknown keys and rule IDs are errors.

**Repo settings.** `remote` is required and `credential` too, except for a local path or `file://` remote. Both only come from the repo's file. The credential is a file path, never a value: for `ssh://` and `user@host:path` remotes an SSH private key (used with `-i` and `IdentitiesOnly=yes`; the host must be in the guard user's `known_hosts`), for `https://` a file holding a token (sent as password with the user name `x-access-token`). Optional `known_hosts` (SSH remotes only) points to a known_hosts file for this remote instead of the guard user's own; either way an unknown or changed host key is refused (`StrictHostKeyChecking=yes`). `ssh` runs with `-F none`, so no ssh_config on the wall host (`ProxyCommand`, `HostName`, other keys) can change where or how the guard connects.

**Wall settings** (`defaults.yaml` only): `agent.name` (required; in every log entry and warning), `agent.token_file` (password for `serve`), `notify.command` (argv list; gets the warning JSON on stdin), `state_dir` (default `<config>/state`), `pages_branch` (default `gh-pages`), `scanner.gitleaks`, `timeout` (per push, default `60s`), `forward.atomic` (default `true`).

**Rules.** Each rule has `enabled` (default `true`), `color` (`red` or `yellow`), `allow` and `deny` (regular expressions over the rule's subject) and its limits. Patterns always match the whole subject (they are wrapped as `^(?:…)$`). `deny` fires even if `allow` matches; `REF-NAMESPACE` looks at every pushed ref and `PATH-*` at every changed path, so a `deny` there protects a ref or path outright. Subjects: the full ref name for `REF-*`, the path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*` limits, the ref name for per-ref `SIZE-*` totals, the branch name for `META-*`; `REF-COUNT` and `RATE-*` have no subject.

| Rule | Default | Fires on |
| --- | --- | --- |
| `REF-DELETE` | red | a branch or tag is deleted |
| `REF-NON-FF` | red | a branch moves to a commit that doesn't contain the remote's old commit |
| `REF-TAG-MOVE` | red | an existing tag is moved |
| `REF-TAG-NEW` | yellow | a new tag |
| `REF-NAMESPACE` | red | a ref outside `refs/heads/*` and `refs/tags/*` |
| `REF-COUNT` | red | more than `max_refs` (10) refs in one push |
| `PATH-RED` / `PATH-YELLOW` | red / yellow | a changed path (both names of a rename) matches the rule's `match` list |
| `MODE-EXEC` / `MODE-SYMLINK` / `MODE-SUBMODULE` | yellow | a file becomes executable; a symlink or gitlink is added or changed |
| `CONTENT-SECRET` | red | gitleaks finds something in any new commit |
| `CONTENT-SCANNER-ALLOW` | red | an added `gitleaks:allow` or `trufflehog:ignore` |
| `CONTENT-INVISIBLE` | red | bidi controls, zero-width characters, U+FEFF other than at the very start of a file, Unicode tag characters |
| `CONTENT-BINARY` | yellow | a binary file (or text that isn't UTF-8) is added or changed |
| `CONTENT-BLOB` | yellow | 200+ characters of base64 or 100+ of hex on one added line |
| `CONTENT-PAGES-SCRIPT` | yellow | on the Pages branch: `<script src>` or `<link href>` to a host not found in the old tree |
| `META-UNSIGNED` | yellow | an unsigned commit where the last `lookback` (20) commits of the branch carry a signature |
| `META-BACKDATED` | yellow | committer date before a parent's, or more than `max_age` (24h) before the push |
| `META-FUTURE` | yellow | author or committer date more than `max_skew` (10m) after the push |
| `SIZE-LIMIT` | red | over 2,000 files, 50,000 lines, 50 MB of new objects or a 10 MB file |
| `SIZE-LARGE` | yellow | over 200 files, 5,000 lines, 100 commits or a 1 MB file |
| `SIZE-MASS-DELETE` | yellow | more than 20 files deleted, or more than 50 % of a file over 100 lines removed |
| `RATE-LIMIT` | red | more than 30 pushes per hour (answer: `rate limited, try later`) |
| `RATE-YELLOW-STREAK` | red | more than 5 yellow rejections in a repo within 24 h; then every push is red until a human approves a SHA or resets |

The full reasoning per rule is in the Push Guard design notes.

**Secret scanner.** gitleaks runs as `gitleaks git --log-opts "<new> --not <remote refs>"` with `--config` (the repo folder's `gitleaks.toml`, else the wall's, else a generated one that extends the gitleaks defaults), `--gitleaks-ignore-path` (the repo folder's `gitleaksignore`, else the wall's, else an empty file) and `--ignore-gitleaks-allow`, so nothing in the pushed repo can switch it off. Findings are redacted.

Check a configuration with `push-guard check-config --config /etc/warden` (add `--remote` to also run `ls-remote` against every remote with its credential).

## Running it

**Over HTTP** (the usual setup):

```bash
push-guard serve --config /etc/warden --listen 0.0.0.0:8418
```

`serve` answers only Git smart HTTP routes `/<name>.git/…` through `git http-backend`, behind HTTP basic auth with the password from `agent.token_file` (any user name). It creates the guard's bare repository `state_dir/repos/<name>.git` on first use with the hook installed, and syncs it from the remote (branches and tags, with prune) before every ref advertisement, so agents fetch through the guard as well. Put TLS in front of it if the traffic leaves the host.

**Over SSH or a local path:** `push-guard init-repo --config /etc/warden hermetarium` creates and syncs the guard repository; agents then push to that path, e.g. over SSH with `git-shell`. Without `serve` nothing re-syncs it automatically; the hook still reads the remote fresh for every push.

**Agents** use the guard as their only remote:

```bash
git remote set-url origin http://hermit:$TOKEN@warden.internal:8418/hermetarium.git
git push origin agent/fix-typo
```

The agent must not have any other write credential for the remote, otherwise the guard is decoration.

## Human commands

Run on the wall host; being able to run them there is the authentication.

```bash
push-guard approve --config /etc/warden hermetarium refs/heads/main 3f2a9c1   # SHA prefix of a rejected push is enough
push-guard reset-streak --config /etc/warden hermetarium
push-guard replay --config /etc/warden hermetarium --git-dir /path/to/clone --branch main [--skip-scanner] [-v]
push-guard version
```

- `approve`: the agent's next push of exactly that SHA to that ref is forwarded without re-checking (also with a lease if it rewrites or deletes the ref). It ends an active yellow streak. The approval is logged with the rule IDs of the overruled push. Any other SHA is checked from scratch.
- `reset-streak`: ends a `RATE-YELLOW-STREAK` without approving anything.
- `replay`: treats every first-parent step of a branch's history as one push (with the commit's committer time as the time of the push), forwards nothing, writes nothing, and prints the hits per rule. Use it to tune the defaults before going live.

A red push's commits are in `state_dir/pending/<repo>/<id>.bundle`; inspect them with `git fetch <bundle> 'refs/*:refs/pending/*'` in a scratch clone.

## Files on the wall

- `state_dir/push.jsonl`: one JSON line per push (time, id, agent, repo, updates with remote old, new and kind, findings, verdict, forwarded, remote message) and per approval, reset and streak. Append-only. A malformed line makes every push fail closed until a human fixes it.
- `state_dir/pending/<repo>/<id>.bundle`: new commits of red pushes, kept for now.
- `state_dir/repos/<name>.git`: the guard repositories.
- `state_dir/error.log`: errors that could not go into `push.jsonl`.

## Warnings

`notify.command` gets a JSON object on stdin for red pushes (`red_push`), the start of a yellow streak (`yellow_streak`), the first push of a rate-limited series (`rate_limited`) and pushes to an unknown repo (`unknown_repo`). Facts come first: updates, findings with rule IDs, the bundle path and the `approve` commands. The agent's commit messages only appear under `untrusted`, truncated and marked as an unverified quote.

## Limits

- Tested live against GitHub over HTTPS, and over SSH against a local `sshd` the test starts itself (fresh host and user keys, wrong and unknown host key, unauthorized key). GitLab and Gitea are not tested yet.
- `META-UNSIGNED` checks only that a signature is present (`gpgsig` header). The wall has no keyring, so it doesn't verify signatures; with fewer than `lookback` commits of history the rule stays quiet.
- `CONTENT-PAGES-SCRIPT` looks at one added line at a time, so a tag split across lines is missed, and a host counts as known if its name appears anywhere in the old tree.
- `push.jsonl` is read in full on every push; fine for now, it will need rotation or an index later.
- One Push Guard per agent: `agent.name` is per installation, and rate limits count all pushes of that installation.
- No registry checks for new dependencies (R17) and no AI judge: the Push Guard is rules only.

## License

[PolyForm Noncommercial 1.0.0](LICENSE). Source-available, not OSI Open Source.
