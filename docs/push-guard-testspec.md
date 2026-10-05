# Push Guard: test specification

This document maps every requirement in [push-guard-rules.md](push-guard-rules.md) (`REQ-PG-001` to `REQ-PG-045`) to the automated tests that check it. Test names start with `TestREQ_PG_<nnn>_`, so `go test ./... -run 'TestREQ_PG_020'` runs the tests of one requirement.

Status is as of branch `qa/journal-tests` (5.10.2026). **Red** means the test fails because the code does not meet the requirement yet; the tests are written against the spec, not against the current code.

## Summary

- 45 of 45 requirements have at least one test; 89 requirement tests in total.
- 88 green, 1 red: `approve` with a SHA prefix that fits two different rejected SHAs silently picks the newer one (`REQ-PG-005`, see below).

## Coverage

Statement coverage from `go test ./... -coverprofile`, before (`test/rules-req-ids`) and after (`qa/rules-tests`):

| Package | Before | After |
| --- | --- | --- |
| `internal/rules` | 83.7 % | 93.4 % |
| `internal/config` | 82.2 % | 98.2 % |
| `internal/journal` | 75.3 % | 95.7 % |
| `internal/pushguard` (unit tests only) | 21.8 % | 21.8 % |
| `internal/pushguard` (end-to-end, measured in the built binary) | 61.0 % | 61.3 % |
| whole module | 56.4 % | 60.6 % |

The end-to-end tests in `cmd/push-guard` run the compiled binary, so `go test -cover` reports 0 % for them. The binary row was measured by building it with `-cover` (`GOFLAGS=-cover`, `GOCOVERDIR`) and reading the result with `go tool covdata percent`. The journal and config requirement tests check boundaries of code that was already covered, so their coverage numbers do not move.

## Requirements and tests

