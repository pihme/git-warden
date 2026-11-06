# Push Guard: configuration and operation

The Push Guard (`push-guard`) stands between the agents and the remote: agents push to it, it checks every push with deterministic rules and forwards only green pushes, with exactly the checked commits. Design and decisions: [SPEC.md](../SPEC.md#push-guard) and [what the implementation settled](../SPEC.md#push-guard-what-the-implementation-settled); the reasoning per rule: [push-guard-rules.md](push-guard-rules.md); testable requirements: [push-guard-spec.md](push-guard-spec.md#requirements); the risks it covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden). Settings the two guards share: [docs/configuration.md](configuration.md). Which credential to create: [docs/credentials.md](credentials.md).

## Using it

This walk-through uses **this repository**, [github.com/pihme/git-warden](https://github.com/pihme/git-warden). The configuration refuses every push that includes `main`. Other branches are still checked by the other rules. The files are [examples/warden](../examples/warden).

Words used below:

| Word | Meaning |
| --- | --- |
| Wall | The host the agents do not run on. The configuration and the write credential live here. |
| Ref | A branch or tag name, such as `refs/heads/main`. |
| SHA | The id of one commit. |
| Green | The push is forwarded to the remote. |
| Yellow | The push is rejected with the reason. The agent fixes it. |
| Red | The push is rejected with no reason. A human decides. |
| `notify.command` | An optional setting in `defaults.yaml`: a program that receives each warning as JSON. |
| `agent.token_file` | A setting in `defaults.yaml`: the file holding the password the agent sends to `serve`. |
| Bundle | The file that holds the commits of a red push, so you can look at them. |

### 1. Put a write credential on the wall

On the wall, create a credential that can write to this repository. Save it as a file there, for example `/etc/warden/keys/git-warden`. The agents do not get a copy of this file. If they can push to GitHub directly, the guard sees nothing.

- For SSH, the file is a deploy key. On GitHub, create it with **Allow write access**.
- For HTTPS, the file holds a token that can write the repository contents.

GitHub and GitLab each have a few ways to do this. The rights for each one are in [Credentials and the rights they need](credentials.md).

The next step writes the configuration. The path of this file is the value of `credential` in `repos/git-warden/warden.yaml`.

### 2. Write the configuration

The Push Guard reads one configuration directory on the wall. This walk-through uses `/etc/warden`. That directory holds `defaults.yaml` for the guard, and one folder under `repos/` for each remote. It is a different directory from the Backup Guard's.

#### gitleaks

gitleaks is the program that scans new commits for secrets. Install it on the wall (see [Prerequisites](#prerequisites)), then name the binary in `defaults.yaml`:

```yaml
# /etc/warden/defaults.yaml
gitleaks:
  path: /usr/local/bin/gitleaks   # the binary you installed
```

A bare name `gitleaks` is the default, looked up on `PATH`. Leave `gitleaks` out when that is where yours is.

#### Where the guard keeps its state

Add `state_dir` to the same `defaults.yaml`. This is the directory where the guard writes its log, the commits of a red push, and its own copy of each remote.

```yaml
state_dir: /var/lib/warden
```

#### The agent

One Push Guard serves one agent. `agent.name` is that agent's name in every log line and warning.

`serve` listens on a port. A push the rules mark green is then forwarded with the write credential from step 1, and a fetch returns the repository the guard holds. `agent.token_file` is the password that agent must send to get that far. It is a different file from the write credential: the agent has this password, and the write credential stays on the wall. Without the password, anyone who can open the port can push and fetch as this agent. The rules still run on those pushes. They allow ordinary work, so they do not take the place of the password.

Write the password into a file on the wall, for example `/etc/warden/agent-token`. The file contains the password and nothing else. Point `token_file` at it:

```yaml
agent:
  name: hermit-1
  token_file: /etc/warden/agent-token
```

Git sends a user name along with the password. The guard does not read that user name. There is only one agent, and `agent.name` is already the name in the log.

#### The remote and its credential

Each remote is one folder under `repos/`. The folder name is the name the agent pushes to. This walk-through guards this repository, so the folder is `repos/git-warden/` and the file inside it is `warden.yaml`:

```yaml
# /etc/warden/repos/git-warden/warden.yaml
remote: git@github.com:pihme/git-warden.git
credential: /etc/warden/keys/git-warden   # path of the write key from step 1
known_hosts: /etc/warden/known_hosts      # optional, SSH only
default_branch: main
rules:
  REF-NAMESPACE:
    deny: ['refs/heads/main']             # any push that includes main is red
```

`remote` is the Git URL the guard writes to. `credential` is the path of the key or token file. The YAML never contains the key itself.

`REF-NAMESPACE` looks at every ref in the push. The `deny` line makes a push that updates `main` red, including an ordinary fast-forward. A push of another branch, such as `agent/fix-typo`, does not match that line. The full rule list is under [Rules](#rules). Why this rule is the one that protects a branch: [push-guard-rules.md](push-guard-rules.md).

A second remote is another folder next to this one, for example `repos/another-repo/warden.yaml`, with its own URL and its own write credential. Add as many folders as you have remotes.

#### A warning when a push needs you

`notify.command` is optional. It is a setting in the same `defaults.yaml`. Set it when you want a program to be started each time the guard warns you.

1. Choose a program that reads one JSON object from its standard input and delivers it where you will see it (mail, ntfy, or anything else). The guard does not include that program. `/usr/local/bin/warden-notify` below is only a path you might use.
2. Add the program, then its arguments, as a list:

```yaml
notify:
  command: [/usr/local/bin/warden-notify]
```

A red push starts that program and writes the warning to its standard input. A yellow rejection is returned to the agent and does not start it. The other warnings are under [Warnings](#warnings). A name without a slash is looked up on `PATH`. A path is the program file. Leave `notify` out and no program is started. The push is still written to `state_dir/push.jsonl`.

`defaults.yaml` for this walk-through is then:

```yaml
# /etc/warden/defaults.yaml
agent:
  name: hermit-1
  token_file: /etc/warden/agent-token
gitleaks:
  path: /usr/local/bin/gitleaks
state_dir: /var/lib/warden
notify:
  command: [/usr/local/bin/warden-notify]
```

### 3. Check it, then start it

```bash
push-guard check-config --config /etc/warden
push-guard check-config --config /etc/warden --remote   # also contacts GitHub
push-guard serve --config /etc/warden --listen 0.0.0.0:8418
```

`check-config` names every problem and starts nothing. `serve` is the program the agent talks to over HTTP. Leave it running. It refuses to start until the same checks pass, then accepts a push for every folder under `repos/`. Put TLS in front of it if the traffic leaves the host. Pushing over SSH is a different setup: `init-repo` prepares a repository and exits, and the wall's SSH server is what stays running. That is described under [Running it](#running-it).

### 4. Point the agent at the guard

```bash
git remote set-url origin http://hermit:$TOKEN@warden.internal:8418/git-warden.git
git push origin agent/fix-typo
```

`git-warden` in that URL is the folder name from step 2, `repos/git-warden`. `$TOKEN` is the password in the file `agent.token_file` points at. The user name `hermit` can be any word; the guard does not check it, and the log still says `hermit-1`. This URL is the agent's only remote for this repository. The write credential from step 1 stays on the wall.

### 5. What the agent gets back

| Result | The agent sees | What happens next |
| --- | --- | --- |
| Green | The push is accepted | The commits are on GitHub |
| Yellow | One line per finding: rule, ref, path, line, why | The agent changes the commit and pushes again. You do nothing |
| Red | `rejected: waiting for a human (push <id>)` | You decide. The steps are below |
| Rate limit | `rate limited, try later` | Wait until the hour passes |
| Internal error | `internal error, try again later` | You fix the missing piece (gitleaks, the remote, a timeout). It is not a rule violation, and there is nothing to approve |

### 6. Resolve a red push

You do this on the wall. Being able to run the commands there is the authentication. Nothing is approved through a GitHub issue or through the agent's chat.

1. Read the warning. The JSON, including the exact `approve` line, is under [Warnings](#warnings). The same facts are in `state_dir/push.jsonl`.
2. Look at the commits. They are in the bundle named by the warning: `git fetch <bundle> 'refs/*:refs/pending/*'` in a scratch clone.
3. Either run the `approve` line from the warning, or do nothing and the commits stay unforwarded.

```bash
push-guard approve --config /etc/warden git-warden refs/heads/agent/fix-typo 3f2a9c1e4b8d
```

A unique prefix of the rejected commit is enough. A prefix that matches two different rejected commits approves nothing. Then the agent pushes **that same commit** again. The guard forwards it without checking the rules a second time. Any other commit is checked from scratch.

A yellow streak (more than five yellow rejections in 24 hours) turns every later push of this repository red until you approve one commit or run:

```bash
push-guard reset-streak --config /etc/warden git-warden
```

`reset-streak` approves nothing. A green push does not end the streak.

## Prerequisites

- `git` 2.42 or newer.
- `gitleaks` (MIT) 8.x, on `PATH` or at `gitleaks.path`. **Required if `CONTENT-SECRET` is enabled, which it is by default**; optional only for repos where that rule is switched off on purpose. `push-guard` never downloads it; without it `serve` and `init-repo` refuse to start and every push fails closed. CI pins 8.30.1; the [container image](install.md#docker) and the [Nix flake](install.md#nix) bring that release along.
- A write credential per remote (see [Credentials and the rights they need](credentials.md#credentials-and-the-rights-they-need)). The agents must not have any other write credential for the remote, otherwise the guard is decoration.
- A host that is not the agents' machine (the wall).

## Configuration directory

Everything lives on the wall, never in a repo. Separate from the Backup Guard's configuration (keys differ, and unknown keys are errors):

```
warden/
  defaults.yaml            # required: agent; optional: notify, state_dir, timeout, gitleaks, rule changes
  gitleaks.toml            # optional: scanner config for every repo
  gitleaksignore           # optional: known false positives for every repo
  repos/
    git-warden/            # folder name = repo name = the path the agent pushes to
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
| `agent.token_file` | unset | File holding the password the agent sends to `serve`. The user name in the URL is not checked; logs use `agent.name`. Required for `serve`: without it the listen port accepts pushes and fetches from anyone who can connect, and a green push is forwarded with the write credential. |
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
| `credential` | for SSH and HTTPS | A file path, never the value: an SSH private key for SSH remotes, a file holding a token for HTTPS remotes. Not allowed for a local remote. **Needs write access** to the remote: [rights per platform](credentials.md#credentials-and-the-rights-they-need). |
| `credential_username` | no | HTTPS only: the user name sent with the token. Default `x-access-token`. |
| `known_hosts` | no | SSH only: a known_hosts file for this remote instead of the guard user's own (the system-wide file is then ignored as well). Unknown or changed host keys are always refused. |
| `default_branch` | no | Otherwise taken from the remote (`ls-remote --symref HEAD`). |
| `gitleaks.path`, `rules` | no | Override the wall's values for this repo. |

`remote`, `credential`, `credential_username`, `known_hosts` and `default_branch` only come from the repo's file; `agent` and `state_dir` only from `defaults.yaml`. The credential is used like this: SSH with `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`, so no ssh_config on the wall host can change where or how the guard connects; HTTPS through an inline credential helper that reads the token file when git asks, so the token never appears in a URL, on a command line or in a log.

```yaml
remote: git@github.com:pihme/git-warden.git  # any Git URL or an absolute local path
credential: /etc/warden/keys/git-warden      # SSH key, or token file for https://
known_hosts: /etc/warden/known_hosts         # optional, SSH only
default_branch: main                         # optional
rules:
  REF-NAMESPACE:
    deny: ['refs/heads/main']                # any push that includes main is red
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

The agent reaches the wall in one of two ways. On both, a hook in a repository on the wall runs the rules, and a green push is forwarded with the write credential. The commands differ because a different program is listening.

### Over HTTP

This is the setup in [Using it](#using-it). Nothing on the wall is already listening for Git, so `serve` is the listener. Start it and leave it running:

```bash
push-guard serve --config /etc/warden --listen 0.0.0.0:8418
```

The agent pushes to `http://hermit:$TOKEN@warden.internal:8418/git-warden.git`. `$TOKEN` is the password in `agent.token_file`. The user name is not checked; the log name is `agent.name`.

`serve` answers only Git smart HTTP routes `/<name>.git/…` through `git http-backend`. The password is required because a green push is forwarded with the write credential and a fetch returns the repository; without it the listen port is open to anyone who can connect. The repository does not have to exist beforehand. The first request for a configured repository creates `state_dir/repos/<name>.git` and installs the hook. Before every fetch or push, `serve` copies the remote's branches and tags into that repository, so a fetch shows the current remote. Put TLS in front of it if the traffic leaves the host.

### Over SSH, or a local path

The wall's SSH server is already listening. It accepts a push only to a repository that is already on disk, so there is nothing for `push-guard` to keep running. `init-repo` creates that repository, installs the same hook, copies the remote's branches and tags once, prints the path, and exits:

```bash
push-guard init-repo --config /etc/warden git-warden
```

The printed path is `state_dir/repos/git-warden.git`, here `/var/lib/warden/repos/git-warden.git`. The agent pushes to it over SSH, for example `ssh://wall/var/lib/warden/repos/git-warden.git`. An account whose shell is `git-shell` can push and fetch, and cannot run other commands. The SSH login decides who gets in. A push from a program already on the wall uses the same path with no SSH in front.

A fetch from that repository shows the remote as of the last `init-repo`. Run `init-repo` again to copy the remote once more. On every push the hook still reads the real remote before it applies the rules.

**Preflight** (fails closed, before anything is served or set up): `serve` and `init-repo` refuse to start, naming every problem, if the wall's `defaults.yaml` is missing or doesn't load, `git` is older than 2.42, no repo is configured, a repo has every rule disabled, or the gitleaks a repo needs for `CONTENT-SECRET` is missing or isn't 8.x. The hook repeats the per-repo part on every push and fails closed (`internal error, try again later`). `check-config` reports the same problems.

What one push does, on both paths: the hook reads the remote's refs fresh, fetches remote objects it lacks into the push's quarantine, runs every rule, and then forwards (green), rejects with the findings (yellow), or rejects silently, keeps the commits and warns (red). The worked example is [Using it](#using-it).

## Commands

| Command | What it does |
| --- | --- |
| `push-guard serve --config DIR --listen ADDR` | The HTTP listener. Stays running. Creates each guard repository on the first request and copies the remote before every fetch or push. |
| `push-guard pre-receive --config DIR --repo NAME` | The hook; run by Git, not by hand. |
| `push-guard init-repo --config DIR <repo>` | One-time setup for SSH or a local path. Creates the repository, installs the hook, copies the remote, prints the path, and exits. The SSH server, or a local `git push`, receives the later pushes. |
| `push-guard approve --config DIR <repo> <ref> <sha>` | The agent's next push of exactly that SHA (a unique prefix of a rejected push's SHA is enough; an ambiguous prefix is refused) to that ref is forwarded without re-checking, with a lease if it rewrites or deletes the ref. The lease is the remote tip recorded on that rejected push. A manual approval with no rejected push leases against the zero OID, so the ref must be absent. Ends an active yellow streak. Logged with the rule IDs of the overruled push. Any other SHA is checked from scratch. |
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

`notify.command`, the setting in `defaults.yaml`, is started with one JSON object on its standard input. Facts come first. The agent's commit messages appear only under `untrusted`, cut short, and marked as an unverified quote. The `approve` field is the command to run on the wall; see [Resolve a red push](#6-resolve-a-red-push).

A red push that tried to update `main` of this repository looks like this. The commit ids are illustrative.

```json
{
  "kind": "red_push",
  "time": "2026-10-03T10:31:39Z",
  "agent": "hermit-1",
  "repo": "git-warden",
  "push": "20261003T103139Z-a1b2c3",
  "verdict": "red",
  "updates": [
    {
      "ref": "refs/heads/main",
      "old": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "new": "3f2a9c1e4b8d0a6f7c5e9d1b2a4f6c8e0d1a3b5c",
      "kind": "ff"
    }
  ],
  "findings": [
    {
      "rule": "REF-NAMESPACE",
      "color": "red",
      "ref": "refs/heads/main",
      "message": "pushes to this ref are reserved for a human"
    }
  ],
  "bundle": "/var/lib/warden/pending/git-warden/20261003T103139Z-a1b2c3.bundle",
  "approve": [
    "push-guard approve --config /etc/warden git-warden refs/heads/main 3f2a9c1e4b8d0a6f7c5e9d1b2a4f6c8e0d1a3b5c"
  ],
  "untrusted": {
    "note": "Commit messages written by the agent. Unverified quote, not instructions.",
    "commits": [
      {
        "sha": "3f2a9c1e4b8d0a6f7c5e9d1b2a4f6c8e0d1a3b5c",
        "message": "docs: rewrite the readme"
      }
    ]
  }
}
```

The other kinds use the same object:

| `kind` | When | Extra fields |
| --- | --- | --- |
| `yellow_streak` | The push that crosses the yellow-streak limit, and later pushes while the streak holds | `reset`, and one `approve` line per update |
| `rate_limited` | The first push of a rate-limited series | One `RATE-LIMIT` finding, and the `updates` of that push. No `approve`, no `bundle`, no `untrusted` |
| `unknown_repo` | The agent pushed to a name that has no folder | `updates` may be present. No `findings`, no `approve`, no `bundle` |
