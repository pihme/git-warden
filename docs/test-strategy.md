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
| Mutation | the mutation branches, `make mutate` | Whether the unit tests notice small changes to the code | **no**, run by hand |
| Fuzzing and property tests | only the places listed below | Inputs that once produced special cases | fuzz seeds and `rapid` tests run with `go test`; long fuzzing runs only by hand |

Coverage for `internal/pushguard` should be read with the end-to-end tests included (build the test binary with `-cover` and use `GOCOVERDIR`), because most of that package is only reached through the binary. How to measure it is in the test specification.

## Mutation testing

- **Tool:** `gomutants`. Gremlins was dropped: it treats conditions in `case` lines as not covered and skips them, and no setting changes that. `go-mutesting` (avito fork) works but runs strictly one mutant at a time. The final decision is recorded here after the full runs.
- **Scope:** `internal/rules`, `internal/journal`, `internal/config`. Each mutant runs only the tests of its own package; the end-to-end tests are not run per mutant (about 13 s each).
- **Command:** `make mutate` on the mutation branch, with `WORKERS` for parallelism (default 2). Not part of CI or the build: a full run takes tens of minutes and loads the machine.
- **Goal:** no surviving mutant in the scoped packages. A survivor that the end-to-end tests already catch still gets a unit test in its package, so the mutation run stays meaningful on its own.
- **Workflow:** QA runs the tool and lists the survivors with file, line and mutation. The maintainer (or QA) adds unit tests until they are killed. A mutant that cannot change behaviour (an equivalent mutant) is listed with its mutant ID and a one-line reason under Equivalent mutants below instead of being tested.

### Equivalent mutants

None yet.

## Fuzzing and property tests

Only where testing already found special cases, not as a general net:

- **Go fuzzing (`go test -fuzz`):** parsing of diff headers and `git diff --numstat -z` output (paths with spaces, TABs, renames).
- **`rapid` property tests:** an ambiguous SHA prefix never approves anything; a limit of the wrong kind never passes `check-config`. `rapid` is a test-only dependency and does not end up in the binaries.

New candidates are added when a bug or clarification shows a class of inputs rather than a single case.

## Backup Guard

`backup-guard` has numbered requirements, `REQ-BG-…` in [backup-guard.md § Requirements](backup-guard.md#requirements); requirement tests are named `TestREQ_BG_<nnn>_…`. Its tests (`internal/backupguard`, `cmd/backup-guard`) follow the same rules; a test specification follows. Mutation testing does not cover it yet.

## Roles

- **QA** writes the test specification and requirement tests, runs mutation and exploratory tests, and reports findings as red tests on a `qa/…` branch.
- **Maintainer** makes red tests green, keeps CI and the mutation command working, and merges.
