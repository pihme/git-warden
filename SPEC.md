# Git Warden: SPEC

The design is in `docs/`:

- [docs/design.md](docs/design.md): the three guard posts (Push Guard, Merge Guard, Pull Guard), the shared decision core, the policy (`red` goes to a human, `yellow` is the agent's job) and the risk coverage per guard post.
- [docs/push-guard-rules.md](docs/push-guard-rules.md): the Push Guard's rules with their reasoning, verdicts, configuration, repos, layout on the wall, human actions, statistics.
- [docs/risks.md](docs/risks.md): the risk register R1–R22 the other documents refer to.

This file lists only what the implementation settled, added or changed relative to that design; where they differ, this file wins. Decisions here are settled; reopen them only with a reason (see `docs/agents/domain.md`).

## Layout and naming

- **Monorepo, one binary per guard post.** `cmd/push-guard` now; `cmd/merge-guard` later. The package name `git-warden` is taken on npm, PyPI and crates.io, so binaries are named per guard post. Each binary gets its own release tag (`push-guard/vX.Y.Z`).
- **Decision core is a library without platform knowledge:** `internal/rules` (delta normalisation, deterministic rules, verdict). Around it: `internal/config` (load and merge), `internal/gitx` (git as a subprocess with timeouts and credential handling), `internal/journal` (`push.jsonl`), `internal/pushguard` (hook, forwarding, approvals, rate limit, streak, serve, replay).
- **The Push Guard has no AI judge.** Rules only. The judge is used by the Merge Guard only.
- **No Pull Guard binary.** The Pull Guard is a scheduled, pinned [git-everref](https://github.com/daojyun/git-everref) run (bridge mode, all branches and tags) into an append-only backup repo; it decides nothing. Missing everref features are contributed upstream rather than rebuilt here. Tooling to browse and restore the backup may come here later.

## Configuration

- **Three layers:** built-in defaults compiled into the binary (`internal/config/defaults.yaml`), the wall's `defaults.yaml`, the repo's `repos/<name>/warden.yaml`. Scalars and limits override; `match`, `allow`, `deny` append; `match_remove`, `allow_remove`, `deny_remove` remove an exact entry of a lower layer, and an entry that isn't there is an error. Unknown keys and rule IDs are errors, so typos don't silently disable protection.
- **Deny semantics:** a rule fires for a subject if `deny` matches, or if it is triggered and no `allow` matches. Which subjects a rule looks at: `REF-NAMESPACE` every pushed ref, `PATH-*` every changed path; every other rule only the subjects its trigger hits. So `deny` on `REF-NAMESPACE` protects a branch on every push, while `deny` on `REF-DELETE` only keeps a deletion red despite a broader `allow`.
- **Subjects:** full ref name for `REF-*`; path for `PATH-*`, `MODE-*`, `CONTENT-*` and per-file `SIZE-*` limits; ref name for per-ref `SIZE-*` totals; branch name without `refs/heads/` for `META-*` (full ref name for tags). `REF-COUNT` and `RATE-*` have no subject; `allow`/`deny` don't apply.
- **`credential` is optional only for a local path or `file://` remote.** For SSH remotes it is a private key (`GIT_SSH_COMMAND` with `-i` and `IdentitiesOnly=yes`), for `https://` a token file read by an inline credential helper. The credential never appears in YAML, logs or command lines; only its path does.
- **SSH host keys:** optional per-repo `known_hosts` (SSH remotes only), otherwise the guard user's own (with a per-repo file the system-wide known_hosts is ignored too); `StrictHostKeyChecking=yes` and `BatchMode=yes`, and `ssh` runs with `-F none`, so no ssh_config on the wall host can change where or how the guard connects.
- **Wall-only settings:** `agent` and `state_dir` may not appear in a repo file; `remote`, `credential`, `default_branch` may not appear in a defaults file.

## Hook and forwarding

- **Inputs:** the remote's refs come from a fresh `git ls-remote --symref` per push. Remote objects the guard lacks are fetched by oid (`git fetch --no-write-fetch-head <remote> <oid>…`) into the push's quarantine; no ref is updated. If they are still missing, the push fails closed.
- **Ref kinds** are computed against the remote's old state; annotated tags are peeled to commits. Cumulative diff base: the remote's old commit for `ff`; the merge base for `non_ff` and `tag_move`; `merge-base(remote default branch, new)` for a new ref; otherwise the empty tree. New commits: `rev-list <new> --not <remote heads and tags>`.
- **Forwarding:** `git push --porcelain [--atomic] <remote> <sha>:<ref> …`, never `--force`. `--force-with-lease=<ref>:<remote old>` (and `:<ref>` for a delete) is used only when `REF-NON-FF`, `REF-TAG-MOVE` or `REF-DELETE` was triggered and explicitly exempted by `allow` for that ref, or when a human approved exactly that SHA. Without a lease the remote's own non-fast-forward check applies. The quarantine variable is dropped for every command that talks to the remote, because a local remote's `receive-pack` would otherwise refuse the update.
- **Pending store as bundles:** the design's original `refs/warden/pending/<id>/*` can't work: refs can't be written inside the quarantine, and the quarantined objects vanish when the hook rejects. A red push's new commits are written to `state_dir/pending/<repo>/<id>.bundle` instead (built in a temporary repository that borrows the quarantined objects).
- **Approvals** are per (ref, SHA) and used up by the forwarded push that carries them. In a push that mixes approved and other updates, only the others are checked. An approval also ends an active yellow streak.
- **Yellow streak:** yellow rejections are counted per repo since the last `approve` or `reset-streak` and within the window. The push that would exceed `max_yellow` becomes red and logs a `streak` event; while a `streak` event is newer than the last approve or reset, every push to the repo is red.
- **Rate limit:** a rate-limited push isn't evaluated, answers `rate limited, try later`, and only the first of a series warns the human.
- **Fail-closed:** any error answers `internal error, try again later`, is logged in `push.jsonl` with its detail, and is not warned as a violation. A malformed `push.jsonl` line fails every push closed. gitleaks exits 0 even when its git call fails, so its log is checked for errors too.
- **Unknown repo:** red with the message `rejected: unknown repo` (the only red answer that names a reason besides the rate limit).

## Rules

- **`META-UNSIGNED` checks presence only:** a `gpgsig`/`gpgsig-sha256` header. The wall has no keyring, so signatures aren't verified. "The last `lookback` commits on that branch" are first-parent commits of the remote's old state (the default branch for a new ref or a tag); with fewer than `lookback` commits the rule stays quiet.
- **`CONTENT-BINARY`** also fires for added text that isn't valid UTF-8 (the design's "undecodable text counts as binary").
- **`CONTENT-PAGES-SCRIPT`:** a host counts as new if `git grep -i -F <host>` finds it nowhere in the remote's old tree of the Pages branch.
- **`SIZE-MASS-DELETE`** share check applies to modified and renamed files; deleted files count toward `max_deleted_files`.

## Human commands

`push-guard approve <repo> <ref> <sha>` and `push-guard reset-streak <repo>` (the design's `warden approve` / `warden reset-streak`), plus `check-config`, `init-repo`, `replay` and `version`. All take `--config DIR`.

## Serve

`push-guard serve` wraps `git http-backend` (net/http/cgi), accepts only the smart HTTP routes of `/<name>.git`, authenticates with HTTP basic auth against `agent.token_file` (constant-time compare), creates guard repositories on demand with `http.receivepack=true`, `receive.fsckObjects=true` and the hook installed (the hook script embeds the absolute binary path, the config directory and the repo name), and syncs them from the remote before every ref advertisement, so agents fetch through the guard too.