| REQ | Area | Test | File | Status |
| --- | --- | --- | --- | --- |
| `REQ-PG-001` | Verdict | `TestREQ_PG_001_RedResponseIsOnlyTheWaitingLine` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-002` | Verdict | `TestREQ_PG_002_FindingString` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-003` | Verdict | `TestREQ_PG_003_AllowedTagMoveIsForwarded` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-003` | Verdict | `TestREQ_PG_003_GreenForwardsExactlyTheCheckedSHAs` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-003` | Verdict | `TestREQ_PG_003_MixedPushForwardsNothing` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-003` | Verdict / A15 | `TestREQ_PG_003_ApprovedRewriteRemoteMovedSinceApproval` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_BadLimitsFailClosed` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_BadObjectsFailClosed` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_InternalErrorThenFreshCheck` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_NoScannerIsAnError` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_Parsers` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_ScannerFailuresFailClosed` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_ScannerTimeoutFailsClosed` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_ScannerErrorMessageIsShortened` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_CatFileMissingOrUnexpectedFailsClosed` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_ScannerTimeoutIsInternalError` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-004` | Verdict | `TestREQ_PG_004_UnknownCommitInReportGoesToFirstRef` | `internal/rules/req_failclosed_test.go` | green |
| `REQ-PG-005` | Verdict | `TestREQ_PG_005_ApprovalIsPerRefAndSHA` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-005` | Verdict | `TestREQ_PG_005_ApprovedRewriteIsForwardedWithoutRecheck` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-005` | Verdict | `TestREQ_PG_005_FindRejectedByPrefix` | `internal/journal/req_journal_edges_test.go` | green |
| `REQ-PG-005` | Verdict | `TestREQ_PG_005_AmbiguousPrefixApprovesNothing` | `internal/journal/req_journal_edges_test.go` | green |
| `REQ-PG-006` | `REF-DELETE` | `TestREQ_PG_006_RefDeleteBranchAndTag` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-007` | `REF-NON-FF` | `TestREQ_PG_007_RefNonFastForward` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-008` | `REF-TAG-MOVE` | `TestREQ_PG_008_RefTagMove` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-009` | `REF-TAG-NEW` | `TestREQ_PG_009_RefTagNew` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-010` | `REF-NAMESPACE` | `TestREQ_PG_010_RefNamespace` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-011` | `REF-COUNT` | `TestREQ_PG_011_RefCountDefaultBoundary` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-012` | `PATH-RED` | `TestREQ_PG_012_PathRedAddedModifiedDeletedRenamed` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-013` | `PATH-YELLOW` | `TestREQ_PG_013_PathYellowAddedModifiedDeleted` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-014` | `PATH-RED`, `PATH-YELLOW` | `TestREQ_PG_014_RenameCheckedUnderBothNames` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-015` | `PATH-RED`, `PATH-YELLOW` | `TestREQ_PG_015_PathOnBothListsIsRed` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-016` | `MODE-EXEC` | `TestREQ_PG_016_ModeExecOnlyWhenBecomingExecutable` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-017` | `MODE-SYMLINK` | `TestREQ_PG_017_ModeSymlinkAddedOrChanged` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-018` | `MODE-SUBMODULE` | `TestREQ_PG_018_ModeSubmoduleAddedOrChanged` | `internal/rules/req_ref_path_mode_test.go` | green |
| `REQ-PG-019` | `CONTENT-SECRET` | `TestREQ_PG_019_SecretOnlyInPushedRangeAndAttributedToRef` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-019` | `CONTENT-SECRET` | `TestREQ_PG_019_ScannerReportDuplicatesAndUnknownCommit` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-020` | `CONTENT-SECRET` | `TestREQ_PG_020_ScannerFilesComeFromTheWall` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-020` | `CONTENT-SECRET` | `TestREQ_PG_020_ScannerIgnoresIgnoreFileInScannedDir` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-020` | `CONTENT-SECRET` | `TestREQ_PG_020_ScannerIgnoresRepoConfiguration` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-020` | `CONTENT-SECRET` | `TestREQ_PG_020_DotGitleaksIgnoreDirectoryFailsClosed` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-020` | `CONTENT-SECRET` | `TestREQ_PG_020_ScannerDefaultsAndWallOnlyConfig` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-021` | `CONTENT-SCANNER-ALLOW` | `TestREQ_PG_021_ScannerAllowInAddedLinesAnyCase` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-021` | `CONTENT-SCANNER-ALLOW` | `TestREQ_PG_021_ScannerAllowInOddFileNames` | `internal/rules/req_pathnames_test.go` | green |
| `REQ-PG-021` | `CONTENT-SCANNER-ALLOW` | `TestREQ_PG_021_DiffHeaderPath` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-022` | `CONTENT-INVISIBLE` | `TestREQ_PG_022_ByteOrderMarkOnlyAllowedAtTheStart` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-022` | `CONTENT-INVISIBLE` | `TestREQ_PG_022_InvisibleCharacterRanges` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-022` | `CONTENT-INVISIBLE` | `TestREQ_PG_022_InvisibleInOddFileNames` | `internal/rules/req_pathnames_test.go` | green |
| `REQ-PG-023` | `CONTENT-BINARY` | `TestREQ_PG_023_BinaryAddedOrChangedAlsoInTestDirs` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-024` | `CONTENT-BINARY` (SPEC settled) | `TestREQ_PG_024_InvalidUTF8InAddedLinesIsBinary` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-025` | `CONTENT-BLOB` | `TestREQ_PG_025_BlobThresholds` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-025` | `CONTENT-BLOB` | `TestREQ_PG_025_BlobInOddFileNames` | `internal/rules/req_pathnames_test.go` | green |
| `REQ-PG-026` | `CONTENT-PAGES-SCRIPT` | `TestREQ_PG_026_PagesScriptOnConfiguredBranchOnly` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-026` | `CONTENT-PAGES-SCRIPT` | `TestREQ_PG_026_PagesScriptInOddFileNames` | `internal/rules/req_pathnames_test.go` | green |
| `REQ-PG-026` | `CONTENT-PAGES-SCRIPT` | `TestREQ_PG_026_EmptyPagesBranchDisablesRule` | `internal/rules/req_edges_test.go` | green |
| `REQ-PG-027` | `CONTENT-PAGES-SCRIPT` (SPEC settled) | `TestREQ_PG_027_PagesHostKnownFromOldTreeCaseInsensitive` | `internal/rules/req_content_test.go` | green |
| `REQ-PG-028` | `META-UNSIGNED` | `TestREQ_PG_028_DefaultLookbackIs20` | `internal/rules/req_meta_test.go` | green |
| `REQ-PG-028` | `META-UNSIGNED` | `TestREQ_PG_028_UnsignedAfterSignedHistory` | `internal/rules/req_meta_test.go` | green |
| `REQ-PG-029` | `META-UNSIGNED` (SPEC settled) | `TestREQ_PG_029_LookbackFollowsFirstParents` | `internal/rules/req_meta_test.go` | green |
| `REQ-PG-029` | `META-UNSIGNED` (SPEC settled) | `TestREQ_PG_029_LookbackSource` | `internal/rules/req_meta_test.go` | green |
| `REQ-PG-030` | `META-BACKDATED` | `TestREQ_PG_030_BackdatedBoundaries` | `internal/rules/req_meta_test.go` | green |
| `REQ-PG-031` | `META-FUTURE` | `TestREQ_PG_031_FutureBoundaries` | `internal/rules/req_meta_test.go` | green |
| `REQ-PG-032` | `SIZE-LIMIT` | `TestREQ_PG_032_033_FileAndLineTotals` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-032` | `SIZE-LIMIT` | `TestREQ_PG_032_033_SizeDefaults` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-032` | `SIZE-LIMIT` | `TestREQ_PG_032_MaxBytes` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-032` | `SIZE-LIMIT` | `TestREQ_PG_032_SizeSkipsDeletions` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-032` | Configuration | `TestREQ_PG_032_WholeNumberLimitForms` | `internal/config/req_config_test.go` | green |
| `REQ-PG-032` | Configuration | `TestREQ_PG_032_LimitKindCheckedOnLoad` | `internal/config/req_config_test.go` | green |
| `REQ-PG-033` | `SIZE-LARGE` | `TestREQ_PG_033_MaxCommits` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-034` | `SIZE-MASS-DELETE` | `TestREQ_PG_034_DeletedFiles` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-034` | `SIZE-MASS-DELETE` | `TestREQ_PG_034_DeletedShare` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-034` | `SIZE-MASS-DELETE` | `TestREQ_PG_034_DeletedShareDefaults` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-035` | `SIZE-MASS-DELETE` (SPEC settled) | `TestREQ_PG_035_ShareOnRenamesNotDeletes` | `internal/rules/req_size_test.go` | green |
| `REQ-PG-036` | `RATE-LIMIT` | `TestREQ_PG_036_PushCountWindow` | `internal/journal/req_journal_test.go` | green |
| `REQ-PG-036` | `RATE-LIMIT` | `TestREQ_PG_036_LastPush` | `internal/journal/req_journal_edges_test.go` | green |
| `REQ-PG-036` | `RATE-LIMIT` | `TestREQ_PG_036_RateLimitedPushIsRedWithFixedMessage` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-037` | `RATE-YELLOW-STREAK` | `TestREQ_PG_037_ApprovalEndsStreak` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-037` | `RATE-YELLOW-STREAK` | `TestREQ_PG_037_GreenPushDoesNotResetCount` | `cmd/push-guard/req_e2e_test.go` | green |
| `REQ-PG-037` | `RATE-YELLOW-STREAK` | `TestREQ_PG_037_StreakEndsOnlyByApproveOrReset` | `internal/journal/req_journal_test.go` | green |
| `REQ-PG-037` | `RATE-YELLOW-STREAK` | `TestREQ_PG_037_YellowCountWindowAndScope` | `internal/journal/req_journal_test.go` | green |
| `REQ-PG-038` | Configuration | `TestREQ_PG_038_039_EveryRuleOnByDefaultWithDocumentedColor` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-038` | Configuration | `TestREQ_PG_038_DisabledRulesNeverFire` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-039` | Configuration | `TestREQ_PG_039_ColorOverrides` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-040` | Configuration | `TestREQ_PG_040_AllowPerSubject` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-041` | Configuration | `TestREQ_PG_041_DenyWinsOverAllow` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-041` | Configuration / A16 | `TestREQ_PG_041_EventDenyNeedsTrigger` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-042` | Configuration | `TestREQ_PG_042_PatternsMatchWholeSubject` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-043` | Configuration | `TestREQ_PG_043_Subjects` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-044` | What a rule sees | `TestREQ_PG_044_CumulativeDiffAndPerCommitMeta` | `internal/rules/req_config_test.go` | green |
| `REQ-PG-045` | What a rule sees | `TestREQ_PG_045_KindAndOldFromRemote` | `internal/rules/req_config_test.go` | green |

## Red tests

### REQ-PG-005: ambiguous SHA prefix (fixed)

**Fixed:** `FindRejected` returns nothing when a prefix matches more than one distinct rejected SHA; `approve` errors with “ambiguous SHA prefix …; give more digits”. Same SHA rejected twice still resolves to the newest push.

### Fixed: REQ-PG-032 (a limit of the wrong kind passed check-config)

`config.load` checks only that a limit is a number or a string that parses as a duration, not that it has the kind the rule reads. `META-FUTURE: {max_skew: 600}`, `SIZE-LIMIT: {max_files: '10m'}` and `SIZE-LIMIT: {max_files: 1.5}` therefore load, and `push-guard check-config` says `configuration ok`. On the first push the rule calls `Duration` or `Int`, gets an error, and every push to that repo fails with `internal error, try again later` (fail-closed, so safe, but the preflight should catch it). A list or a bool is already rejected on load. `TestREQ_PG_032_LimitKindCheckedOnLoad` expects all five cases to fail on load; green since `7cee044`.

### Fixed: REQ-PG-021, 022, 025, 026 (file names with a space)

`addedLines` in `internal/rules/delta.go` took the path from the `+++ b/<path>` line of the patch. For a path that contains a space, git appends a TAB to that line (`+++ b/with space.txt\t`), so the added lines were stored under `"with space.txt\t"` and `CONTENT-SCANNER-ALLOW`, `CONTENT-INVISIBLE`, `CONTENT-BLOB` and `CONTENT-PAGES-SCRIPT` never fired on such files. Since `a1ef172` the path is cut at the first TAB (`diffHeaderPath`); `req_pathnames_test.go` and `TestREQ_PG_021_DiffHeaderPath` are green.

### Fixed: REQ-PG-020

gitleaks also loads `<source>/.gitleaksignore` next to `--gitleaks-ignore-path`. If that file exists in the scanned directory, the scanner now fails closed (`TestREQ_PG_020_ScannerIgnoresIgnoreFileInScannedDir`, green) instead of renaming it aside (rename races under concurrent scans). Committed `.gitleaks.toml` / `.gitleaksignore` and inline `gitleaks:allow` stay ignored via `--config` / `--ignore-gitleaks-allow`.

## Observations (questions, not failing tests)

These behaviours are within the spec's wording or on the safe side, but may not be intended:

1. **Approved rewrite must not overwrite another writer (A15 / REQ-PG-003).** The lease for a human-approved rewrite/tag-move/delete is the old OID recorded with the approval (remote tip at check time). If another writer moves the ref between approval and repush, the guard rejects with the fixed message `Remote moved since approval`, leaves the remote tip unchanged, and invalidates the approval (re-check and a new approval required). Allow-list exemptions still lease against the remote state just read.
2. **Settled (A16 Option 1, 2026-10-05):** `REQ-PG-041` deny semantics. Subject-scanning rules (`REF-NAMESPACE`, `PATH-*`) fire on `deny` alone; event rules (`REF-DELETE`, `REF-NON-FF`, `REF-TAG-NEW`, `REF-TAG-MOVE`, `MODE-*`, `META-*`, `SIZE-*`, `CONTENT-*`) apply `deny` only when already triggered. Spec sentence sharpened accordingly; code already matched.
3. `CONTENT-PAGES-SCRIPT` searches with `git grep -I`, so binary files are skipped; the spec says `-i -F`.
4. `RATE-LIMIT` counts rate-limited pushes too, so an agent that keeps pushing stays limited until it pauses for a full window.
5. `RATE-YELLOW-STREAK` counts yellow rejections per repo, not per agent and repo as REQ-PG-037 says. Same result while each repo has one agent.
6. `CONTENT-BLOB` treats long runs of `-` or `=` (e.g. Markdown rules) as base64.
7. Refs approved by a human are excluded from `REF-COUNT`.
8. `parseNumstat` accepts a truncated rename entry ending in a NUL. Git never produces this.
9. `pages_branch: ''` switches `CONTENT-PAGES-SCRIPT` off completely, even for `gh-pages`. Neither the spec nor `push-guard.md` mentions this.

### Further configuration tests

`internal/config/req_config_test.go` also covers the verdict colour order, wall-only `Load`, `Repos` and `RepoDir`, unknown rule IDs (disabled), the limit accessors, `allow_remove` / `deny_remove`, a rule entry without settings, `timeout` (default 60s, must be positive), unreadable or broken `defaults.yaml` / `warden.yaml`, and `known_hosts` or a scheme that does not fit the remote. All green. Left out on purpose in `internal/config`: errors from `filepath.Abs` and from the built-in defaults (fixed at build time), and the `60s` fallback in `build`, which the built-in defaults already set.

### Further journal tests

`internal/journal/req_journal_edges_test.go` also covers blank and 1 MB lines in `push.jsonl`, a broken line failing closed with its line number, an unreadable journal, the private permissions of the state directory (0700) and the journal (0600), append errors, and 50 concurrent appends of 64 KB each without interleaving. Left out on purpose: `json.Marshal`, `flock`, `write` and `fsync` errors, and the fallback at the end of `ApprovalLease`, which `OpenApproval` makes unreachable.

### Untested on purpose

The remaining uncovered statements in `internal/rules` (49 of 739) are `if err != nil` branches after git calls and config reads that are validated at load time, plus the `gitleaks` fallback in `NewScanner`, which config already fills in. They are reachable only with a broken git or a hand-built config. That a failing rule rejects the push is covered by the `REQ-PG-004` tests.

## Running

```sh
go test ./... -run 'TestREQ_PG_'           # all requirement tests
go test ./... -coverprofile=cover.out      # coverage
```

`REQ-PG-020` and the `CONTENT-SECRET` tests need gitleaks 8.x on `PATH`.
