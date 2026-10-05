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

None yet. Survivors that remain after targeted tests and that truly cannot change observable behaviour are listed here with file:line, mutator, and a one-line reason.

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
