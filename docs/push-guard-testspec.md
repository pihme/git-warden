# Push Guard: test specification

This document maps every requirement in [push-guard-rules.md](push-guard-rules.md) (`REQ-PG-001` to `REQ-PG-045`) to the automated tests that check it. Test names start with `TestREQ_PG_<nnn>_`, so `go test ./... -run 'TestREQ_PG_020'` runs the tests of one requirement.

Status is as of `main` @ `f223de4` plus branch `qa/journal-tests` (5.10.2026). **Red** means the test fails because the code does not meet the requirement yet; the tests are written against the spec, not against the current code.

## Summary

- 45 of 45 requirements have at least one test; 89 requirement tests in total.
- 89 green, 0 red. All findings so far are fixed (see below).

## Coverage

Statement coverage from `go test ./... -coverprofile`, before (`test/rules-req-ids`) and after (`qa/rules-tests`):

| Package | Before | After |
| --- | --- | --- |
| `internal/rules` | 83.7 % | 93.4 % |
| `internal/config` | 82.2 % | 98.2 % |
| `internal/journal` | 75.3 % | 96.5 % |
| `internal/pushguard` (unit tests only) | 21.8 % | 21.8 % |
| `internal/pushguard` (end-to-end, measured in the built binary) | 61.0 % | 61.3 % |
| whole module | 56.4 % | 60.9 % |

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

## Tests found by mutation testing

