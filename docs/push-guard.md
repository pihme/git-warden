# Push Guard: configuration and operation

The Push Guard (`push-guard`) stands between the agents and the remote. Agents push to the guard. It checks every push with deterministic rules. It forwards only a push that comes out green, and it forwards exactly the commits it checked.

Where to read more:

- Design and the decisions already taken: [SPEC.md](../SPEC.md#push-guard) and [what the implementation settled](../SPEC.md#push-guard-what-the-implementation-settled).
- Why each rule exists: [push-guard-rules.md](push-guard-rules.md).
- How to change the rules, and how to try them on history you already have: [push-guard-configuring-rules.md](push-guard-configuring-rules.md).
- The testable requirements: [push-guard-spec.md](push-guard-spec.md#requirements).
- Which risks the guard covers: [Coverage by Git Warden](../SPEC.md#coverage-by-git-warden).
- Settings both guards share: [docs/configuration.md](configuration.md).
- Which credential to create: [docs/credentials.md](credentials.md).

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

`serve` listens on a port. A push the rules mark green is then forwarded with the write credential from step 1. A fetch returns the repository the guard holds.

`agent.token_file` is the password that agent must send before any of that happens. It is a different file from the write credential. The agent has this password. The write credential stays on the wall.

Without the password, anyone who can open the port can push and fetch as this agent. The rules still run on those pushes. They allow ordinary work, such as a fast-forward to a branch that is not reserved. They do not take the place of the password.

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

A red push starts that program. The guard writes the warning to the program's standard input. A yellow rejection is returned to the agent and does not start the program. The other warnings, and the shape of the JSON, are under [Warnings](#warnings).

A name with no slash is looked up on `PATH`. A name with a slash is the path of the program file. Leave `notify` out and no program is started. The push is still written to `state_dir/push.jsonl`.

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

`check-config` names every problem and starts nothing. Run it before `serve`.

`serve` is the program the agent talks to over HTTP. Leave it running. It refuses to start until the same checks pass. Once it is running, it accepts a push for every folder under `repos/`.

Put TLS in front of `serve` when the traffic leaves the host. The agent's password is in the request.

### 4. Point the agent at the guard

```bash
git remote set-url origin http://hermit:$TOKEN@warden.internal:8418/git-warden.git
git push origin agent/fix-typo
```

`git-warden` in that URL is the folder name from step 2, `repos/git-warden`.

`$TOKEN` is the password in the file `agent.token_file` points at. The user name `hermit` can be any word. The guard does not check it. The log still says `hermit-1`, which is `agent.name`.

This URL is the agent's only remote for this repository. The write credential from step 1 stays on the wall. The agent never uses the GitHub URL.

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

A unique prefix of the rejected commit is enough. You do not have to type the full SHA. A prefix that matches two different rejected commits approves nothing.

The agent then pushes **that same commit** again. The guard forwards it and does not run the rules a second time. Any other commit is checked from scratch.

A yellow streak (more than five yellow rejections in 24 hours) turns every later push of this repository red until you approve one commit or run:

```bash
push-guard reset-streak --config /etc/warden git-warden
```

`reset-streak` approves nothing. A green push does not end the streak.

## Prerequisites

- `git` 2.42 or newer.
- `gitleaks` (MIT) 8.x, on `PATH` or at `gitleaks.path`. The secret scan (`CONTENT-SECRET`) is on by default, and then gitleaks is required. Switch that rule off for a repo that should not be scanned, and gitleaks is optional for that repo. `push-guard` never downloads gitleaks. Without it, `serve` refuses to start, and every push is rejected with `internal error, try again later`. CI pins 8.30.1. The [container image](install.md#docker) and the [Nix flake](install.md#nix) bring that release along.
- One write credential per remote. The rights for each platform are in [Credentials and the rights they need](credentials.md#credentials-and-the-rights-they-need). The agents must not have a write credential of their own for that remote. If they can push to the remote directly, the guard never sees the push.
- A host that is not the agents' machine. That host is the wall.

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

**Layers.** Three layers are merged, in this order:

1. The built-in defaults, compiled into the program from [internal/config/defaults.yaml](../internal/config/defaults.yaml).
2. The wall's `defaults.yaml`.
3. The repo's `warden.yaml`.

A number, a duration, `enabled`, and `color` in a later layer replace the earlier value.

`match`, `allow`, and `deny` are lists of patterns. A list in a later layer is added to the list from earlier. The earlier patterns stay. To take one pattern off, repeat that same text under `match_remove`, `allow_remove`, or `deny_remove`. The text has to be a copy of a pattern that is already on the list. A pattern that is not on the list is an error, so a typo cannot quietly do nothing.

An unknown key is an error. An unknown rule id is an error.

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

`remote`, `credential`, `credential_username`, `known_hosts`, and `default_branch` are read only from the repo's file. `agent` and `state_dir` are read only from `defaults.yaml`. A repo file cannot change which agent this guard serves, and `defaults.yaml` cannot point one repo at another repo's remote.

For an SSH remote the guard runs `ssh -F none -i <key> -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes`. `-F none` means no `ssh_config` on the wall can change where or how it connects.

For an HTTPS remote, git asks a small helper for the password, and the helper reads the credential file at that moment. The token is not written into a URL, onto a command line, or into a log.

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

Each rule can be turned off. `enabled` defaults to `true`. A repository in which every rule is turned off is refused.

`color` is `red` or `yellow`. The default for each rule is in the table below. Red means a human decides. Yellow means the agent is told the reason and is expected to fix the push.

`allow` and `deny` are regular expressions. The guard matches each one against the whole subject: a pattern of `agent/.*` does not match `refs/heads/agent/fix-typo`. `deny` wins when the subject matches both lists.

The subject is the string the pattern is compared with:

- For `REF-*`, the full ref name, such as `refs/heads/main`.
- For `PATH-*`, `MODE-*`, `CONTENT-*`, and a limit that applies to one file, the path, with no leading slash.
- For a `SIZE-*` total that covers a whole ref, the ref name.
- For `META-*`, the branch name, without the `refs/heads/` prefix.
- `REF-COUNT` and `RATE-*` have no subject. `allow` and `deny` do not apply to them.

`REF-NAMESPACE` looks at every ref in the push. `PATH-*` looks at every changed path. A `deny` on those rules fires by itself, so it can reserve a ref or a path. The other rules fire only when their own event happens, such as a delete or a file that is not a fast-forward. On those, `deny` beats `allow` for a subject the event already hit. It does not create the event.

How the lists are added to the defaults, and how to take one pattern off, is under [Configuring the rules](#configuring-the-rules).

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

The full reasoning per rule: [push-guard-rules.md](push-guard-rules.md).

### Configuring the rules

The table above is what each rule does with the built-in defaults.

A change goes under `rules:` in one of two files.

- `defaults.yaml` applies to every repo of this guard.
- `repos/<name>/warden.yaml` applies to that one repo.

For one rule you can:

- turn it off, with `enabled: false`
- choose who deals with a hit, with `color: red` or `color: yellow`
- add a pattern, with `match`, `allow`, or `deny`
- take a pattern away, with `match_remove`, `allow_remove`, or `deny_remove`
- set one of its limits, such as `max_files` or `max_age`

`match`, `allow` and `deny` are lists of patterns. A list you write is added to the list the rule already has. The patterns already there stay.

`PATH-RED` uses `match`. That list already contains the workflows, the licence files, and the agent instruction files. This adds `scripts/release.sh`. The workflow pattern stays, and so does every other pattern already on the list.

```yaml
rules:
  PATH-RED:
    match: ['scripts/release\.sh']
```

To stop protecting one of those paths, name its pattern under `match_remove`. The text has to be a copy of the pattern already on the list. GitHub Actions workflows are on the list as `\.github/workflows/.*`, which is every file under `.github/workflows/`. Copy that pattern. The name of one workflow file, such as `ci.yml`, is not the list entry, so it does not remove anything.

```yaml
rules:
  PATH-RED:
    match_remove: ['\.github/workflows/.*']
```

After this, a change under `.github/workflows/` is no longer red from `PATH-RED`. The other CI patterns stay, including `\.github/actions/.*`, `\.gitlab-ci\.yml`, and `Jenkinsfile`.

`allow_remove` and `deny_remove` do the same thing for the `allow` and `deny` lists. A pattern that is not on the list is an error. A shorter or reworded pattern does not match the entry, and the old pattern stays.

`check-config` refuses the file in four cases: the rule id is unknown, a pattern is not a valid regular expression, a limit is outside its range, or a `*_remove` pattern is not on that list. It prints the problem and changes nothing.

Then run `replay` against a clone that already has the history. It treats each commit on one branch as a push, forwards nothing, and prints which rules would have fired. That is how you see whether a change is too strict before an agent pushes.

```bash
push-guard check-config --config /etc/warden
push-guard replay --config /etc/warden git-warden --git-dir ~/src/git-warden
```

[Configuring the rules](push-guard-configuring-rules.md) has the rest: examples of changes you are likely to want (reserving `main`, letting the agent rewrite its own branches, allowing images), how a pattern is matched against a whole ref or path, and how to read the replay output.

**Secret scanner.** gitleaks runs as `gitleaks git --log-opts "<new> --not <remote refs>"`. The range is the new commits, and not the commits the remote already has. A secret that was added and then removed again in the same push is still a hit.

The guard passes three flags so the pushed repository cannot change the scan:

- `--config` is the repo folder's `gitleaks.toml` when that file exists, otherwise the wall's `gitleaks.toml`. When neither exists, the guard writes a temporary config that extends the gitleaks defaults.
- `--gitleaks-ignore-path` is the repo folder's `gitleaksignore`, otherwise the wall's, otherwise an empty file.
- `--ignore-gitleaks-allow` makes an inline `gitleaks:allow` comment in the pushed repository do nothing.

Findings are redacted before they are shown or stored. The only way to run without gitleaks is to switch `CONTENT-SECRET` off, with `rules: {CONTENT-SECRET: {enabled: false}}`. That is a deliberate choice, for one repo or for the whole wall.

Check a configuration with:

```bash
push-guard check-config --config /etc/warden            # preflight, files, credentials
push-guard check-config --config /etc/warden --remote   # plus ls-remote per repo
```

## Running it

The agent reaches the wall through `serve`. A hook in the guard repository runs the rules. A green push is forwarded with the write credential.

This is the setup in [Using it](#using-it). Nothing on the wall is already listening for Git over HTTP, so `serve` is the listener. Start it and leave it running:

```bash
push-guard serve --config /etc/warden --listen 0.0.0.0:8418
```

The agent pushes to `http://hermit:$TOKEN@warden.internal:8418/git-warden.git`. `$TOKEN` is the password in `agent.token_file`. The user name is not checked; the log name is `agent.name`.

`serve` answers only Git's smart HTTP routes, the paths under `/<name>.git/`. It hands those requests to `git http-backend`.

The caller must send the password from `agent.token_file`. A green push is then forwarded with the write credential, and a fetch returns the repository the guard holds. Without the password, anyone who can open the listen port can push and fetch.

The repository does not have to exist beforehand. The first request for a configured repository creates `state_dir/repos/<name>.git` and installs the hook. Before every fetch or push, `serve` copies the remote's branches, tags and default branch into that repository. A fetch then shows the current remote, including a commit that landed without going through the guard. Other refs, such as notes, are not copied.

Put TLS in front of `serve` when the traffic leaves the host. The agent's password is in the request.

**Preflight.** `serve` refuses to start when any of these is true. It names every problem, and it serves nothing.

- `defaults.yaml` is missing or does not load.
- `git` is older than 2.42.
- No repo is configured.
- A repo has every rule disabled.
- A repo needs gitleaks for `CONTENT-SECRET`, and the binary is missing or is not version 8.x.

The hook repeats the checks that belong to one repo on every push. When one of them fails, the push is rejected with `internal error, try again later`. `check-config` reports the same problems and does not start the guard.

On every push the hook does four things:

1. It reads the remote's current refs.
2. It fetches any remote objects it does not yet have into the push's quarantine. The quarantine is discarded with the push if the push is rejected.
3. It runs every rule.
4. It then forwards the push (green), rejects it and returns the findings (yellow), or rejects it without the findings, keeps the commits, and warns a human (red).

The walk-through of a real repository is [Using it](#using-it).

## Commands

| Command | What it does |
| --- | --- |
| `push-guard serve --config DIR --listen ADDR` | The HTTP listener. Stays running. Creates each guard repository on the first request and copies the remote before every fetch or push. |
| `push-guard pre-receive --config DIR --repo NAME` | The hook; run by Git, not by hand. |
| `push-guard approve --config DIR <repo> <ref> <sha>` | Forwards the agent's next push of exactly that SHA without checking the rules again. The paragraphs under this table say when a prefix is enough, and what the lease is. |
| `push-guard reset-streak --config DIR <repo>` | Ends a `RATE-YELLOW-STREAK` without approving anything. |
| `push-guard check-config --config DIR [--remote]` | Preflight, files and credentials; `--remote` adds `ls-remote` per repo. |
| `push-guard replay --config DIR <repo> --git-dir PATH [--branch main] [--skip-scanner] [-v]` | Treats every first-parent step of a branch's history as one push (with the commit's committer time as the time of the push), forwards nothing, writes nothing, and prints the hits per rule. How to read it: [Configuring the rules](push-guard-configuring-rules.md#try-the-rules-on-existing-history). |
| `push-guard version` | Prints the version. |

`approve` forwards one commit you have already decided to allow. You pass the repo, the ref, and the SHA. A unique prefix of a rejected SHA is enough. A prefix that matches more than one rejected SHA approves nothing.

The next push of exactly that SHA to that ref is forwarded, and the rules are not run again. Any other SHA is checked from scratch.

If that push rewrites or deletes the ref, the approval carries a lease. The lease is the remote's tip at the time of the rejected push. The forward is refused when the remote tip has moved since then. An approval you type by hand, with no rejected push behind it, is leased against the id Git uses for a missing ref, so the ref must not exist on the remote yet.

An approval ends an active yellow streak. The log line names the rule ids of the push you overruled.

`approve`, `reset-streak`, and `replay` run on the wall. Being able to run them there is the authentication. Exit codes: `0` success, `1` failure, `2` usage error.

## State

- `state_dir/repos/<name>.git`: the guard repositories, with the hook installed.
- `state_dir/push.jsonl`: one JSON line per push. The line has the time, the push id, the agent, the repo, and the updates. Each update names the ref, the old tip, the new tip, and a kind: `create`, `delete`, `ff` (fast-forward), `non_ff`, `tag_move`, or `noop` when the remote was already at that commit. The line also has the findings, the verdict, whether the push was forwarded, and the message from the remote. Approvals, streak resets, and streak events are lines in the same file. The file is append-only. A line that is not valid JSON makes every push fail with `internal error, try again later` until a human repairs the file.
- `state_dir/pending/<repo>/<id>.bundle`: new commits of red pushes, kept for now.
- `state_dir/error.log`: errors that could not go into `push.jsonl`.

## Looking and restoring

The remote never received a rejected push. There is nothing to undo on the remote.

The commits of a red push are in `state_dir/pending/<repo>/<id>.bundle`. To look at them, make a scratch clone and run:

```bash
git fetch <bundle> 'refs/*:refs/pending/*'
```

Then approve the SHA, or leave the bundle where it is.

`push.jsonl` is the record of what was pushed, what was decided, and what was forwarded.

## Warnings

When `notify.command` is set in `defaults.yaml`, the guard starts that program and writes one JSON object to its standard input.

The facts come first: which agent, which repo, which refs, and which rules fired. The agent's own commit messages are only under `untrusted`. They are cut short, and they are marked as an unverified quote. They are not instructions to you or to the program that reads the JSON.

The `approve` field is the command to run on the wall. The steps around it are under [Resolve a red push](#6-resolve-a-red-push).

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
