# Backup Guard: test specification

This document maps every requirement in [backup-guard.md](backup-guard.md#requirements) (`REQ-BG-001` to `REQ-BG-118`) to the automated tests that check it. Requirement tests are named `TestREQ_BG_<nnn>_…`, so `go test ./internal/backupguard ./cmd/backup-guard -run 'TestREQ_BG_081'` runs the tests of one requirement. Older tests that already covered a requirement before the numbering are listed with their own names.

Status is as of `main` @ `0d52412` (Peter's decisions of 2026-10-06 included) plus branch `qa/backup-guard-tests` (6.10.2026). **Red** means the test fails because the code does not meet the requirement yet; the tests are written against the spec, not against the current code.

## Summary

- 113 of 118 requirements have at least one test; `REQ-BG-114` to `118` (limits, 2026-10-11) are specified below and not written yet. `REQ-BG-071` (one pass, then exit) is covered implicitly by every `run` test in `cmd/backup-guard`.
- 85 requirement tests (65 from QA, 20 from Maintainer with the 2026-10-06 decisions), plus 12 older tests mapped below.
- 87 green, 0 red. `TestREQ_BG_081_OutputTail` was red until the fix on this branch (see Findings).

## Coverage

Coverage is measured for the whole module with `make coverage` (see [Test strategy](test-strategy.md#layers)); at `ae5e34a` (2026-10-11) it is **88.5 %**, with `internal/backupguard` and `cmd/backup-guard` between 92 % and 99.6 %. `make coverage` prints the full per-package breakdown.

### Earlier numbers (old method, not comparable)

Measured per package with plain `go test -cover`:

Statement coverage from `go test -cover`, before (`2be85f0`, numbering only), on `main` (`0d52412`, with Maintainer's decision tests) and with `qa/backup-guard-tests`:

| Package | `2be85f0` | `main` `0d52412` | + `qa/backup-guard-tests` |
| --- | --- | --- | --- |
| `internal/backupguard` | 75 % | 82.0 % | 86.6 % |
| `cmd/backup-guard` | 87 % | 86.7 % | 96.7 % |
| whole module | | | 63.4 % |

## Requirements and tests

| REQ | Area | Test | File | Status |
| --- | --- | --- | --- | --- |
| `REQ-BG-001` | Preflight | `TestREQ_BG_001_FailedPreflightSetsUpNoRepo` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-001` | Preflight | `TestRunFailsClosedWithoutEverref` | `cmd/backup-guard/main_test.go` | green |
| `REQ-BG-002` | Preflight | `TestRunFailsClosedWithoutEverref` | `cmd/backup-guard/main_test.go` | green |
| `REQ-BG-003` | Preflight | `TestREQ_BG_003_ConfigDirMustBeADirectory` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-003` | Preflight | `TestREQ_BG_003_004_023_UnreadableConfigEntries` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-003` | Preflight | `TestREQ_BG_003_RelativeConfigDirWithoutWorkingDir` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-004` | Preflight | `TestREQ_BG_004_BrokenDefaultsFailPreflight` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-005` | Prerequisites | `TestREQ_BG_005_GitMissing` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-006` | Prerequisites | `TestREQ_BG_006_007_GitVersion` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-007` | Prerequisites | `TestREQ_BG_006_007_GitVersion` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-008` | Prerequisites | `TestREQ_BG_008_018_EverrefLookup` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-009` | Prerequisites | `TestREQ_BG_009_EverrefNotFound` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-009` | Prerequisites | `TestPreflightFailsClosed` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-009` | Prerequisites | `TestREQ_BG_009_010_011_EverrefErrorMessages` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-010` | Prerequisites | `TestREQ_BG_010_VersionCommandFails` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-011` | Prerequisites | `TestREQ_BG_011_VersionLineFormat` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-012` | Prerequisites | `TestREQ_BG_012_OnlyTheMajorVersionIsCompared` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-012` | Prerequisites | `TestPreflightFailsClosed` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-013` | Prerequisites | `TestREQ_BG_013_EverrefVersionDefaultsTo100` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-014` | Preflight | `TestREQ_BG_014_NoRepos` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-014` | Preflight | `TestPreflightFailsClosed` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-015` | Preflight | `TestREQ_BG_015_FailedPreflightIsRecordedAndWarned` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-015` | Preflight | `TestREQ_BG_015_RunRecordsFailedPreflight` | `cmd/backup-guard/req_test.go` | green |
| `REQ-BG-015` | Preflight | `TestREQ_BG_015_PreflightRunOutcomes` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-016` | Configuration | `TestLoadDefaults` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-017` | Configuration | `TestREQ_BG_017_OnlyKnownDefaultsKeys` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-017` | Configuration | `TestLoadDefaults` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-018` | Configuration | `TestREQ_BG_008_018_EverrefLookup` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-019` | Configuration | `TestREQ_BG_019_021_EmptyValues` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-020` | Configuration | `TestREQ_BG_020_RelativeStateDir` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-021` | Configuration | `TestREQ_BG_019_021_EmptyValues` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-022` | Configuration | `TestREQ_BG_022_Timeout` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-022` | Configuration | `TestLoadDefaults` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-023` | Repo configuration | `TestREQ_BG_023_RepoNeedsBackupYAML` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-024` | Repo configuration | `TestREQ_BG_024_RepoNames` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-024` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-025` | Repo configuration | `TestREQ_BG_025_OnlyKnownRepoKeys` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-025` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-026` | Repo configuration | `TestREQ_BG_026_RemoteRequired` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-026` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-027` | Repo configuration | `TestREQ_BG_027_RemoteKinds` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-027` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-028` | Repo configuration | `TestREQ_BG_028_RelativeLocalRemote` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-028` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-029` | Repo configuration | `TestREQ_BG_029_RemoteNeedsCredential` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-029` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-030` | Repo configuration | `TestREQ_BG_030_LocalRemoteNoCredential` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-030` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-031` | Repo configuration | `TestREQ_BG_031_UsernameOnlyForHTTPS` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-031` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-032` | Repo configuration | `TestREQ_BG_032_UsernameCharset` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-032` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-033` | Repo configuration | `TestREQ_BG_033_KnownHostsOnlyForSSH` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-033` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-034` | Repo configuration | `TestREQ_BG_034_RelativeCredentialPaths` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-034` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-035` | Branches and tags | `TestREQ_BG_035_ExcludeAnchored` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-035` | Branches and tags | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-036` | Repo configuration | `TestREQ_BG_036_InvalidExcludeRegexp` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-036` | Repo configuration | `TestLoadRepo` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-037` | Preflight | `TestREQ_BG_037_InvalidBackupYamlFailsPreflight` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-038` | Credentials | `TestREQ_BG_038_039_042_SSHCommand` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-039` | Credentials | `TestREQ_BG_038_039_042_SSHCommand` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-040` | Credentials | `TestREQ_BG_040_041_HTTPSHelper` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-040` | Credentials | `TestTokenFileReachesEverrefFetch` | `internal/backupguard/token_test.go` | green |
| `REQ-BG-041` | Credentials | `TestREQ_BG_040_041_HTTPSHelper` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-041` | Credentials | `TestTokenFileReachesEverrefFetch` | `internal/backupguard/token_test.go` | green |
| `REQ-BG-042` | Credentials | `TestREQ_BG_038_039_042_SSHCommand` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-042` | Credentials | `TestTokenFileReachesEverrefFetch` | `internal/backupguard/token_test.go` | green |
| `REQ-BG-043` | Branches and tags | `TestREQ_BG_043_LsRemoteFailureFails` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-044` | Branches and tags | `TestREQ_BG_044_OnlyHeadsAreBranches` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-045` | Branches and tags | `TestREQ_BG_045_AddOnFirstSight` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-045` | Branches and tags | `TestREQ_BG_045_ProtectedBranchesIgnoreEmptyNames` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-046` | Branches and tags | `TestREQ_BG_046_AddBatchesOf100` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-046` | Branches and tags | `TestREQ_BG_046_048_BranchesSortedAndFailuresListed` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-047` | Branches and tags | `TestAddRetriesSinglyAndRunFailureNotifies` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-048` | Branches and tags | `TestAddRetriesSinglyAndRunFailureNotifies` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-049` | Branches and tags | `TestAddRetriesSinglyAndRunFailureNotifies` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-050` | Branches and tags | `TestREQ_BG_050_051_ProtectionsStay` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-051` | Branches and tags | `TestREQ_BG_050_051_ProtectionsStay` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-051` | Branches and tags | `TestREQ_BG_051_ExcludedLaterStaysBackedUp` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-052` | Branches and tags | `TestREQ_BG_052_TagsNotExcludable` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-053` | Branches and tags | `TestREQ_BG_053_BridgedTagsOnOnce` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-054` | Branches and tags | `TestREQ_BG_054_NoBranchesFails` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-054` | Branches and tags | `TestEmptyRemoteIsAFailure` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-055` | Branches and tags | `TestEndToEndWithEverref` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-056` | Branches and tags | `TestEndToEndWithEverref` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-057` | Branches and tags | `TestEndToEndWithEverref` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-058` | Branches and tags | `TestEndToEndWithEverref` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-059` | Setup and state | `TestREQ_BG_059_060_061_BridgeLayout` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-060` | Setup and state | `TestREQ_BG_059_060_061_BridgeLayout` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-060` | Setup and state | `TestREQ_BG_060_BrokenBridgeRemoteIsRestored` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-061` | Setup and state | `TestREQ_BG_059_060_061_BridgeLayout` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-062` | Setup and state | `TestBridgeRefusesChangedRemote` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-063` | Setup and state | `TestREQ_BG_063_SetupInTempDirs` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-063` | Setup and state | `TestInterruptedFirstRun` | `internal/backupguard/interrupt_test.go` | green |
| `REQ-BG-063` | Setup and state | `TestREQ_BG_063_065_SetupErrorsLeaveNoHalfBuiltRepo` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-064` | Setup and state | `TestREQ_BG_064_LeftoversRemoved` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-064` | Setup and state | `TestInterruptedFirstRun` | `internal/backupguard/interrupt_test.go` | green |
| `REQ-BG-065` | Setup and state | `TestREQ_BG_065_IncompleteDirs` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-065` | Setup and state | `TestREQ_BG_065_UnreadableStateDirFailsTheRepo` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-066` | Setup and state | `TestREQ_BG_066_RefsOnlyGrow` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-067` | Run | `TestREQ_BG_067_068_088_RunSelection` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-068` | Run | `TestREQ_BG_067_068_088_RunSelection` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-068` | Run | `TestREQ_BG_068_UnknownRepoFailsTheRun` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-069` | Run | `TestREQ_BG_069_FailedRepoDoesNotStopOthers` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-070` | Run | `TestREQ_BG_070_072_RunAllWithoutTrigger` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-070` | Run | `TestREQ_BG_070_080_RunAllOutputIsCounted` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-071` | Trigger | every `backup-guard run` test in `cmd/backup-guard` (one pass, then exit) | `cmd/backup-guard` | green (implicit) |
| `REQ-BG-072` | Trigger | `TestREQ_BG_070_072_RunAllWithoutTrigger` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-073` | Trigger | `TestREQ_BG_073_ExampleTimer` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-074` | Run | `TestREQ_BG_074_TimeoutKillsEverref` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-074` | Run | `TestREQ_BG_074_TimeoutIsNamed` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-074` | Run | `TestREQ_BG_074_081_RunOutputAndTimeout` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-074` | Run | `TestREQ_BG_074_ChildHoldingThePipeDoesNotBlock` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-075` | Locking | `TestREQ_BG_075_LockedRepoIsSkippedWithoutWaiting` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-075` | Locking | `TestREQ_BG_075_LockThatCannotBeTakenFailsTheRepo` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-076` | Locking | `TestREQ_BG_076_LockReleasedOnKill` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-077` | Interrupted runs | `TestInterruptedFirstRun` | `internal/backupguard/interrupt_test.go` | green |
| `REQ-BG-078` | State | `TestREQ_BG_078_079_JournalLine` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-079` | State | `TestREQ_BG_078_079_JournalLine` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-080` | State | `TestCountEvents` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-081` | State | `TestREQ_BG_081_FailedAddCarriesEverrefOutput` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-081` | State | `TestREQ_BG_081_OutputTail` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-081` | State | `TestREQ_BG_081_TailKeepsTheEnd` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-082` | State | `TestREQ_BG_082_JournalAppendFails` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-083` | Warnings | `TestREQ_BG_083_084_Warnings` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-083` | Warnings | `TestAddRetriesSinglyAndRunFailureNotifies` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-084` | Warnings | `TestREQ_BG_083_084_Warnings` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-085` | Warnings | `TestREQ_BG_085_NoNotifyCommand` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-086` | Warnings | `TestREQ_BG_086_NotifyFailureRecorded` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-087` | Commands | `TestREQ_BG_087_Version` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-088` | Commands | `TestREQ_BG_067_068_088_RunSelection` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-089` | Commands | `TestREQ_BG_089_090_ExitCodes` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-089` | Commands | `TestREQ_BG_089_SubcommandHelpIsNotAnError` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-090` | Commands | `TestREQ_BG_089_090_ExitCodes` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-090` | Commands | `TestREQ_BG_090_InvalidRepoConfigExitsOne` | `cmd/backup-guard/req_test.go` | green |
| `REQ-BG-091` | Commands | `TestREQ_BG_091_UsageErrors` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-092` | check-config | `TestREQ_BG_092_093_CheckConfigPreflightNoState` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-093` | check-config | `TestREQ_BG_092_093_CheckConfigPreflightNoState` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-094` | check-config | `TestREQ_BG_094_CheckConfigReportsEveryBrokenBackupYaml` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-095` | check-config | `TestREQ_BG_095_096_099_CheckConfigProblems` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-095` | check-config | `TestREQ_BG_095_096_099_CheckConfigProblemsListed` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-096` | check-config | `TestREQ_BG_095_096_099_CheckConfigProblems` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-097` | check-config | `TestREQ_BG_097_CheckConfigNotes` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-097` | check-config | `TestREQ_BG_097_CheckConfigNotesDefaultEverrefVersion` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-097` | check-config | `TestREQ_BG_097_098_CheckConfigNotesAndRemote` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-097` | check-config | `TestREQ_BG_097_CredentialModeNote` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-098` | check-config | `TestREQ_BG_098_CheckConfigRemote` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-099` | check-config | `TestREQ_BG_095_096_099_CheckConfigProblems` | `cmd/backup-guard/req_cmd_test.go` | green |
| `REQ-BG-099` | check-config | `TestREQ_BG_099_EveryRepoProblemListed` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-100` | Looking and restoring | `TestREQ_BG_100_101_EverrefToolsWorkAndRemoteUntouched` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-101` | Looking and restoring | `TestREQ_BG_100_101_EverrefToolsWorkAndRemoteUntouched` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-102` | No runtime downloads | `TestREQ_BG_102_NoRuntimeDownloads` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-103` | Provisioning | `TestREQ_BG_103_104_InstallScriptChecks` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-103` | Provisioning | `TestInstallScriptPin` | `internal/backupguard/backupguard_test.go` | green |
| `REQ-BG-104` | Provisioning | `TestREQ_BG_103_104_InstallScriptChecks` | `internal/backupguard/req_bg_test.go` | green |
| `REQ-BG-105` | Preflight | `TestREQ_BG_105_UnloadableDefaultsRecordNothing` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-106` | Preflight | `TestREQ_BG_106_UnwritableStateDirStillFails` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-107` | Preflight | `TestREQ_BG_107_InvalidRepoFolderNameFailsPreflight` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-108` | Preflight | `TestREQ_BG_108_PreflightReportsEveryProblem` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-109` | Locking | `TestREQ_BG_109_SkippedRepoIsRecordedAndWarned` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-110` | check-config | `TestREQ_BG_110_CheckConfigRecordsNoPreflightFailure` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-111` | Warnings | `TestREQ_BG_111_NotifyCommandRelativeToConfigDir` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-112` | Prerequisites | `TestREQ_BG_112_SourceBuildNeedsExplicitDev` | `internal/backupguard/req_decisions_test.go` | green |
| `REQ-BG-113` | Configuration | `TestREQ_BG_113_EverrefVersionMustBeAVersionOrDev` | `internal/backupguard/req_decisions_test.go` | green |

## Tests found by mutation testing

The mutation campaign of October 2026 added tests in `internal/backupguard/backupguard_mutants_test.go`, `backupguard_mutants2_test.go` and `cmd/backup-guard/main_mutants_test.go`. They stay, but move to where their behaviour is tested and are named after it, the same way as for Push Guard ([Push Guard test specification](push-guard-testspec.md#tests-found-by-mutation-testing), [Test strategy § Where tests go](test-strategy.md#where-tests-go)). "Merge" means: add the cases as table entries or subtests to the named test. The REQ fit is QA's proposal; the maintainer may correct it while moving if the test body shows a better one. Afterwards each REQ test gets a row in the table above.

### internal/backupguard

| Old test | REQ | New name | Target file |
| --- | --- | --- | --- |
| `TestREQ_BG_097_CheckConfigReport` | `REQ-BG-097`, `098` | `TestREQ_BG_097_098_CheckConfigNotesAndRemote` | `internal/backupguard/req_bg_test.go` |
| `TestREQ_BG_097_CheckConfigProblems` | `REQ-BG-095`, `096`, `099` | `TestREQ_BG_095_096_099_CheckConfigProblemsListed` | `internal/backupguard/req_bg_test.go` |
| `TestREQ_BG_097_CheckConfigLines` | `REQ-BG-099` | `TestREQ_BG_099_EveryRepoProblemListed` (64 repos with a missing credential) | `internal/backupguard/req_bg_test.go` |
| `TestREQ_BG_097_CredentialModeNote` | `REQ-BG-097` | keep | `internal/backupguard/req_bg_test.go` |
| `TestREQ_BG_015_PreflightRunOutcomes` | `REQ-BG-015` | keep | `internal/backupguard/req_decisions_test.go` |
| `TestMutantsPreflightProblemCount` | `REQ-BG-108` | merge into `TestREQ_BG_108_PreflightReportsEveryProblem` | `internal/backupguard/req_decisions_test.go` |
| `TestMutantsLoadErrors`, `TestMutantsRepoFileIsDirectory` | `REQ-BG-003`, `004`, `023` | `TestREQ_BG_003_004_023_UnreadableConfigEntries` (merged) | `internal/backupguard/req_bg_test.go` |
| `TestMutantsLoadDefaultsErrors` | `REQ-BG-003`, `016`, `022` | merge into `TestLoadDefaults` | `internal/backupguard/backupguard_test.go` |
| `TestMutantsRelativeConfigDirWithoutWorkingDir` | `REQ-BG-003` | `TestREQ_BG_003_RelativeConfigDirWithoutWorkingDir` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsReposAndLoadRepo` | `REQ-BG-023`, `036` | merge into `TestLoadRepo` | `internal/backupguard/backupguard_test.go` |
| `TestMutantsEverrefConstantsAndMajorVersion` | `REQ-BG-012` | major-version cases: merge into `TestREQ_BG_012_OnlyTheMajorVersionIsCompared`; exit-code constants: `TestEverrefExitCodes` | `internal/backupguard/req_decisions_test.go` / `backupguard_test.go` |
| `TestMutantsFindEverrefMessages` | `REQ-BG-009`–`011`, `112` | `TestREQ_BG_009_010_011_EverrefErrorMessages` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsEverrefRun`, `TestMutantsRunOutputLimitsAndTimeout` | `REQ-BG-074`, `081` | `TestREQ_BG_074_081_RunOutputAndTimeout` (merged) | `internal/backupguard/req_decisions_test.go` |
| `TestMutantsWaitDelay` | `REQ-BG-074`, `086` | `TestREQ_BG_074_ChildHoldingThePipeDoesNotBlock` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsTruncateAndRunErrorUnwrap`, `TestMutantsTailAndTruncate` | `REQ-BG-081` | tail/truncate cases: merge into `TestREQ_BG_081_TailKeepsTheEnd`; `RunError` unwrap: `TestRunError_UnwrapsExecError` | `internal/backupguard/req_decisions_test.go` / `backupguard_test.go` |
| `TestMutantsAddFailureReasonsAndOutput` | `REQ-BG-048`, `081` | merge into `TestREQ_BG_081_FailedAddCarriesEverrefOutput` | `internal/backupguard/req_decisions_test.go` |
| `TestMutantsBranchesSortedAndFailuresListed` | `REQ-BG-046`, `048` | `TestREQ_BG_046_048_BranchesSortedAndFailuresListed` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsProtectedBranchesSkipsEmptyName` | `REQ-BG-045` | `TestREQ_BG_045_ProtectedBranchesIgnoreEmptyNames` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsTagsCheckedAgainstEveryValue` | `REQ-BG-053` | merge into `TestREQ_BG_053_BridgedTagsOnOnce` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsListRefsErrorIsWrapped` | `REQ-BG-043` | merge into `TestREQ_BG_043_LsRemoteFailureFails` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsRunAllOutput` | `REQ-BG-070`, `080` | `TestREQ_BG_070_080_RunAllOutputIsCounted` (kept apart from `TestCountEvents`, which checks only the event parser) | `internal/backupguard/req_bg_test.go` |
| `TestMutantsCreateOnceErrorPaths`, `TestMutantsCreateOnceStatAndRemoveErrors` | `REQ-BG-063`, `065` | `TestREQ_BG_063_065_SetupErrorsLeaveNoHalfBuiltRepo` (merged) | `internal/backupguard/req_bg_test.go` |
| `TestMutantsSetupLeftoverThatCannotBeRemoved` | `REQ-BG-064` | merge into `TestREQ_BG_064_LeftoversRemoved` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsUnreadableStateDirs` | `REQ-BG-065`, `069` | `TestREQ_BG_065_UnreadableStateDirFailsTheRepo` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsRunRepoSetupFailures` | `REQ-BG-060`, `068`, `075` | `TestREQ_BG_060_BrokenBridgeRemoteIsRestored`; unknown-repo case: `TestREQ_BG_068_UnknownRepoFailsTheRun`; the lock-failure case checks `REQ-BG-075` and moved to `TestREQ_BG_075_LockThatCannotBeTakenFailsTheRepo` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsLockAndJournalErrors`, `TestMutantsLockPathIsDirectory`, `TestMutantsAppendJournalOnDirectory` | `REQ-BG-075`, `082` | split: `TestREQ_BG_075_LockThatCannotBeTakenFailsTheRepo`; journal cases merged into `TestREQ_BG_082_JournalAppendFails` | `internal/backupguard/req_bg_test.go` |
| `TestMutantsNotifyArgsAndFailure` | `REQ-BG-083`, `086` | merge into `TestREQ_BG_086_NotifyFailureRecorded` (argument check as a subtest) | `internal/backupguard/req_bg_test.go` |
| `TestMutantsStatePermissions` | none | `TestStatePermissions` | `internal/backupguard/backupguard_test.go` |

### cmd/backup-guard

| Old test | REQ | New name | Target file |
| --- | --- | --- | --- |
| `TestMutantsUsageGoesToStderr`, `TestMutantsFlagErrorsUseStderrWriter` | `REQ-BG-091` | merge into `TestREQ_BG_091_UsageErrors` (stdout stays empty) | `cmd/backup-guard/req_cmd_test.go` |
| `TestMutantsSubcommandHelpExitsZero` | `REQ-BG-089`, `091` | `TestREQ_BG_089_SubcommandHelpIsNotAnError` | `cmd/backup-guard/req_cmd_test.go` |
| `TestMutantsParseInterleavedPositionals` | `REQ-BG-088` | merge into `TestREQ_BG_067_068_088_RunSelection` | `cmd/backup-guard/req_cmd_test.go` |
| `TestMutantsPreflightErrorWraps` | none | `TestRun_PreflightErrorKeepsItsCause` | `cmd/backup-guard/main_test.go` |

## Planned: limits (2026-10-11)

Tests for the limit requirements Peter approved on 2026-10-11. Every bound is tested just inside and just outside. Status: **planned**; the maintainer writes the tests (red first), then the code.

| REQ | Test | File | Cases |
| --- | --- | --- | --- |
| `REQ-BG-114` | `TestREQ_BG_114_TimeoutBounds` | `internal/backupguard/req_bg_test.go` | `59s` refused, `1m` loads, `24h` loads, `24h0m1s` refused; `check-config` reports it. `TestREQ_BG_022_Timeout` keeps the cases for zero, negative and non-durations. |
| `REQ-BG-115` | `TestREQ_BG_115_EventsCountedForLargeOutput` | `internal/backupguard/req_bg_test.go` | Stand-in everref whose `run --all` prints 3 MiB of event lines (known numbers of `[new]`, rewrites and tombstones): the counts in `backup.jsonl` are exact; on failure `output` still holds the end of the output (`REQ-BG-081` cut applies after). |
| `REQ-BG-116` | `TestREQ_BG_116_VersionOutputBounded` | `internal/backupguard/req_bg_test.go` | `--version` answering a valid first line padded to exactly 1 MiB passes; 1 MiB + 1 byte fails the preflight. |
| `REQ-BG-117` | `TestREQ_BG_117_AddAndNotifyOutputKeepTail` | `internal/backupguard/req_bg_test.go` | Stand-in `add` that writes 3 MiB and fails: `output` and `add_failed` come from the end (last marker present, first marker absent). A `notify.command` that writes 3 MiB: the run's result is unchanged and the guard's memory stays bounded. |
| `REQ-BG-118` | `TestREQ_BG_118_ShortErrorText` | `internal/backupguard/req_bg_test.go` | A program error text of 300 characters is kept whole, 301 is shortened. The 5 s after a kill is the existing `TestREQ_BG_074_ChildHoldingThePipeDoesNotBlock`, which gets `REQ-BG-118` in its name. |

## Findings

### Open

None.

### Fixed

- Everything Peter decided on 2026-10-06 (`REQ-BG-001`, `012`, `013`, `015`, `016`, `037`, `075`, `081` for `add`, `097`, `105` to `113`), fixed by Maintainer in `ec030cc` and `0d52412`.
- **`REQ-BG-081`, end of everref's output was lost** (`TestREQ_BG_081_OutputTail`). `run.go` took the last 40 lines but `truncate(…, 4000)` kept the *first* 4000 bytes. Now the last 4000 bytes are kept, cut at a line start (or at least a UTF-8 boundary) and marked with a leading `…`, in `backup.jsonl` and the warning; the same for the short error text and `add_failed` reasons.

## Observations (no requirement broken)

- **`REQ-BG-074`, timeout cause not shown** (addressed). A run killed by `timeout` reported only `git-everref … run --all: exit -1:`. The error in `backup.jsonl` and the warning now starts with `timeout <timeout> (defaults.yaml) exceeded, killed:` (`TestREQ_BG_074_TimeoutIsNamed`).
- **`REQ-BG-094` now overlaps `REQ-BG-037`.** Since the preflight loads every `backup.yaml`, a broken one already fails the preflight of `check-config`; `REQ-BG-094` is met through the preflight (`REQ-BG-108` reports all of them). The test checks that every broken file is named and exit is `1`. The wording of `REQ-BG-094` could say so.

## Untested on purpose

- The 30 s limit for `git-everref --version` (`REQ-BG-010`) and the 1-minute limit for `notify.command` (`REQ-BG-086`): testing them would take 30 s or 1 min per run. The tests check the failure paths (non-zero exit, notifier error) instead.

## How to run

```sh
go test ./internal/backupguard ./cmd/backup-guard            # all tests
go test ./internal/backupguard -run 'TestREQ_BG_0[78]'        # REQ-BG-070 to 089
make coverage                                                 # coverage (module-wide)
```

Tests that call `realEverref(t)` use the installed `git-everref` (v1.x); all others use a shell stand-in for everref (`qaFake`, `qaEverref`) that logs its arguments and environment.
