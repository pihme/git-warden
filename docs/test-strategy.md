# Test strategy

How Git Warden (`push-guard` and `backup-guard`) is tested, and why. The details per requirement are in the [Push Guard test specification](push-guard-testspec.md); decisions that came out of testing are in [Clarifications during tests](push-guard-testspec.md#clarifications-during-tests).

## Goals

- Every numbered requirement (`REQ-PG-…` in [push-guard-rules.md](push-guard-rules.md)) has at least one test named after it (`TestREQ_PG_<nnn>_…`). Tests are written against the spec, not against the code: a test that fails because the code does not meet the spec stays red until the code is fixed or the spec is changed.
- The guards fail closed. For every error path that matters, a test checks that the push is rejected (or the backup run fails), never that it slips through.
- Statement coverage is a signal, not the target. Mutation testing shows whether the tests actually check what the covered code does.
- A test is filed and named after the behaviour it checks, never after the tool or campaign that found the gap. Mutation testing, fuzzing and exploratory testing only point at missing tests; the tests that close the gap are ordinary unit or requirement tests (see [Where tests go](#where-tests-go)).

## Layers

| Layer | Where | What it checks | Runs in CI |
| --- | --- | --- | --- |
| Unit | `internal/rules`, `internal/config`, `internal/journal`, `internal/gitx`, `internal/pushguard`, `internal/backupguard` | One rule, config value or journal query at a time, with real git repos in temp directories where needed | yes, `go test ./...` |
| End to end | `cmd/push-guard/*_test.go`, `cmd/backup-guard/main_test.go` | The built binary between an agent repo and a remote (local, SSH via `sshd`), including approve, reset and notify | yes, `go test ./...` |
| Live | `cmd/push-guard/live_test.go` (`TestLiveRemote`) | A real push to this repo on GitHub with a token | yes, separate `live` job; skipped locally unless `WARDEN_LIVE_REMOTE` and `WARDEN_LIVE_TOKEN_FILE` are set |
| Mutation | `make mutate` locally, or the manual `Heavy tests` workflow | Whether the unit tests notice small changes to the code | only on demand: `.github/workflows/heavy.yml` (`workflow_dispatch`, one matrix job per package); never on push, never blocks a release |
| Fuzzing and property tests | `//go:build fuzz` files under `internal/rules`, `internal/journal`, `internal/config` | Inputs that once produced special cases | yes, modest (`go test -tags=fuzz` on config/journal/rules with `-rapid.checks=100` in the `fuzz` CI job); long fuzzing (`make fuzz FUZZTIME=…`) by hand or on demand in `heavy.yml` (one matrix job per fuzz target, input `fuzztime`) |

**Coverage** is measured with `make coverage` (`scripts/coverage.sh`, output in `.cov/`). It runs every test with `-coverpkg=./...`, so each test counts for every package, and builds and runs the `cmd/push-guard` end-to-end test binary with `-cover` and `GOCOVERDIR`, so the code in the compiled `push-guard` counts too. Its total is the one coverage number of the project (89.9 % at `e764e9d`, 2026-10-11); it is the number on the project card. CI runs it in the `test` job with `COVERAGE_MIN=85`: the job fails if the total is below 85 % (locally `COVERAGE_MIN` defaults to 0, no check); per-package and total are uploaded as artifact `module-coverage`. Per-package numbers from the same run are only a breakdown. Numbers from plain `go test -cover` per package are lower, especially for `internal/pushguard` and `cmd/push-guard`, and are not comparable.

## Mutation testing

- **Tool:** [gomutants](https://github.com/szhekpisov/gomutants) **v0.6.1**. Decided after comparing alternatives on this repo:
  - **Gremlins** was dropped: it treats conditions in `case` lines as not covered and skips them; no config fixes that.
  - **go-mutesting** (avito fork) works but runs strictly one mutant at a time (~2 h for `internal/rules` alone on this box).
  - **gomutants** parallelizes, routes each mutant to covering unit tests only, and keeps the working tree clean via `go test -overlay`.
- **Scope (default):** `./internal/rules`, `./internal/journal`, `./internal/config`. Each mutant runs only the tests of its own package; end-to-end tests are not run per mutant (about 13 s each). Backup Guard is not in the default set yet; QA can select it with `make mutate PKGS='./internal/backupguard'` (or `PACKAGES=…`).
- **Command:** `make mutate` on main (not CI). Defaults and exclusions live in [`.gomutants.yml`](../.gomutants.yml); `scripts/mutate.sh` / Make vars override for a single run. The script runs under `nice -n 10`.
- **Memory cap:** `scripts/mutate.sh` sets `ulimit -d 4000000` (about 4 GB, `MEM_LIMIT_KB`, 0 switches it off) for gomutants and every test binary it starts. Some mutants make a test allocate without bound, up to 4.5 GB within seconds, which aborted three runs on the shared box on 2026-10-11. With the cap such a mutant dies with "out of memory" and counts as killed. It is `-d` and not `-v`: gitleaks (a Go program) reserves far more address space than it uses and panics at start under `ulimit -v 4000000`, so every test that runs gitleaks would fail.
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
- **Workflow:**
  1. QA (or the maintainer) runs the tool and lists the survivors with file, line and mutation.
  2. For each survivor, ask which behaviour is untested. Often several survivors point at the same behaviour (an error that is not wrapped, a boundary, a loop that must not stop early).
  3. If a requirement covers that behaviour, extend or add a `TestREQ_…` test for it. If the requirement is silent or vague, QA updates the test specification first (and, if needed, asks for a spec decision), then the test is written.
  4. If no requirement covers it (internal helpers, error wrapping, layer merging), add an ordinary unit test next to the existing tests of that function.
  5. A mutant that cannot change behaviour (an equivalent mutant) is listed with location and a one-line reason under Equivalent mutants below instead of being tested.
  6. Re-run per package (`make mutate PACKAGES='./internal/…'`) to verify kills.

  Tests never go into files or names that refer to mutation testing (`*_mutants_test.go`, `TestMutants…`). The mapping of the tests written during the October 2026 campaign to their final place is in the test specifications ([Push Guard](push-guard-testspec.md#tests-found-by-mutation-testing), [Backup Guard](backup-guard-testspec.md#tests-found-by-mutation-testing)).

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

Survivors that remain after targeted tests and cannot change observable behaviour, with the function and a short code snippet (no line numbers, so entries don't drift), gomutants mutator and a one-line reason. gomutants reports file:line; match by function and snippet. Re-check the list when the code moves.

#### internal/config

| Location | Mutator | Why equivalent |
|---|---|---|
| config.go `rank` `return 2`→`3` | INTEGER_INCREMENT | rank is only compared in `Worse`; the order red > yellow > green is kept. |
| config.go `rank` `return 0`→`-1` | INTEGER_DECREMENT | Same: only the relative order of ranks matters. |
| config.go `Int` `x < float64(math.MinInt64)`→`<=` | CONDITIONALS_BOUNDARY | Only -2^63 changes branch, and it is rejected either way (out of range vs. must not be negative). |
| config.go `apply` `sort.Strings(ids)` removed | STATEMENT_REMOVE | Only the order of errors when applying a layer changes; success builds a map. |
| config.go `indexOf` `return -1`→`-2` | INTEGER_INCREMENT | Callers only test `< 0`. |
| config.go `load` both `if err != nil` on the embedded defaults | BRANCH_IF | The embedded defaults.yaml always parses and applies (TestBuiltinDefaults); the branches stay as defence in depth. |
| config.go `build` `if err != nil` on ParseDuration | BRANCH_IF | On error `t` is 0, which fails `t < MinTimeout`; only the error message differs. |
| config.go `compile` `continue`→`break` after a duration limit | INVERT_LOOP_CTRL | An unparseable number becomes 0 in `checkBounds` and is rejected as out of range. |
| config.go `countBounds` `"lookback"` lower `0`→`-1` | INTEGER_DECREMENT | Negatives are rejected earlier by `Int`. |
| config.go `countBounds` `"min_lines"` lower `0`→`-1` | INTEGER_DECREMENT | Negatives are rejected earlier by `Int`. |
| config.go `durationBounds` `"max_skew"` lower `0`→`-1` | INTEGER_DECREMENT | Negatives are rejected earlier by `Duration`. |
| config.go `checkBounds` `sort.Strings(keys)` removed | STATEMENT_REMOVE | Only the order of out-of-range errors changes. |
| config.go `checkBounds` both `if !ok { continue }` (duration and count loop) removed | BRANCH_IF | Every configured limit key has an entry in `countBounds` or `durationBounds`. |
| config.go `checkBounds` `continue`→`break` after a duration limit | INVERT_LOOP_CTRL | Equivalent only because `window` sorts last among duration keys; revisit when duration keys are added. |
| config.go `ResolveProgram` `p == ""`→false | EXPRESSION_REMOVE | `resolve(dir, "")` returns `""` anyway. |
| config.go `RemoteKind` `return KindLocal`→zero value (3) | RETURN_ZERO | `KindLocal` is the zero value of `Kind` (`iota` 0). |
| config.go `RemoteKind` Local case emptied | BRANCH_CASE | The final `return KindLocal` yields the same result. |

#### internal/journal

| Location | Mutator | Why equivalent |
|---|---|---|
| journal.go `Append` `json.Marshal` error | BRANCH_IF | An `Entry` has only strings, ints, bools, times and slices of those; Marshal cannot fail. |
| journal.go `Append` `os.MkdirAll` error | BRANCH_IF | If MkdirAll fails, the OpenFile below fails on the same path with the same error. |
| journal.go `Append` `syscall.Flock(…, LOCK_EX)` error | BRANCH_IF | `flock(LOCK_EX)` on a freshly opened regular file (or char device) has no reachable failure (EINTR is retried by the runtime). |
| journal.go `Read` initial buffer `64*1024` (5 mutants: 63, 65, `/`, 1023, 1025) | INTEGER_*/ARITHMETIC_BASE | Only the starting allocation changes; the 64 MiB maximum (tested) is untouched and bufio grows the buffer. |
| journal.go `lastEnd` `i >= 0`→`> 0`, `0`→`1` | CONDITIONALS_BOUNDARY, INTEGER_INCREMENT | Both callers scan from the returned index + 1; when entry 0 is the approve/reset, returning -1 also scans entry 0, which is not a streak or push and never counts. |
| journal.go `OpenApproval` `continue`→`break` in the `switch` | INVERT_LOOP_CTRL | Inside a `switch` that is the last statement of the loop body, `break` leaves the switch and the iteration ends just as with `continue`. |
| journal.go `ApprovalLease` `i >= 0`→`i >= -1` | INTEGER_DECREMENT | OpenApproval just confirmed an approve entry of (ref, sha), so the loop returns before reaching index -1. |

#### internal/rules

| Location | Mutator | Why equivalent |
|---|---|---|
| rules.go `Evaluate` sort `a.Ref < b.Ref`→`<=` | CONDITIONALS_BOUNDARY | Only reached when the refs differ, so `<=` and `<` agree. |
| rules.go `contentRules` `break`→`continue` (invalid UTF-8 scan) | INVERT_LOOP_CTRL | Later invalid lines set the same `binary` and message again. |
| rules.go `pagesRule` `if e.cfg.PagesBranch == ""` return | BRANCH_IF | Without it the Pages ref is `refs/heads/`, which is not a valid ref name and never in a push. |
| rules.go `pagesRule` `FindAllStringSubmatch(l.Text, -1)`→`-2` | INTEGER_INCREMENT | Any negative n means "all matches". |
| rules.go `pagesRule` `known[host] = seen` | STATEMENT_REMOVE | Cache only: the host is looked up again with the same result. |
| rules.go `hostInTree` `return false, err`→`true, err` | RETURN_TRUE | The caller returns on the error and drops the bool. |
| rules.go `metaRules` `range e.d.Commits` (RANGE_BREAK) and both `parentTimes[…] = …` | RANGE_BREAK, STATEMENT_REMOVE | Cache of committer times only; a missing entry is read from Git with the same value. |
| rules.go `signedHistory` `rd.Kind == TagMove`→false, `rd.IsTag()`→false, `\|\|`→`&&` | EXPRESSION_REMOVE, INVERT_LOGICAL | TagMove only occurs for tags, and a tag with commits and an old commit is always a TagMove, so each operand alone decides the same. |
| rules.go `signedHistory` `e.d.DefaultBranch == ""`→false | EXPRESSION_REMOVE | With an empty default branch, `Remote[""]` is never present, so `!ok` already returns. |
| rules.go `signedHistory` peel error ignored; `return false, err`→`true, err` (2) | BRANCH_IF, RETURN_TRUE | Ignoring the peel error leaves `start` empty and `rev-list ""` fails; on errors the caller drops the bool. |
| rules.go `short` `len(oid) > 12`→`>= 12`, `12`→`11` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | For a 12-character oid, `oid[:12]` is the oid. |
| delta.go `isAncestor` `return false, err`→`true, err` | RETURN_TRUE | The caller returns on the error. |
| delta.go `fill` `d.DefaultBranch != ""`→true | EXPRESSION_REMOVE | `Remote[""]` is never present, so `ok` already implies a non-empty name. |
| delta.go `fill` `if rd.IsTag() && rd.New != rd.NewCommit { ids = append(ids, rd.New) }` (6 mutants) | EXPRESSION_REMOVE, INVERT_LOGICAL, CONDITIONALS_NEGATION, BRANCH_IF, STATEMENT_REMOVE | `rev-list --objects` already lists a tag object given as input, and `check` sums a map keyed by oid, so adding or dropping the extra id (or the commit for a lightweight tag) changes nothing. |
| delta.go `addedLines` initial scanner buffer `64*1024` (5 mutants) | INTEGER_*, ARITHMETIC_BASE | Starting allocation only; bufio grows it up to the maximum. |
| delta.go `addedLines` `no := 0`→`1`/`-1` | INTEGER_* | Every added line follows a hunk header, which sets `no` first. |
| delta.go `hunkStart` `j >= 0`→`> 0`, `0`→`1` | CONDITIONALS_BOUNDARY, INTEGER_INCREMENT | j = 0 means s starts with `,` or space; Atoi of `""` or of that string both give 0. |
| delta.go `check` `return sizes, nil`→`nil, nil` for no oids | RETURN_ZERO | Callers only read from or range over the map; a nil map behaves the same. |
| delta.go `sizes` `f.Status != 'D'`→true, `!gitx.IsZero(f.NewBlob)`→true | EXPRESSION_REMOVE | Exactly the deleted files have a zero new blob, so either check alone excludes them. |
| delta.go `totalSize` `return 0, err`→`±1, err` | INTEGER_* | Normalize returns the error and drops the delta. |
| delta.go `commits` `len(oids) == 0` shortcut (2 mutants) | INTEGER_DECREMENT, BRANCH_IF | `cat-file --batch` with an empty line prints ` missing` and the read loop runs zero times. |
| delta.go `ParseCommit` both `return c, err`→zero commit | RETURN_ZERO | Both callers (batch.commits, pushguard replay) drop the commit on error. |
| secret.go `NewScanner` generated config temp file error | BRANCH_IF | The ignore file is created next in the same temp dir and fails with the same error. |
| secret.go `secretRule` `report.Close()` | STATEMENT_REMOVE | Leaks one file descriptor; gitleaks writes the report by path. |
| secret.go `secretRule` `if errors.Is(runErr, gitx.ErrOutputTooLarge)` removed | BRANCH_IF | RunBounded returns ErrOutputTooLarge without an ExitError, so the next branch (`!errors.As(runErr, &ee)`) returns it wrapped with `%w`: same fail-closed error, `errors.Is` still true, only the message gets a prefix. |
| secret.go `readBounded` `io.LimitReader(f, gitx.OutputLimit+1)`→`+2` | INTEGER_INCREMENT | One more byte is read; the check `len(data) > OutputLimit` decides unchanged. |
| secret.go `refOf` `len(e.d.Refs) > 0`→`>= 0`/`> -1` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | refOf runs only for leaks, which need new commits, so there is always at least one ref. |
| secret.go `firstLine` `i >= 0`→`> 0`, `0`→`1` | CONDITIONALS_BOUNDARY, INTEGER_INCREMENT | s is trimmed first, so it never starts with a newline. |
| secret.go `firstLine` `len(s) > 300`→`>= 300`, `300`→`299` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | The cut is `s[:300]`, which is a no-op for a 300-byte string. |

Not equivalent but not killed in rules (deliberately):

- delta.go maximum diff line: was 256 MiB and not killable on the shared box. Since 2026-10-11 it is 16 MiB (`REQ-PG-049`) and `TestREQ_PG_049_DiffLineLimit` tests exactly 16 MiB and one byte more; the next mutation run shows whether these mutants are now killed.
- secret.go `temp` `WriteString` error (also `return f.Name(), err`→nil): needs a write failure on a freshly created temp file (disk full or fault injection).
- secret.go `readBounded` `io.ReadAll` error: a read error on a regular file just opened needs fault injection.
- rules.go `signedHistory` `b.commits` error after `rev-list` succeeded: needs an object that disappears or is corrupt between two git calls.
- delta.go `parseRaw` `i++`→`i--` (INFRA ERROR), and the loop timeouts in rules.go `longLiteral` and delta.go `parseNumstat`: infinite loops; a timeout counts as detected.

#### internal/backupguard

| Location | Mutator | Why equivalent |
|---|---|---|
| bridge.go `ensureBridge` `os.MkdirAll(base, …)` error | BRANCH_IF | removeSetupLeftovers reads the same directory next and fails with the same error. |
| bridge.go `ensureBridge` `git init` error of the temporary bridge | BRANCH_IF | `remote add` then runs in a directory that is not a repository and fails. |
| bridge.go `ensureBridge` `remote add backup` error | BRANCH_IF | checkRemoteURL of `backup` runs next and fails on the same missing remote. |
| bridge.go `ensureBridge` `[][2]string{…}`→`[3]string` | INTEGER_INCREMENT | Only the array type changes; the third element is empty and never read. |
| bridge.go `protectedBranches` `b != key`→true | EXPRESSION_REMOVE | The config regexp anchors `everref.remotes/origin/`, so the prefix is always trimmed and `b` never equals `key`. |
| bridge.go `lock` `MkdirAll(locks)` error, `appendJournal` `MkdirAll(state_dir)` error | BRANCH_IF | OpenFile on a path inside the same directory runs next and fails with the same error. |
| bridge.go `lock` `f.Close()` (2), `appendJournal` `f.Close()` | STATEMENT_REMOVE | Leaks a file descriptor only (the lock is released by `LOCK_UN` or the failed Flock). |
| bridge.go `notify` and `appendJournal` `json.Marshal` error | BRANCH_IF | Warning and Result hold only strings, ints, bools, a time and a string map; Marshal cannot fail. |
| bridge.go `appendJournal` `return f.Close()`→`nil` | RETURN_ERROR_NIL | Close of a regular file after a successful write does not fail on local file systems. |
| bridge.go `tail` `i >= 0`→true, `0`→`-1` | EXPRESSION_REMOVE, INTEGER_DECREMENT | For i = -1 the condition is `cut < len(s)` and `cut += 0`: no change. |
| check.go `CheckConfig` `strings.SplitN(…, ".", 2)`→`3` | INTEGER_INCREMENT | Only element 0 (the major version) is used. |
| check.go `CheckConfig` LoadRepo error, `checkRemote` newRemote error | BRANCH_IF | Preflight has just loaded every repo with the same checks, so these errors are unreachable. |
| config.go `Repos` `if !e.IsDir() { continue }` | BRANCH_IF | For a file, the Stat of `<file>/backup.yaml` fails and the entry is skipped anyway. |
| config.go `LoadRepo`, run.go `PreflightRun` `%w`→`%v` | ERRORF_WRAP | The wrapped errors are plain `errors.New`/`fmt.Errorf` values with nothing to unwrap to. |
| everref.go `FindEverref` `if errors.Is(err, gitx.ErrOutputTooLarge)` removed | BRANCH_IF | The next branch `if err != nil` returns the error wrapped with `%w`; the preflight fails the same and the message still names 1 MiB. |
| everref.go `RunEach` `return text, nil`→`"", nil` | RETURN_ZERO | All callers (ensureBridge, addBranches, runRepo) drop the text on success. |
| everref.go `lineWriter.Write` `if w.each == nil` removed | BRANCH_IF | Without each, `add` only fills `partial` (bounded) and `flush` calls each only when `each != nil`. |
| everref.go `lineWriter.Write` `len(rest) > 0`→`>= 0`/`> -1` | CONDITIONALS_BOUNDARY, INTEGER_DECREMENT | With an empty `rest`, `add(nil)` runs and then `break`. |
| everref.go `lineWriter.flush` `w.each != nil && !w.tooLong && len(w.partial) > 0` (9 mutants) | EXPRESSION_REMOVE×4, INVERT_LOGICAL×2, CONDITIONALS_BOUNDARY, INTEGER_DECREMENT, INTEGER_INCREMENT | `add` empties `partial` when `tooLong`, so `!w.tooLong` is redundant; with `each == nil` `partial` is empty; handing on empty or 1-character lines changes nothing, since `countEvent("")` counts nothing and no event is 1 character long. |
| everref.go `lastLines` `len(lines) > n`→`>=` | CONDITIONALS_BOUNDARY | For len = n, `lines[0:]` is the whole slice. |
| run.go `preflight` `return &Guard{…}, repos, d, nil`→`nil` | RETURN_ZERO | PreflightRun only uses the Defaults when the preflight failed. |

Not equivalent but not killed in internal/backupguard (deliberately):

- The BRANCH_IF mutants on git config / remote errors in `ensureBridge` (`remote add origin`, `--unset-all remote.backup.fetch`, the `user.*`/`commit.gpgSign` config loop, the protected-branch lookup) and `protectedBranches`, the error branches of `runRepo`, plus the not-covered mutants in the same error branches (`ensureBridge`, `protectedBranches`, `lock`, `notify`, `appendJournal`, `CheckConfig`, `checkRemote`, `runRepo`): git config or remote commands that fail in a healthy bridge, Flock errors other than EWOULDBLOCK, and NewRemote errors that LoadRepo already ruled out. They need fault injection between two git calls.
- Timing constants: `FindEverref` 30 s timeout and its `WaitDelay` 5 s, the `killWait = 5 * time.Second` constant and `notify`'s WaitDelay 5 s, ±1. The WaitDelay itself is tested (a child that keeps the pipe open), but not its exact length.
- run.go `runRepo` `err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)`, `err != nil`→true: only differs if a run succeeds and the deadline passes before the deferred check runs. That's a race, not testable reliably.
- Loop timeouts in bridge.go `addBranches` (3), `tail` and `truncate`: infinite loops; a timeout counts as detected.

Test fakes for git-everref refuse an empty `-C` directory: mutants that lose the bridge path would otherwise run git in the package directory and write `everref.*` keys into the repository's own `.git/config`.

#### cmd/backup-guard

No equivalent mutants remain (98.82% efficacy before d621f98, which killed the last survivor, `run`'s `fs.SetOutput`). Not killed:

- main.go `main` `os.Exit(run(…))` (3 mutants, not covered): `main` exits the process; the tests call `run` instead.
- main.go `run` flag-parsing loop `break`→`continue` and `args = fs.Args()[1:]` (4 mutants): infinite flag-parsing loops; the timeouts count as detected.

Mutants that removed a loop's `i++`/`i--` time out (infinite loop); gomutants counts them as detected.

## Where tests go

| Kind of test | Name | File |
| --- | --- | --- |
| Checks a numbered requirement | `TestREQ_PG_<nnn>_<Behaviour>` / `TestREQ_BG_<nnn>_<Behaviour>`; several requirements as `_<nnn>_<nnn>_` | the `req_*_test.go` file of that area (see the test specifications) |
| Checks a function or internal behaviour no requirement names | `Test<Function>` or `Test<Function>_<Behaviour>`; prefer a subtest (`t.Run`) in an existing test of that function over a new top-level test | `<file>_test.go` next to the code (`config_test.go`, `journal_test.go`, `delta_test.go`, …) |
| Fuzz target or `rapid` property | `Fuzz<Function>…` / `TestProp<Behaviour>` | `*_fuzz_test.go` / `*_prop_test.go` with `//go:build fuzz`; separate files because they run separately (`make fuzz`, CI job `fuzz`), not because of who found them |

When a new test checks what an existing test already checks with other values, add the values to that test (table entry or subtest) instead of adding a test. Two tests that check the same thing are merged.

## Fuzzing and property tests

Only where testing already found special cases, not as a general net. All of them sit behind the `fuzz` build tag (`//go:build fuzz`) so ordinary `go test ./...` stays fast and free of the test-only `rapid` dependency.

- **Go fuzzing (`go test -tags=fuzz -fuzz=…`):** parsing of diff headers and `git diff --numstat -z` / `--raw -z` output (paths with spaces, TABs, renames, C-quoting). Seeds run with every `go test -tags=fuzz`; long runs use `make fuzz FUZZTIME=5s` (or any duration).
- **`rapid` property tests:** an ambiguous SHA prefix never approves anything; a limit of the wrong kind never passes `check-config`; a count outside int64 never loads as a wrapped negative; a negative count or duration never loads, zero always does. `rapid` (`pgregory.net/rapid`) is test-only and does not end up in the binaries (not listed in `THIRD_PARTY_NOTICES.md`).

**Commands:**

| Command | What it does |
| --- | --- |
| `go test -tags=fuzz ./...` / `make fuzz` | Seeds + rapid property tests |
| `make fuzz FUZZTIME=5s` | Same, then each `Fuzz*` for that duration |
| CI `fuzz` job | `go test -tags=fuzz ./internal/config ./internal/journal ./internal/rules -rapid.checks=100` (only packages that import rapid; the flag is undefined elsewhere) |

**Finding recorded here:** YAML can decode a count such as `max_files: 9223372036854775808` as `uint64`, and `Rule.Int` used to cast it to `int64` without a range check, wrapping to a negative limit. Load now rejects values above `MaxInt64` and negative counts (`REQ-PG-032`). The follow-up property test found that negative durations still loaded; they are rejected since `c8b32de` (zero stays allowed).

New candidates are added when a bug or clarification shows a class of inputs rather than a single case.

## Backup Guard

`backup-guard` has numbered requirements, `REQ-BG-…` in [backup-guard.md § Requirements](backup-guard.md#requirements); requirement tests are named `TestREQ_BG_<nnn>_…`. Its tests (`internal/backupguard`, `cmd/backup-guard`) follow the same rules; a test specification follows. Mutation testing does not cover it in the default `make mutate` set; select it with `PKGS='./internal/backupguard'` when QA is ready.

## Roles

- **QA** writes the test specification and requirement tests, runs mutation and exploratory tests, and reports findings as red tests on a `qa/…` branch.
- **Maintainer** makes red tests green, keeps CI and the mutation command working, and merges.