The mutation campaign of October 2026 (see [Test strategy § Mutation testing](test-strategy.md#mutation-testing)) added tests in `*_mutants_test.go` files with `TestMutants…` names. They check real behaviour and stay, but are moved to where that behaviour is tested and renamed after it. "Merge" means: add the cases as table entries or subtests to the named test, and drop the old one. Tests that check the same thing twice are merged into one. Where a REQ is given, the test then gets a row in the table above.

The maintainer moves the tests; the REQ fit is QA's proposal and may be corrected while moving if the test body shows a better one.

### internal/config (`config_mutants_test.go`)

| Old test | REQ | New name | Target file |
| --- | --- | --- | --- |
| `TestMutantsColorRankUnknownTiedWithGreen` | `REQ-PG-039` | merge into the colour-order test | `internal/config/req_config_test.go` |
| `TestMutantsFiresAllowAndTrigger` | `REQ-PG-040`, `041` | `TestREQ_PG_040_041_FiresWithAllowDenyAndTrigger` | `internal/config/req_config_test.go` |
| `TestMutantsAllowedRequiresEnabled` | `REQ-PG-038` | `TestREQ_PG_038_DisabledRuleAllowsNothing` | `internal/config/req_config_test.go` |
| `TestMutantsIntDurationErrorReturns`, `TestMutantsLimitErrorReasons` | `REQ-PG-032` | `TestREQ_PG_032_LimitAccessorErrors` (merged) | `internal/config/req_config_test.go` |
| `TestMutantsDurationLimitContinue`, `TestMutantsLimitCheckContinuesAfterDuration` | `REQ-PG-032` | `TestREQ_PG_032_EveryLimitOfARuleIsChecked` (merged) | `internal/config/req_config_test.go` |
| `TestMutantsTimeoutOneNanosecondIsPositive` | none | merge into the `timeout` test ("must be positive") | `internal/config/req_config_test.go` |
| `TestMutantsMergeSideEffects`, `TestMutantsEmptyNotifyAndForwardKeepLowerLayer`, `TestMutantsStateDirTimeoutDefaults` | none | merge into `TestMergeLayers` as subtests `every wall key applies`, `empty block keeps lower layer`, `defaults` | `internal/config/config_test.go` |
| `TestMutantsNullRuleEntryContinue`, `TestMutantsNullRuleDoesNotStopLaterRules` | none | `TestMergeLayers` subtest `rule without settings` (merged) | `internal/config/config_test.go` |
| `TestMutantsMatchRemoveOnlyNamed` | none | `TestMergeLayers` subtest `match_remove removes only the named pattern` | `internal/config/config_test.go` |
| `TestMutantsWallRejectsRepoRemoteKeys` | none | `TestErrors` subtest `key in the wrong layer` | `internal/config/config_test.go` |
| `TestMutantsRemoveErrorWraps`, `TestMutantsRemoveErrorsWrap` | none | `TestErrors` subtest `*_remove of an unknown pattern` (merged) | `internal/config/config_test.go` |
| `TestMutantsParseLayerWraps`, `TestMutantsParseLayerKeepsYAMLError` | none | `TestErrors` subtest `YAML error is kept` (merged) | `internal/config/config_test.go` |
| `TestMutantsCheckRemoteWraps`, `TestMutantsCheckRemoteErrorWrapped` | none | `TestErrors` subtest `unsupported remote` (merged) | `internal/config/config_test.go` |
| `TestMutantsCompileBadRegexWraps`, `TestMutantsBadRegexKeepsSyntaxError` | none | `TestErrors` subtest `bad pattern names the rule and keeps the syntax error` (merged) | `internal/config/config_test.go` |
| `TestMutantsUnreadableFilesReturnTheReadError` | none | `TestErrors` subtest `read error is returned as is` | `internal/config/config_test.go` |
| `TestMutantsLoadFailsWithoutWorkingDirectory` | none | `TestErrors` subtest `relative dir without working directory` | `internal/config/config_test.go` |
| `TestMutantsErrUnknownRepoUnwraps`, `TestMutantsInvalidRepoNameWithFolder` | none | merge into `TestRepoNames` | `internal/config/config_test.go` |
| `TestMutantsCheckRemoteAndRemoteKind`, `TestMutantsRemoteKindErrorReturnsZero` | none | merge into `TestRemoteKind` (`CheckRemote` cases as a subtest) | `internal/config/config_test.go` |
| `TestMutantsResolveAndResolveProgram` | none | `TestResolveProgram` | `internal/config/config_test.go` |

### internal/journal (`journal_mutants_test.go`, `journal_mutants2_test.go`)

The two files overlap; each pair below is one test afterwards.

| Old test | REQ | New name | Target file |
| --- | --- | --- | --- |
| `TestMutantsYellowCountRequiresPushEvent`, `TestMutantsYellowCountOnlyPushes` | `REQ-PG-037` | merge into `TestREQ_PG_037_YellowCountWindowAndScope` | `internal/journal/req_journal_test.go` |
| `TestMutantsLastPushWalksFromEnd`, `TestMutantsLastPushFirstAndLastEntry` | `REQ-PG-036` | merge into `TestREQ_PG_036_LastPush` | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsOpenApprovalRefAndSHA`, `TestMutantsOpenApprovalMatchesRefAndSHA` | `REQ-PG-005` | `TestREQ_PG_005_OpenApprovalNeedsRefAndSHA` (merged) | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsOpenApprovalConsumedByForwardAndRemoteMoved` | `REQ-PG-003`, `005` | `TestREQ_PG_003_005_ApprovalEndsOnForwardOrRemoteMoved` | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsApprovalLeaseMatches`, `TestMutantsApprovalLeaseOnlyFromApproveOfThatRef` | `REQ-PG-003` | `TestREQ_PG_003_ApprovalLeaseFromNewestApproveOfThatRef` (merged; check against `TestApprovalLeaseAndRemoteMovedInvalidation` and merge there if it is the same) | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsFindRejectedPrefixMinAndContinue`, `TestMutantsFindRejectedEdges` | `REQ-PG-005` | merge into `TestREQ_PG_005_FindRejectedByPrefix` | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsAmbiguousRejectedPrefixSkipsNonPush` | `REQ-PG-005` | merge into `TestREQ_PG_005_AmbiguousPrefixApprovesNothing` | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsAppendAndReadRoundTrip` | none | merge into `TestAppendRead` | `internal/journal/journal_test.go` |
| `TestMutantsAppendReportsOpenWriteAndSyncErrors` | none | `TestAppend_ReportsOpenWriteAndSyncErrors` (check against the append-error test in `req_journal_edges_test.go` and merge if it is the same) | `internal/journal/journal_test.go` |
| `TestMutantsReadKeepsJSONError` | none | merge into the broken-line test ("fails closed with its line number") | `internal/journal/req_journal_edges_test.go` |
| `TestMutantsReadLineLimit` | none | merge into the 1 MB-line test as `just under / over 64 MiB` | `internal/journal/req_journal_edges_test.go` |

### internal/rules (`rules_mutants_test.go`, `delta_mutants_test.go`, `secret_mutants_test.go`)

Tests already named `TestREQ_PG_…` keep their name and only move.

| Old test | REQ | New name | Target file |
| --- | --- | --- | --- |
| `TestMutantsFindingStringAndShort` | `REQ-PG-002` | merge into `TestREQ_PG_002_FindingString` | `internal/rules/req_config_test.go` |
| `TestMutantsFindingsSortedByRefThenRule` | `REQ-PG-002` | `TestREQ_PG_002_FindingsSortedByRefThenRule` | `internal/rules/req_config_test.go` |
| `TestMutantsAllowedOnlyWhenTriggeredAndExempt` | `REQ-PG-040` | `TestREQ_PG_040_AllowRecordedOnlyWhenTriggeredAndEnabled` (check against `TestAllowedRecordedForLease`) | `internal/rules/req_config_test.go` |
| `TestREQ_PG_004_MissingLimitFailsClosed` | `REQ-PG-004` | keep | `internal/rules/req_failclosed_test.go` |
| `TestMutantsRefAndPathMessages` | `REQ-PG-010`, `012`, `013` | split: `TestREQ_PG_010_NamespaceMessages`, `TestREQ_PG_012_013_PathMessages` | `internal/rules/req_ref_path_mode_test.go` |
| `TestMutantsModeRules` | `REQ-PG-016`–`018` | `TestREQ_PG_016_017_018_ModeTargetsAndMessages` | `internal/rules/req_ref_path_mode_test.go` |
| `TestMutantsContentRules` | `REQ-PG-021`, `022` | split: `TestREQ_PG_021_ScannerAllowOnlyInMarkedFile`, `TestREQ_PG_022_InvisibleAtLineStart` | `internal/rules/req_content_test.go` |
| `TestMutantsLongLiteral` | `REQ-PG-025` | `TestREQ_PG_025_LongLiteralCharacterClasses` | `internal/rules/req_content_test.go` |
| `TestREQ_PG_026_PagesScriptHosts` | `REQ-PG-026` | keep | `internal/rules/req_content_test.go` |
| `TestMutantsMetaRulesRefSelection` | `REQ-PG-030`, `031` | `TestREQ_PG_030_031_DateRulesPerRefAndFailClosed` | `internal/rules/req_meta_test.go` |
| `TestREQ_PG_028_SignedHistoryErrorsAndDisable`, `TestREQ_PG_028_LookbackZeroAndOne`, `TestREQ_PG_029_BranchUsesOwnHistory` | `REQ-PG-028`, `029` | keep | `internal/rules/req_meta_test.go` |
| `TestMutantsSizeRulesRefSelection` | `REQ-PG-032`–`034` | `TestREQ_PG_032_033_034_SizeRulesPerRef` | `internal/rules/req_size_test.go` |
| `TestMutantsSubmoduleNotSized` | `REQ-PG-032` | `TestREQ_PG_032_SubmoduleNotSized` | `internal/rules/req_size_test.go` |
| `TestREQ_PG_035_MassDeleteShareEdges` | `REQ-PG-035` | keep | `internal/rules/req_size_test.go` |
| `TestREQ_PG_004_NormalizeFailsClosedAtEveryGitStep` | `REQ-PG-004` | keep | `internal/rules/req_failclosed_test.go` |
| `TestMutantsNormalizeDetails`, `TestMutantsNormalizeRound2` | `REQ-PG-004`, `044`, `045` | split: `TestREQ_PG_044_045_DiffBaseAndCommitList` (same commit on two refs, merge base, empty-tree base, missing numstat entry) into `internal/rules/req_config_test.go`; the fail-closed cases (unpeelable default branch, failing merge-base) as `TestREQ_PG_004_DiffBaseFailsClosed` into `internal/rules/req_failclosed_test.go` | see name |
| `TestMutantsAddedLinesLongAndPlusLines` | none | `TestAddedLines_LongLinesAndPlusPrefixedContent` | `internal/rules/delta_test.go` (new) |
| `TestMutantsPathsAndRemoteOIDs` | none | `TestPathsAndRemoteOIDs` | `internal/rules/delta_test.go` (new) |
| `TestMutantsParseRaw`, `TestMutantsParseNumstat`, `TestMutantsHeaderAndHunkParsing`, `TestMutantsIdentTimeAndParseCommit` | `REQ-PG-004` for the rejected inputs | rejected inputs: merge into `TestREQ_PG_004_Parsers`; accepted inputs: `TestParseRaw`, `TestParseNumstat`, `TestDiffHeaderPathAndHunkStart`, `TestParseCommit` | `internal/rules/req_failclosed_test.go` / `internal/rules/delta_test.go` (new) |
| `TestREQ_PG_004_ScannerInvocation`, `TestREQ_PG_004_ScannerLogOptsWithoutRemote`, `TestREQ_PG_004_NoNewCommitsNotScanned` | `REQ-PG-019` | rename to `TestREQ_PG_019_ScannerScansExactlyTheNewTips`, `TestREQ_PG_019_ScannerLogOptsWithoutRemote`, `TestREQ_PG_019_NoNewCommitsNotScanned`: they check what is scanned, not failing closed | `internal/rules/req_content_test.go` |
| `TestREQ_PG_004_ScannerReportErrorsFailClosed`, `TestREQ_PG_004_ScannerStartErrorWrapped`, `TestREQ_PG_004_ScannerTempFilesFailClosed` | `REQ-PG-004` | keep (check `ScannerReportErrorsFailClosed` against `TestREQ_PG_004_ScannerFailuresFailClosed` and merge if the same) | `internal/rules/req_failclosed_test.go` |
| `TestREQ_PG_020_GeneratedScannerFiles` | `REQ-PG-020` | merge into `TestREQ_PG_020_ScannerDefaultsAndWallOnlyConfig` | `internal/rules/req_edges_test.go` |
| `TestREQ_PG_020_UncheckableDotGitleaksIgnoreFailsClosed` | `REQ-PG-020` | merge into `TestREQ_PG_020_DotGitleaksIgnoreDirectoryFailsClosed` | `internal/rules/req_edges_test.go` |
| `TestMutantsFirstLineTruncation` | `REQ-PG-004` | merge into `TestREQ_PG_004_ScannerErrorMessageIsShortened` | `internal/rules/req_edges_test.go` |

Afterwards there is no `*_mutants_test.go` file and no `TestMutants` name left (`rg -l 'TestMutants|_mutants' --glob '*_test.go' .` finds nothing), `go test ./...` passes with at least the same coverage, and `make mutate` gives the same numbers as at `d7679a7`.

## Red tests

None at the moment.

### Fixed: REQ-PG-005 (an ambiguous SHA prefix approved the newer commit)

`push-guard approve <repo> <ref> <prefix>` silently approved the newest rejected push whose SHA starts with the prefix, even when two different rejected SHAs shared it. An agent could push a second commit with the same short prefix after the one the human reviewed. Since `f223de4`, `FindRejected` returns nothing when a prefix matches more than one distinct rejected SHA, and `approve` errors with `ambiguous SHA prefix …; give more digits`. The same SHA rejected twice still resolves to the newest push. `TestREQ_PG_005_AmbiguousPrefixApprovesNothing` also checks `AmbiguousRejectedPrefix`: forwarded pushes, other repos and other refs do not make a prefix ambiguous.

### Fixed: REQ-PG-032 (a limit of the wrong kind passed check-config)

`config.load` checks only that a limit is a number or a string that parses as a duration, not that it has the kind the rule reads. `META-FUTURE: {max_skew: 600}`, `SIZE-LIMIT: {max_files: '10m'}` and `SIZE-LIMIT: {max_files: 1.5}` therefore load, and `push-guard check-config` says `configuration ok`. On the first push the rule calls `Duration` or `Int`, gets an error, and every push to that repo fails with `internal error, try again later` (fail-closed, so safe, but the preflight should catch it). A list or a bool is already rejected on load. `TestREQ_PG_032_LimitKindCheckedOnLoad` expects all five cases to fail on load; green since `7cee044`.

### Fixed: REQ-PG-021, 022, 025, 026 (file names with a space)

`addedLines` in `internal/rules/delta.go` took the path from the `+++ b/<path>` line of the patch. For a path that contains a space, git appends a TAB to that line (`+++ b/with space.txt\t`), so the added lines were stored under `"with space.txt\t"` and `CONTENT-SCANNER-ALLOW`, `CONTENT-INVISIBLE`, `CONTENT-BLOB` and `CONTENT-PAGES-SCRIPT` never fired on such files. Since `a1ef172` the path is cut at the first TAB (`diffHeaderPath`); `req_pathnames_test.go` and `TestREQ_PG_021_DiffHeaderPath` are green.

### Fixed: REQ-PG-020

gitleaks also loads `<source>/.gitleaksignore` next to `--gitleaks-ignore-path`. If that file exists in the scanned directory, the scanner now fails closed (`TestREQ_PG_020_ScannerIgnoresIgnoreFileInScannedDir`, green) instead of renaming it aside (rename races under concurrent scans). Committed `.gitleaks.toml` / `.gitleaksignore` and inline `gitleaks:allow` stay ignored via `--config` / `--ignore-gitleaks-allow`.

## Clarifications during tests

Points the tests raised that are now decided. The spec (`push-guard-rules.md`, section Decided) and the code follow them; the tests named in the table above check them.

| # | Requirement | Question | Decision |
| --- | --- | --- | --- |
| C1 | `REQ-PG-003` (A15) | Another writer moves the ref between a human's approval and the agent's repush. Does the approved rewrite overwrite that commit? | No. The lease is the old OID recorded with the approval. If the remote moved, the guard rejects with the fixed message `Remote moved since approval`, leaves the remote unchanged and drops the approval, so a new check and approval are needed (`57aabff`). The owner gets no warning for this (Peter, 2026-10-05). |
| C2 | `REQ-PG-041` (A16) | Does `deny` make a rule fire on its own? | Only for rules that scan a subject (`REF-NAMESPACE`, `PATH-*`). Event rules (`REF-DELETE`, `REF-NON-FF`, `REF-TAG-NEW`, `REF-TAG-MOVE`, `MODE-*`, `META-*`, `SIZE-*`, `CONTENT-*`) apply `deny` only when they already fire (`41d8035`, `82be3bf`). |
| C3 | `REQ-PG-005` | What does `approve` do with a SHA prefix that fits two different rejected SHAs? | It approves nothing and asks for more digits, like git. The same SHA rejected twice is not ambiguous; the newest push counts (`f223de4`). |
| C4 | `REQ-PG-032` | May a limit of the wrong kind (`max_skew: 600`, `max_files: '10m'` or `1.5`) load? | No. Limits are checked for the kind the rule reads when the config loads, so `check-config` rejects them instead of every push failing later (`7cee044`). |
| C5 | `REQ-PG-021`, `022`, `025`, `026` | Do the content rules see files whose path contains a space? | Yes. The path from the diff header is cut at the TAB git appends for such paths (`a1ef172`). |
| C6 | `REQ-PG-020` | What if a `.gitleaksignore` lies in the directory gitleaks scans? | The scan fails closed (internal error) instead of letting gitleaks load it or renaming it aside (`41f6ec6`). |
| C7 | `REQ-PG-026` | What does an empty `pages_branch` do? | It switches `CONTENT-PAGES-SCRIPT` off completely, with no fallback to `gh-pages`. Now documented in the spec and `push-guard.md` (`0f05967`). |

## Observations (questions, not failing tests)

These behaviours are within the spec's wording or on the safe side, but may not be intended:

1. `CONTENT-PAGES-SCRIPT` searches with `git grep -I`, so binary files are skipped; the spec says `-i -F`.
2. `RATE-LIMIT` counts rate-limited pushes too, so an agent that keeps pushing stays limited until it pauses for a full window.
3. `RATE-YELLOW-STREAK` counts yellow rejections per repo, not per agent and repo as REQ-PG-037 says. Same result while each repo has one agent.
4. `CONTENT-BLOB` treats long runs of `-` or `=` (e.g. Markdown rules) as base64.
5. Refs approved by a human are excluded from `REF-COUNT`.
6. `parseNumstat` accepts a truncated rename entry ending in a NUL. Git never produces this.

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
