# Configuring Push Guard rules

How to change what the Push Guard rejects, and how to try that change on history you already have before an agent pushes. The walk-through that stands the guard up is [push-guard.md](push-guard.md#using-it). What each rule is for: [push-guard-rules.md](push-guard-rules.md). The default color and trigger of every rule: [the rules table](push-guard.md#rules).

Changes live on the wall, in the configuration directory. A file inside the repository cannot loosen a rule.

## Where a change goes

Two files, applied in order on top of the built-in defaults:

| File | Applies to |
| --- | --- |
| `defaults.yaml` | Every repo of this Push Guard. |
| `repos/<name>/warden.yaml` | That one repo. The folder name is the name the agent pushes to. |

A key the repo file does not mention keeps the value from the layer below. A number, a duration, `enabled` and `color` replace the value from below.

`match`, `allow` and `deny` are lists of patterns. A list you write is added to the list from the layer below. The patterns already there stay.

`PATH-RED` uses `match`. That list already contains the workflows, the licence files, and the agent instruction files. This repo file adds `scripts/release.sh`. The workflow pattern stays, and so does every other pattern already on the list.

```yaml
# /etc/warden/repos/git-warden/warden.yaml
remote: git@github.com:pihme/git-warden.git
credential: /etc/warden/keys/git-warden
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

`allow_remove` and `deny_remove` do the same thing for the `allow` and `deny` lists. A pattern that is not on the list is an error, so a typo cannot silently fail to remove anything. A shorter or reworded pattern does not match the entry, and the old pattern stays.

Unknown keys and unknown rule ids are errors. A color other than `red` or `yellow` is an error. A limit outside its range is an error; the ranges are in [Limit bounds](push-guard-rules.md#limit-bounds). `check-config` reports all of these and changes nothing:

```bash
push-guard check-config --config /etc/warden
```

A repository in which every rule is turned off is refused. Leave at least one rule enabled.

## What you can set on a rule

```yaml
rules:
  MODE-SYMLINK:
    enabled: true          # false: the rule never fires
    color: yellow          # red: a human decides. yellow: the agent gets the reason
    allow: ['docs/.*']     # the rule does not fire for a matching subject
    deny: ['vendor/.*']    # the rule fires for a matching subject even when allow matches
    # limits, only the ones this rule has, for example:
    # max_files: 500
```

`red` rejects the push with no reason and stores it for a human. `yellow` rejects it and tells the agent the rule, the ref or path, and why. The agent is expected to fix a yellow rejection and push again.

`allow` and `deny` are regular expressions over the rule's subject. `deny` wins when both match.

Which string is the subject:

| Rules | Subject |
| --- | --- |
| `REF-DELETE`, `REF-NON-FF`, `REF-TAG-MOVE`, `REF-TAG-NEW`, `REF-NAMESPACE` | The full ref, such as `refs/heads/main` |
| `PATH-RED`, `PATH-YELLOW` | The path, with no leading slash. A rename is checked under both names |
| `MODE-EXEC`, `MODE-SYMLINK`, `MODE-SUBMODULE`, `CONTENT-*`, and a per-file `SIZE-*` limit | The path |
| `META-UNSIGNED`, `META-BACKDATED`, `META-FUTURE` | The branch name |
| `REF-COUNT`, `RATE-LIMIT`, `RATE-YELLOW-STREAK` | None. `allow` and `deny` do not apply |

`PATH-RED` and `PATH-YELLOW` also have `match`: the path list those rules look at. A changed path that matches `match` is a hit, unless `allow` matches it. `deny` on those two rules keeps the path a hit even when `allow` matches. Adding a line to `match` protects one more path. It does not replace the built-in list.

The guard anchors every pattern, so the pattern has to describe the whole subject. `agent/.*` does not match `refs/heads/agent/fix-typo`. `refs/heads/agent/.*` does. `package-lock\.json` matches that file at the root of the tree only. `(.*/)?package-lock\.json` matches it in any directory. `.` matches a slash, so `docs/.*\.png` matches `docs/img/logo.png`.

Two kinds of rule use `deny` differently.

- `REF-NAMESPACE` and `PATH-*` look at every ref or every changed path. A `deny` there fires on its own. `deny: ['refs/heads/main']` on `REF-NAMESPACE` makes every push that includes `main` red, including an ordinary fast-forward. The agent is told that pushes to that ref are reserved for a human.
- The other rules fire only when their own event happens: a delete, a file that is not a fast-forward, a new binary, a commit date. On those, `deny` beats `allow` for a subject the event already hit. It does not create the event. A `deny` on `REF-NON-FF` does not reserve a branch. A normal fast-forward never asks that rule.

## Try the rules on existing history

`replay` loads the same merged configuration a push would use, walks one branch, and prints what the rules would have done. It forwards nothing, writes nothing to `push.jsonl`, and starts no warning program. Run it as often as you like.

```bash
push-guard replay --config /etc/warden git-warden --git-dir ~/src/git-warden
push-guard replay --config /etc/warden git-warden --git-dir ~/src/git-warden --branch agent/fix-typo -v
```

`--git-dir` is a local clone that already contains the branch. The command does not contact the remote. `--branch` defaults to `main`. `-v` prints every finding; without it, a hit is one line of rule ids. `--skip-scanner` leaves gitleaks out, which is useful while you are moving path patterns and do not want a secret scan on every step.

The walk is first-parent, oldest commit first. Each commit is treated as a push of that one branch onto the commit before it. The time of the pretend push is that commit's committer time. Green steps are counted and not printed. Any other step is printed as twelve characters of the SHA, the color, and the subject:

```
3f2a9c1e4b8d red    add workflow
    PATH-RED
a11b22c33d4e yellow add deps
    PATH-YELLOW

3 steps: 1 green, 1 yellow, 1 red
rule                    steps    red yellow
PATH-RED                    1      1      0
PATH-YELLOW                 1      0      1
```

`steps` is how many commits made that rule fire. `red` and `yellow` count findings.

Read it in this order:

1. `check-config` until it reports no problem.
2. Replay the branch the agent will actually push, for example `agent/fix-typo`. A replay of `main` after you have denied `refs/heads/main` is red on every step. That is the reservation working. It hides every other rule on that branch.
3. Look at the table. A rule with a high red count on ordinary history is a candidate for an `allow`, a higher limit, `color: yellow`, or `enabled: false`.
4. Change one thing, run `check-config`, replay again.
5. Start or restart nothing until the replay of the agent's kind of branch is mostly green, and the replay of a protected branch is red for the reason you wanted.

`replay` does not show everything a live push can hit.

- `RATE-LIMIT` and `RATE-YELLOW-STREAK` are not replayed. They count real pushes and real yellow rejections.
- Only one branch is walked, and only its first parent. A merge on `main` appears as the merge commit, not as the commits on the side branch. Replay the side branch itself to see those.
- Tags, deletions, and a push of several refs at once are not in the walk. Each step is a fast-forward of the one branch.
- The pretend push happens at the commit's committer time. A commit that is old compared with the moment it was really pushed, or a committer date in the future, does not show up. A committer date earlier than a parent's still does.

After the guard is in use, `state_dir/push.jsonl` counts hits, approvals, and yellow rejections the agent fixed. A rule you keep approving is the same kind of candidate as a rule that dominates a replay. Changing it is still a human's edit of these files.

## Cookbook

Each snippet is the `rules:` block of one repo's `warden.yaml`, unless the note says to put it in `defaults.yaml`. Patterns are copied exactly when a later recipe removes them.

### Reserve a branch for a human

Puts `main`, and any branch under `release/`, behind a human. An ordinary fast-forward is red too. This is the example in [examples/warden/repos/git-warden/warden.yaml](../examples/warden/repos/git-warden/warden.yaml), plus the release branches.

```yaml
rules:
  REF-NAMESPACE:
    deny:
      - 'refs/heads/main'
      - 'refs/heads/release/.*'
```

Replay `main` and expect every step red, from `REF-NAMESPACE`. Replay `agent/fix-typo` to see the other rules.

### Let the agent delete and rewrite its own branches

The built-in `REF-DELETE` and `REF-NON-FF` are red for every branch. This allow is what [examples/warden/defaults.yaml](../examples/warden/defaults.yaml) ships for deletes. The same pattern on `REF-NON-FF` lets the agent force-push those branches. `main` still cannot be deleted or rewritten, and the reservation above still rejects a normal push to `main`.

```yaml
rules:
  REF-DELETE:
    allow: ['refs/heads/agent/.*']
  REF-NON-FF:
    allow: ['refs/heads/agent/.*']
```

Put this in `defaults.yaml` when every repo of this guard uses an `agent/` prefix. Put it in one `warden.yaml` when only that repo does.

### Let the agent edit its own instruction file

`AGENTS.md`, `CLAUDE.md` and `GEMINI.md` are on the built-in `PATH-RED` list, in any directory, in any case. A repo where the agent is supposed to maintain `AGENTS.md` moves that one pattern to yellow. `match_remove` has to repeat the built-in pattern exactly.

```yaml
rules:
  PATH-RED:
    match_remove:
      - '(?i)(.*/)?(AGENTS|CLAUDE|GEMINI)\.md'
  PATH-YELLOW:
    match:
      - '(?i)(.*/)?(AGENTS|CLAUDE|GEMINI)\.md'
```

Leaving the pattern off both lists makes those files green. Yellow keeps the push visible to the agent, which can then fix anything else the file change dragged in.

### Allow images in the docs

`CONTENT-BINARY` is yellow for every new or changed binary, including pictures. This is the allow in the git-warden example. It covers `docs/guide.png` and `docs/img/guide.png`. A binary anywhere else still fires.

```yaml
rules:
  CONTENT-BINARY:
    allow: ['docs/.*\.(png|svg)']
```

### Allow lockfile updates

Dependency manifests are on the built-in `PATH-YELLOW` list. An allow exempts one path. The pattern has to cover the whole path, in any directory.

```yaml
rules:
  PATH-YELLOW:
    allow:
      - '(.*/)?package-lock\.json'
      - '(.*/)?go\.sum'
```

The manifest stays yellow. Only the lockfile is exempt. Replay a branch that updated a lockfile and confirm `PATH-YELLOW` no longer lists that step.

### Let snapshot tags move

Moving an existing tag is red. New tags are yellow, so a release tag still asks the agent to notice. Snapshot tags may move.

```yaml
rules:
  REF-TAG-MOVE:
    allow: ['refs/tags/.*-SNAPSHOT']
```

### This repository does not sign commits

`META-UNSIGNED` is yellow when a new commit is unsigned and the last 20 commits on the branch were signed. On a repository where nobody signs, it fires once a stretch of signed commits exists, which is noise. Turn it off for that repo.

```yaml
rules:
  META-UNSIGNED:
    enabled: false
```

`lookback: 0` also switches the history check off, and leaves the rule enabled. `enabled: false` is the clearer statement.

### No symlinks in this repository

The built-in color is yellow. Red means a symlink waits for a human instead of being handed back to the agent.

```yaml
rules:
  MODE-SYMLINK:
    color: red
```

### This repository has no Pages site

`CONTENT-PAGES-SCRIPT` looks at `pages_branch` (`gh-pages` unless you set it). An empty value turns that rule off. There is no fallback to `gh-pages`. The key sits next to `rules`, not under it.

```yaml
pages_branch: ''
```

Set this in `defaults.yaml` only when none of the repos publish a Pages branch.

### One more path is red here

`match` adds to the built-in blocklist. This makes `scripts/release.sh` red in this repo, on top of the paths that are already red.

```yaml
rules:
  PATH-RED:
    match: ['scripts/release\.sh']
```

### A secret-scanner finding that is a false positive

The scanner does not read a `.gitleaks.toml` or a `.gitleaksignore` from the repository. An agent cannot push a secret together with an exception for it. Add the exception on the wall, in `repos/<name>/gitleaksignore`, or in that folder's `gitleaks.toml` (start it with `[extend] path = "/etc/warden/gitleaks.toml"` so the shared rules stay). Details of the flags: [Scanner configuration](push-guard-rules.md#scanner-configuration).

Turning the rule off is a separate, deliberate choice, per repo or for the whole wall:

```yaml
rules:
  CONTENT-SECRET:
    enabled: false
```

`serve` then no longer requires gitleaks for that repo. Replay with `--skip-scanner` skips the same rule for one run and leaves the configuration unchanged.

### A branch that is large on purpose

`SIZE-LARGE` is yellow above 200 files, 5,000 lines, 100 commits, or a 1 MB file. A repository that lands big generated branches can raise the commit count for that repo. Stay inside the [range](push-guard-rules.md#limit-bounds) (1 to 10,000,000). `SIZE-LIMIT` is the red ceiling and is unchanged by this.

```yaml
rules:
  SIZE-LARGE:
    max_commits: 500
```

Replay the large branch. The summary's `SIZE-LARGE` row is the check that the new limit is high enough, and that the other rules on that branch still say what you want.
