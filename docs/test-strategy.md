# Test strategy

How Git Warden (`push-guard` and `backup-guard`) is tested, and why. The details per requirement are in the [Push Guard test specification](push-guard-testspec.md); decisions that came out of testing are in [Clarifications during tests](push-guard-testspec.md#clarifications-during-tests).

## Goals

- Every numbered requirement (`REQ-PG-…` in [push-guard-rules.md](push-guard-rules.md)) has at least one test named after it (`TestREQ_PG_<nnn>_…`). Tests are written against the spec, not against the code: a test that fails because the code does not meet the spec stays red until the code is fixed or the spec is changed.
- The guards fail closed. For every error path that matters, a test checks that the push is rejected (or the backup run fails), never that it slips through.
- Statement coverage is a signal, not the target. Mutation testing shows whether the tests actually check what the covered code does.

## Layers

| Layer | Where | What it checks | Runs in CI |
| --- | --- | --- | --- |
| Unit | `internal/rules`, `internal/config`, `internal/journal`, `internal/gitx`, `internal/pushguard`, `internal/backupguard` | One rule, config value or journal query at a time, with real git repos in temp directories where needed | yes, `go test ./...` |
| End to end | `cmd/push-guard/*_test.go`, `cmd/backup-guard/main_test.go` | The built binary between an agent repo and a remote (local, SSH via `sshd`), including approve, reset and notify | yes, `go test ./...` |
| Live | `cmd/push-guard/live_test.go` (`TestLiveRemote`) | A real push to this repo on GitHub with a token | yes, separate `live` job; skipped locally unless `WARDEN_LIVE_REMOTE` and `WARDEN_LIVE_TOKEN_FILE` are set |
| Mutation | `make mutate` (local / QA only) | Whether the unit tests notice small changes to the code | **no**, run by hand |
| Fuzzing and property tests | `//go:build fuzz` files under `internal/rules`, `internal/journal`, `internal/config` | Inputs that once produced special cases | yes, modest (`go test -tags=fuzz` on config/journal/rules with `-rapid.checks=100` in the `fuzz` CI job); long fuzzing (`make fuzz FUZZTIME=…`) only by hand |

Coverage for `internal/pushguard` should be read with the end-to-end tests included (build the test binary with `-cover` and use `GOCOVERDIR`), because most of that package is only reached through the binary. How to measure it is in the test specification.

## Mutation testing

- **Tool:** [gomutants](https://github.com/szhekpisov/gomutants) **v0.6.1**. Decided after comparing alternatives on this repo:
  - **Gremlins** was dropped: it treats conditions in `case` lines as not covered and skips them; no config fixes that.
  - **go-mutesting** (avito fork) works but runs strictly one mutant at a time (~2 h for `internal/rules` alone on this box).
  - **gomutants** parallelizes, routes each mutant to covering unit tests only, and keeps the working tree clean via `go test -overlay`.
- **Scope (default):** `./internal/rules`, `./internal/journal`, `./internal/config`. Each mutant runs only the tests of its own package; end-to-end tests are not run per mutant (about 13 s each). Backup Guard is not in the default set yet; QA can select it with `make mutate PKGS='./internal/backupguard'` (or `PACKAGES=…`).
- **Command:** `make mutate` on main (not CI). Defaults and exclusions live in [`.gomutants.yml`](../.gomutants.yml); `scripts/mutate.sh` / Make vars override for a single run. The script runs under `nice -n 10`.
- **Useful variables:**

  | Variable | Default | Meaning |
  | --- | --- | --- |
  | `WORKERS` | `2` | Parallel mutant workers (shared box; keep low) |
  | `TEST_CPU` | `1` | Passed to `go test -cpu` per mutant (`0` omits) |
  | `TIMEOUT_COEFFICIENT` | `10` | Global timeout ceiling = baseline × this |
  | `TIMEOUT_MIN` | `2s` | Floor for adaptive per-mutant timeout |
  | `TIMEOUT_MARGIN` | `3` | Adaptive multiplier on selected test durations |
  | `ADAPTIVE_TIMEOUT` | `1` | `0` = single global ceiling only |
  | `PACKAGES` / `PKGS` | rules + journal + config | Space-separated package patterns (`PKGS` aliases `PACKAGES`) |
  | `DRY_RUN` | `0` | `1` = list mutants without testing |
  | `OUTPUT` | `mutation-report.json` | JSON report path (gitignored) |

  Examples: `make mutate PACKAGES='./internal/config'`, `make mutate PKGS='./internal/backupguard' WORKERS=2`.
- **Goal:** no surviving mutant in the scoped packages except documented equivalent mutants below. A survivor that the end-to-end tests already catch still gets a unit test in its package, so the mutation run stays meaningful on its own.
- **Workflow:** QA runs the tool and lists the survivors with file, line and mutation. The maintainer (or QA) adds unit tests until they are killed. A mutant that cannot change behaviour (an equivalent mutant) is listed with location and a one-line reason under Equivalent mutants below instead of being tested. Re-run per package (`make mutate PACKAGES='./internal/…'`) to verify kills.

### Tuning and exclusions

Every exclusion needs a reason; do not use exclusions to hide real survivors.

| Setting | Value | Reason |
| --- | --- | --- |
| `workers: 2`, `test-cpu: 1` | shared-box defaults | Keeps load low; never more than one mutation campaign at a time |
| `adaptive-timeout: true`, `timeout-min: 2s`, `timeout-margin: 3`, `timeout-coefficient: 10` | gomutants defaults | Adaptive deadlines avoid false `TIMED OUT` without inflating waits |
| `exclude-calls-defaults: true` | built-in `log.Print*`, `slog.*` | Pure log text is not asserted; mutating it cannot be killed honestly |
| `exclude-calls: [fmt.Sprintf]` | extends built-ins | `RETURN_ZERO` / argument mutants inside `fmt.Sprintf` only change message wording with no branching; pinning exact strings would freeze copy without measuring logic |
| `exclude-files` | empty | No generated production sources in the scoped packages today |

Do **not** disable `ERRORF_WRAP` (`%w` → `%v`): that breaks `errors.Is` / `errors.As` and is a real behavioural change when callers unwrap.

### Equivalent mutants

Survivors that remain after targeted tests and cannot change observable behaviour, with file:line (as of the commit that added the entry), gomutants mutator and a one-line reason. Re-check the list when the code moves.

#### internal/config

| Location | Mutator | Why equivalent |
|---|---|---|
| config.go:45 `rank` `2`→`3` | INTEGER_INCREMENT | rank is only compared in `Worse`; the order red > yellow > green is kept. |
| config.go:49 `rank` `0`→`-1` | INTEGER_DECREMENT | Same: only the relative order of ranks matters. |
| config.go:160 `x < float64(MinInt64)`→`<=` | CONDITIONALS_BOUNDARY | Only -2^63 changes branch, and it is rejected either way (out of range vs. must not be negative). |
| config.go:370 `indexOf` `return -1`→`-2` | INTEGER_INCREMENT | Callers only test `< 0`. |
| config.go:410, :413 `if err != nil` on the embedded defaults | BRANCH_IF | The embedded defaults.yaml always parses and applies (TestBuiltinDefaults); the error is unreachable. |
| config.go:472 `m.timeout = "60s"` | BRANCH_IF | The embedded defaults set `timeout: 60s`, so `m.timeout` is never empty here. |
| config.go:552 `p == ""`→false in ResolveProgram | EXPRESSION_REMOVE | For `""`, `filepath.Base("") == "."` sends it to `resolve(dir, "")`, which returns `""` too. |
| config.go:610 `case`/`return KindLocal` and :620 `KindLocal`→zero value | BRANCH_CASE, RETURN_ZERO | `KindLocal` is the zero value of `Kind`, and the emptied case falls through to the same return. |

#### internal/journal

| Location | Mutator | Why equivalent |
|---|---|---|
| journal.go:83 `json.Marshal` error | BRANCH_IF | An `Entry` has only strings, ints, bools, times and slices of those; Marshal cannot fail. |
| journal.go:86 `MkdirAll` error | BRANCH_IF | If MkdirAll fails, the OpenFile below fails on the same path with the same error. |
| journal.go:94 `Flock` error | BRANCH_IF | `flock(LOCK_EX)` on a freshly opened regular file (or char device) has no reachable failure (EINTR is retried by the runtime). |
| journal.go:116 initial buffer `64*1024` (5 mutants: 63, 65, `/`, 1023, 1025) | INTEGER_*/ARITHMETIC_BASE | Only the starting allocation changes; the 64 MiB maximum (tested) is untouched and bufio grows the buffer. |
| journal.go:136 `i >= 0`→`> 0`, `0`→`1` in lastEnd | CONDITIONALS_BOUNDARY, INTEGER_INCREMENT | Both callers scan from the returned index + 1; when entry 0 is the approve/reset, returning -1 also scans entry 0, which is not a streak or push and never counts. |
| journal.go:212 `continue`→`break` | INVERT_LOOP_CTRL | Inside a `switch` that is the last statement of the loop body, `break` leaves the switch and the iteration ends just as with `continue`. |
| journal.go:231 `i >= 0`→`i >= -1` in ApprovalLease | INTEGER_DECREMENT | OpenApproval just confirmed an approve entry of (ref, sha), so the loop returns before reaching index -1. |

#### internal/rules

| Location | Mutator | Why equivalent |
|---|---|---|
| rules.go:104 `a.Ref < b.Ref`→`<=` | CONDITIONALS_BOUNDARY | Only reached when the refs differ, so `<=` and `<` agree. |
| rules.go:254 `break`→`continue` (invalid UTF-8 scan) | INVERT_LOOP_CTRL | Later invalid lines set the same `binary` and message again. |
| rules.go:330 `PagesBranch == ""` return | BRANCH_IF | Without it the Pages ref is `refs/heads/`, which is not a valid ref name and never in a push. |
| rules.go:343 `FindAllStringSubmatch(…, -1)`→`-2` | INTEGER_INCREMENT | Any negative n means "all matches". |
| rules.go:359 `known[host] = seen` | STATEMENT_REMOVE | Cache only: the host is looked up again with the same result. |
| rules.go:383 hostInTree `return false, err`→`true, err` | RETURN_TRUE | The caller returns on the error and drops the bool. |
| rules.go:404 (RANGE_BREAK), :405, :432 `parentTimes[…] = …` | RANGE_BREAK, STATEMENT_REMOVE | Cache of committer times only; a missing entry is read from Git with the same value. |
| rules.go:457 `rd.Kind == TagMove`→false, `rd.IsTag()`→false, `\|\|`→`&&` | EXPRESSION_REMOVE, INVERT_LOGICAL | TagMove only occurs for tags, and a tag with commits and an old commit is always a TagMove, so each operand alone decides the same. |
| rules.go:459 `e.d.DefaultBranch == ""`→false | EXPRESSION_REMOVE | With an empty default branch, `Remote[""]` is never present, so `!ok` already returns. |
| rules.go:463 peel error ignored; :464, :469 `return false, err`→`true, err` | BRANCH_IF, RETURN_TRUE | Ignoring the peel error leaves `start` empty and `rev-list ""` fails; on errors the caller drops the bool. |
| rules.go:576 `len(oid) > 12`→`>= 12`, `12`→`11` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | For a 12-character oid, `oid[:12]` is the oid. |
| delta.go:218 isAncestor `return false, err`→`true, err` | RETURN_TRUE | The caller returns on the error. |
| delta.go:241 `d.DefaultBranch != ""`→true | EXPRESSION_REMOVE | `Remote[""]` is never present, so `ok` already implies a non-empty name. |
| delta.go:282–283 append of the annotated tag object (6 mutants) | EXPRESSION_REMOVE, INVERT_LOGICAL, CONDITIONALS_NEGATION, BRANCH_IF, STATEMENT_REMOVE | `rev-list --objects` already lists a tag object given as input, and `check` sums a map keyed by oid, so adding or dropping the extra id (or the commit for a lightweight tag) changes nothing. |
| delta.go:309 `if !ok { continue }` | BRANCH_IF | Without it a missing numstat entry assigns zero values to fields that are already zero. |
| delta.go:422 initial scanner buffer `64*1024` (5 mutants) | INTEGER_*, ARITHMETIC_BASE | Starting allocation only; bufio grows it up to the maximum. |
| delta.go:423 `no := 0`→`1`/`-1` | INTEGER_* | Every added line follows a hunk header, which sets `no` first. |
| delta.go:456 `j >= 0`→`> 0`, `0`→`1` in hunkStart | CONDITIONALS_BOUNDARY, INTEGER_INCREMENT | j = 0 means s starts with `,` or space; Atoi of `""` or of that string both give 0. |
| delta.go:484 `return sizes, nil`→`nil, nil` for no oids | RETURN_ZERO | Callers only read from or range over the map; a nil map behaves the same. |
| delta.go:511 `f.Status != 'D'`→true, `!gitx.IsZero(f.NewBlob)`→true | EXPRESSION_REMOVE | Exactly the deleted files have a zero new blob, so either check alone excludes them. |
| delta.go:528 totalSize `return 0, err`→`±1, err` | INTEGER_* | Normalize returns the error and drops the delta. |
| delta.go:539 `len(oids) == 0` shortcut (2 mutants) | INTEGER_DECREMENT, BRANCH_IF | `cat-file --batch` with an empty line prints ` missing` and the read loop runs zero times. |
| delta.go:587, :593 ParseCommit `return c, err`→zero commit | RETURN_ZERO | Both callers (batch.commits, pushguard replay) drop the commit on error. |
| secret.go:50 generated config temp file error | BRANCH_IF | The ignore file is created next in the same temp dir and fails with the same error. |
| secret.go:113 `report.Close()` | STATEMENT_REMOVE | Leaks one file descriptor; gitleaks writes the report by path. |
| secret.go:182 refOf `len(e.d.Refs) > 0`→`>= 0`/`> -1` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | refOf runs only for leaks, which need new commits, so there is always at least one ref. |
| secret.go:205 firstLine `i >= 0`→`> 0`, `0`→`1` | CONDITIONALS_BOUNDARY, INTEGER_INCREMENT | s is trimmed first, so it never starts with a newline. |
| secret.go:208 `len(s) > 300`→`>= 300`, `300`→`299` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | The cut is `s[:300]`, which is a no-op for a 300-byte string. |

Not equivalent but not killed in rules (deliberately):

- delta.go:422 maximum token size `256*1024*1024` ±1 in any factor (8 mutants), :443 `sc.Err()` branch and :444 (not covered): killing them needs a single diff line of about 256 MiB; too heavy for the shared box and CI.
- secret.go:73 temp file `WriteString` error: needs a write failure on a freshly created temp file (disk full or fault injection).
- rules.go:475–476 `b.commits` error after `rev-list` succeeded: needs an object that disappears or is corrupt between two git calls.
- delta.go:320 (INFRA ERROR) parseRaw `i++`→`i--`, and the timeouts at rules.go:300 and delta.go:358/384: infinite loops; a timeout counts as detected.

#### internal/backupguard

| Location | Mutator | Why equivalent |
|---|---|---|
| bridge.go:52 `MkdirAll(base)` error | BRANCH_IF | removeSetupLeftovers reads the same directory next and fails with the same error. |
| bridge.go:67 `git init` error of the temporary bridge | BRANCH_IF | `remote add` then runs in a directory that is not a repository and fails. |
| bridge.go:83 `remote add backup` error | BRANCH_IF | checkRemoteURL of `backup` runs next and fails on the same missing remote. |
| bridge.go:94 `[2]string`→`[3]string` | INTEGER_INCREMENT | Only the array type changes; the third element is empty and never read. |
| bridge.go:193 `b != key`→true | EXPRESSION_REMOVE | The config regexp anchors `everref.remotes/origin/`, so the prefix is always trimmed and `b` never equals `key`. |
| bridge.go:274 `MkdirAll(locks)` error, :333 `MkdirAll(state_dir)` error | BRANCH_IF | OpenFile on a path inside the same directory runs next and fails with the same error. |
| bridge.go:282, :290, :345 `f.Close()` | STATEMENT_REMOVE | Leaks a file descriptor only (the lock is released by `LOCK_UN` or the failed Flock). |
| bridge.go:315, :337 `json.Marshal` error | BRANCH_IF | Warning and Result hold only strings, ints, bools, a time and a string map; Marshal cannot fail. |
| bridge.go:348 `return f.Close()`→`nil` | RETURN_ERROR_NIL | Close of a regular file after a successful write does not fail on local file systems. |
| bridge.go:362 `i >= 0`→true, `0`→`-1` in tail | EXPRESSION_REMOVE, INTEGER_DECREMENT | For i = -1 the condition is `cut < len(s)` and `cut += 0`: no change. |
| check.go:25 `SplitN(…, 2)`→`3` | INTEGER_INCREMENT | Only element 0 (the major version) is used. |
| check.go:35, :73 LoadRepo / newRemote error | BRANCH_IF | Preflight has just loaded every repo with the same checks, so these errors are unreachable. |
| config.go:169 `if !e.IsDir() { continue }` | BRANCH_IF | For a file, the Stat of `<file>/backup.yaml` fails and the entry is skipped anyway. |
| config.go:206, run.go:90 `%w`→`%v` | ERRORF_WRAP | The wrapped errors are plain `errors.New`/`fmt.Errorf` values with nothing to unwrap to. |
| everref.go:134 `len(lines) > n`→`>=` | CONDITIONALS_BOUNDARY | For len = n, `lines[0:]` is the whole slice. |
| run.go:130 `return …, d, nil`→`nil` | RETURN_ZERO | PreflightRun only uses the Defaults when the preflight failed. |

Not equivalent but not killed in internal/backupguard (deliberately):

- bridge.go:71, :91, :95, :100, :186, run.go:212, :245 (BRANCH_IF), plus the 16 not-covered mutants in the same error branches (bridge.go:53, :68, :72, :84, :92, :96, :101, :187, :286, :316, :338; check.go:36–37, :74; run.go:213, :246): git config or remote commands that fail in a healthy bridge, Flock errors other than EWOULDBLOCK, and NewRemote errors that LoadRepo already ruled out. They need fault injection between two git calls.
- Timing constants: everref.go:40 (30 s), everref.go:43, :114 and bridge.go:324 WaitDelay 5 s ±1. The WaitDelay itself is tested (a child that keeps the pipe open), but not its exact length.
- run.go:206 `err != nil`→true: only differs if a run succeeds and the deadline passes before the deferred check runs. That's a race, not testable reliably.
- Timeouts at bridge.go:213 (2), :216, :360 and :374: infinite loops; a timeout counts as detected.

Test fakes for git-everref refuse an empty `-C` directory: mutants that lose the bridge path would otherwise run git in the package directory and write `everref.*` keys into the repository's own `.git/config`.

#### cmd/backup-guard

No equivalent mutants remain (98.82% efficacy before d621f98, which killed the last survivor, main.go:45 `fs.SetOutput`). Not killed:

- main.go:33 `os.Exit(run(…))` (3 mutants, not covered): `main` exits the process; the tests call `run` instead.
- main.go:99 `break`→`continue` and main.go:102 `args = fs.Args()[1:]` (4 mutants): infinite flag-parsing loops; the timeouts count as detected.

Mutants that removed a loop's `i++`/`i--` time out (infinite loop); gomutants counts them as detected.

## Fuzzing and property tests

Only where testing already found special cases, not as a general net. All of them sit behind the `fuzz` build tag (`//go:build fuzz`) so ordinary `go test ./...` stays fast and free of the test-only `rapid` dependency.

- **Go fuzzing (`go test -tags=fuzz -fuzz=…`):** parsing of diff headers and `git diff --numstat -z` / `--raw -z` output (paths with spaces, TABs, renames, C-quoting). Seeds run with every `go test -tags=fuzz`; long runs use `make fuzz FUZZTIME=5s` (or any duration).
- **`rapid` property tests:** an ambiguous SHA prefix never approves anything; a limit of the wrong kind never passes `check-config`; a count outside int64 never loads as a wrapped negative. `rapid` (`pgregory.net/rapid`) is test-only and does not end up in the binaries (not listed in `THIRD_PARTY_NOTICES.md`).

**Commands:**

| Command | What it does |
| --- | --- |
| `go test -tags=fuzz ./...` / `make fuzz` | Seeds + rapid property tests |
| `make fuzz FUZZTIME=5s` | Same, then each `Fuzz*` for that duration |
| CI `fuzz` job | `go test -tags=fuzz ./internal/config ./internal/journal ./internal/rules -rapid.checks=100` (only packages that import rapid; the flag is undefined elsewhere) |

**Finding recorded here:** YAML can decode a count such as `max_files: 9223372036854775808` as `uint64`, and `Rule.Int` used to cast it to `int64` without a range check, wrapping to a negative limit. Load now rejects values above `MaxInt64` and negative counts (`REQ-PG-032`).

New candidates are added when a bug or clarification shows a class of inputs rather than a single case.

## Backup Guard

`backup-guard` has numbered requirements, `REQ-BG-…` in [backup-guard.md § Requirements](backup-guard.md#requirements); requirement tests are named `TestREQ_BG_<nnn>_…`. Its tests (`internal/backupguard`, `cmd/backup-guard`) follow the same rules; a test specification follows. Mutation testing does not cover it in the default `make mutate` set; select it with `PKGS='./internal/backupguard'` when QA is ready.

## Roles

- **QA** writes the test specification and requirement tests, runs mutation and exploratory tests, and reports findings as red tests on a `qa/…` branch.
- **Maintainer** makes red tests green, keeps CI and the mutation command working, and merges.
