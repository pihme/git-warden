# Push Guard: configuration and operation

The Push Guard (`push-guard`) stands between the agents and the remote: agents push to it, it checks every push with deterministic rules and forwards only green pushes, with exactly the checked SHAs. Design and decisions: [SPEC.md](../SPEC.md#push-guard) and [what the implementation settled](../SPEC.md#push-guard-what-the-implementation-settled); the reasoning per rule: [push-guard-rules.md](push-guard-rules.md); the risks it covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden). The settings both guards share are described the same way in [docs/backup-guard.md](backup-guard.md) and summarised in the [README](../README.md#configuration-and-operation).

## Prerequisites

- `git` 2.42 or newer.
- `gitleaks` (MIT) 8.x, on `PATH` or at `gitleaks.path`. **Required if `CONTENT-SECRET` is enabled, which it is by default**; optional only for repos where that rule is switched off on purpose. `push-guard` never downloads it; without it `serve` and `init-repo` refuse to start and every push fails closed. CI pins 8.30.1; the [container image](../README.md#docker) and the [Nix flake](../README.md#nix) bring that release along.
- A write credential per remote (see [Credentials and the rights they need](../README.md#credentials-and-the-rights-they-need)). The agents must not have any other write credential for the remote, otherwise the guard is decoration.
- A host that is not the agents' machine (the "wall").

## Configuration directory

Everything lives on the wall, never in a repo. Separate from the Backup Guard's configuration (keys differ, and unknown keys are errors):

```
warden/
  defaults.yaml            # required: agent; optional: notify, state_dir, timeout, gitleaks, rule changes
  gitleaks.toml            # optional: scanner config for every repo
  gitleaksignore           # optional: known false positives for every repo
  repos/
    hermetarium/           # folder name = repo name = the path the agent pushes to
      warden.yaml          # required: remote (+ credential), plus what differs here
      gitleaks.toml        # optional: per-repo scanner config (use [extend] path = ".../gitleaks.toml")
      gitleaksignore       # optional
```

See [examples/warden](../examples/warden). Relative file paths in either file are relative to the configuration directory.

**Layers.** The built-in defaults ([internal/config/defaults.yaml](../internal/config/defaults.yaml), compiled in) come first, then the wall's `defaults.yaml`, then the repo's `warden.yaml`. Scalars and limits override. The lists `match`, `allow` and `deny` are appended to, so a default can't vanish unnoticed; `match_remove`, `allow_remove` and `deny_remove` remove an exact entry from the layers below (an entry that isn't there is an error). Unknown keys and rule IDs are errors.

### `defaults.yaml`

| Key | Default | Meaning |
| --- | --- | --- |
| `agent.name` | required | The agent this Push Guard serves; in every log entry and warning. One Push Guard per agent. |
| `agent.token_file` | unset | File holding the password agents use for `serve` (HTTP basic auth, any user name). Required for `serve`. |
| `gitleaks.path` | `gitleaks` on `PATH` | The gitleaks binary: a bare name is looked up on `PATH`, a path is relative to the configuration directory. May also be set per repo. |
| `notify.command` | unset | argv list; gets a warning as JSON on stdin (see [Warnings](#warnings)). |
| `state_dir` | `<config>/state` | Guard repositories, `push.jsonl`, pending bundles, locks. |
| `timeout` | `60s` | Per push; the push fails closed when it is exceeded. |
| `pages_branch` | `gh-pages` | The branch `CONTENT-PAGES-SCRIPT` looks at. An empty value disables the rule entirely (no fallback to `gh-pages`). |
| `forward.atomic` | `true` | Forward all refs of a push in one atomic push. |
| `rules` | built-in | Rule changes for every repo (see [Rules](#rules)). |

### `repos/<name>/warden.yaml`

| Key | Required | Meaning |
| --- | --- | --- |
| `remote` | yes | The remote: `ssh://…` or `user@host:path` (SSH), `https://…` or `http://…` (HTTPS), or a local repository as an absolute path or `file://` URL. |
| `credential` | for SSH and HTTPS | A file path, never the value: an SSH private key for SSH remotes, a file holding a token for HTTPS remotes. Not allowed for a local remote. **Needs write access** to the remote: [rights per platform](../README.md#credentials-and-the-rights-they-need). |
| `credential_username` | no | HTTPS only: the user name sent with the token. Default `x-access-token`. |
| `known_hosts` | no | SSH only: a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well). Unknown or changed host keys are always refused. |
| `default_branch` | no | Otherwise taken from the remote (`ls-remote --symref HEAD`). |
| `gitleaks.path`, `rules` | no | Override the wall's values for this repo. |

`remote`, `credential`, `credential_username`, `known_hosts` and `default_branch` only come from the repo's file; `agent` and `state_dir` only from `defaults.yaml`. The credential is used like this: SSH with `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`, so no ssh_config on the wall host can change where or how the guard connects; HTTPS through an inline credential helper that reads the token file when git asks, so the token never appears in a URL, on a command line or in a log.

```yaml
remote: git@github.com:example/hermetarium.git  # any Git URL or an absolute local path
credential: /etc/warden/keys/hermetarium        # SSH key, or token file for https://
known_hosts: /etc/warden/known_hosts           # optional, SSH only
default_branch: main                            # optional
rules:
  PATH-YELLOW:
    allow: ['package-lock\.json']
```

### Rules

Each rule has `enabled` (default `true`), `color` (`red` or `yellow`), `allow` and `deny` (regular expressions over the rule's subject) and its limits. Patterns always match the whole subject (they are wrapped as `^(?:…)$`). `deny` fires even if `allow` matches; `REF-NAMESPACE` looks at every pushed ref and `PATH-*` at every changed path, so a `deny` there protects a ref or path outright. Subjects: the full ref name for `REF-*`, the path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*` limits, the ref name for per-ref `SIZE-*` totals, the branch name for `META-*`; `REF-COUNT` and `RATE-*` have no subject. A repo with every rule disabled is refused.

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
| `CONTENT-PAGES-SCRIPT` | yellow | on the Pages branch: `<script src>` or `<link href>` to a host not found in the old tree; empty `pages_branch` turns the rule off |
| `META-UNSIGNED` | yellow | an unsigned commit where the last `lookback` (20) commits of the branch carry a signature |
| `META-BACKDATED` | yellow | committer date before a parent's, or more than `max_age` (24h) before the push |
| `META-FUTURE` | yellow | author or committer date more than `max_skew` (10m) after the push |
| `SIZE-LIMIT` | red | over 2,000 files, 50,000 lines, 50 MB of new objects or a 10 MB file |
| `SIZE-LARGE` | yellow | over 200 files, 5,000 lines, 100 commits or a 1 MB file |
| `SIZE-MASS-DELETE` | yellow | more than 20 files deleted, or more than 50 % of a file over 100 lines removed |
| `RATE-LIMIT` | red | more than 30 pushes per hour (answer: `rate limited, try later`) |
| `RATE-YELLOW-STREAK` | red | more than 5 yellow rejections in a repo within 24 h; then every push is red until a human approves a SHA or resets |

The full reasoning per rule: [push-guard-rules.md](push-guard-rules.md). Tune the rules against real history with [`replay`](#commands) before going live.

**Secret scanner.** gitleaks runs as `gitleaks git --log-opts "<new> --not <remote refs>"` with `--config` (the repo folder's `gitleaks.toml`, else the wall's, else a generated one that extends the gitleaks defaults), `--gitleaks-ignore-path` (the repo folder's `gitleaksignore`, else the wall's, else an empty file) and `--ignore-gitleaks-allow`, so nothing in the pushed repo can switch it off. Findings are redacted. Switching `CONTENT-SECRET` off (`rules: {CONTENT-SECRET: {enabled: false}}`) is the only way to run without gitleaks, and it is a deliberate choice per repo or for the wall.

Check a configuration with:

```bash
push-guard check-config --config /etc/warden            # preflight, files, credentials
push-guard check-config --config /etc/warden --remote   # plus ls-remote per repo
```

## Running it

```bash
push-guard serve --config /etc/warden --listen 0.0.0.0:8418   # over HTTP, the usual setup
push-guard init-repo --config /etc/warden hermetarium        # over SSH or a local path
```

**Preflight** (fails closed, before anything is served or set up): `serve` and `init-repo` refuse to start, naming every problem, if the wall's `defaults.yaml` is missing or doesn't load, `git` is older than 2.42, no repo is configured, a repo has every rule disabled, or the gitleaks a repo needs for `CONTENT-SECRET` is missing or isn't 8.x. The hook repeats the per-repo part on every push and fails closed (`internal error, try again later`). `check-config` reports the same problems.

**Over HTTP:** `serve` answers only Git smart HTTP routes `/<name>.git/…` through `git http-backend`, behind HTTP basic auth with the password from `agent.token_file` (any user name). It creates the guard's bare repository `state_dir/repos/<name>.git` on first use with the hook installed, and syncs it from the remote (branches and tags, with prune) before every ref advertisement, so agents fetch through the guard as well. Put TLS in front of it if the traffic leaves the host.

**Over SSH or a local path:** `init-repo` creates and syncs the guard repository; agents then push to that path, e.g. over SSH with `git-shell`. Without `serve` nothing re-syncs it automatically; the hook still reads the remote fresh for every push.

**Agents** use the guard as their only remote:

```bash
git remote set-url origin http://hermit:$TOKEN@warden.internal:8418/hermetarium.git
git push origin agent/fix-typo
```

What one push does: the hook reads the remote's refs fresh, fetches remote objects it lacks into the push's quarantine, runs every rule, and then forwards (green), rejects with the findings (yellow), or rejects silently, keeps the commits and warns (red); see [How it works](../README.md#push-guard).

## Commands

| Command | What it does |
| --- | --- |
| `push-guard serve --config DIR --listen ADDR` | The HTTP server (see above). |
| `push-guard pre-receive --config DIR --repo NAME` | The hook; run by Git, not by hand. |
| `push-guard init-repo --config DIR <repo>` | Creates and syncs a guard repository for SSH or local pushes. |
| `push-guard approve --config DIR <repo> <ref> <sha>` | The agent's next push of exactly that SHA (a prefix of a rejected push's SHA is enough) to that ref is forwarded without re-checking, with a lease if it rewrites or deletes the ref. Ends an active yellow streak. Logged with the rule IDs of the overruled push. Any other SHA is checked from scratch. |
| `push-guard reset-streak --config DIR <repo>` | Ends a `RATE-YELLOW-STREAK` without approving anything. |
| `push-guard check-config --config DIR [--remote]` | Preflight, files and credentials; `--remote` adds `ls-remote` per repo. |
| `push-guard replay --config DIR <repo> --git-dir PATH [--branch main] [--skip-scanner] [-v]` | Treats every first-parent step of a branch's history as one push (with the commit's committer time as the time of the push), forwards nothing, writes nothing, and prints the hits per rule. Use it to tune the rules before going live. |
| `push-guard version` | Prints the version. |

`approve`, `reset-streak` and `replay` run on the wall host; being able to run them there is the authentication. Exit codes: `0` success, `1` failure, `2` usage error.

## State

- `state_dir/repos/<name>.git`: the guard repositories, with the hook installed.
- `state_dir/push.jsonl`: one JSON line per push (time, id, agent, repo, updates with remote old, new and kind, findings, verdict, forwarded, remote message) and per approval, reset and streak. Append-only. A malformed line makes every push fail closed until a human fixes it.
- `state_dir/pending/<repo>/<id>.bundle`: new commits of red pushes, kept for now.
- `state_dir/error.log`: errors that could not go into `push.jsonl`.

## Looking and restoring

The remote never received a rejected push, so there is nothing to restore there. A red push's commits are in `state_dir/pending/<repo>/<id>.bundle`; inspect them in a scratch clone with `git fetch <bundle> 'refs/*:refs/pending/*'`, then `approve` the SHA or leave it. `push.jsonl` answers what was pushed, decided and forwarded when.

## Warnings

`notify.command` gets a JSON object on stdin for red pushes (`red_push`), the start of a yellow streak (`yellow_streak`), the first push of a rate-limited series (`rate_limited`) and pushes to an unknown repo (`unknown_repo`). Facts come first: updates, findings with rule IDs, the bundle path and the `approve` commands. The agent's commit messages only appear under `untrusted`, truncated and marked as an unverified quote.
