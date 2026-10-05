#!/usr/bin/env bash
# Mutation testing with gomutants (https://github.com/szhekpisov/gomutants).
# Local / QA only — not wired into CI or the build pipeline.
#
# Usage:
#   make mutate
#   scripts/mutate.sh
#   WORKERS=2 scripts/mutate.sh
#   DRY_RUN=1 scripts/mutate.sh
#   PACKAGES='./internal/config' scripts/mutate.sh
#   PKGS='./internal/backupguard' scripts/mutate.sh   # alias for PACKAGES
#
# Tunables (env or `make mutate WORKERS=…`):
#   WORKERS              --workers / -w: parallel mutant workers (default: 2)
#   TEST_CPU             --test-cpu: passed to go test -cpu (default: 1; 0 = omit)
#   TIMEOUT_COEFFICIENT  --timeout-coefficient: global ceiling = baseline × this
#                          (default: 10). With adaptive timeouts (default on),
#                          also the upper clamp for per-mutant deadlines.
#   TIMEOUT_MIN          --timeout-min: floor for adaptive per-mutant timeout
#                          (default: 2s). Example: TIMEOUT_MIN=5s
#   TIMEOUT_MARGIN       --timeout-margin: adaptive multiplier on selected
#                          test durations (default: 3)
#   ADAPTIVE_TIMEOUT     --adaptive-timeout: 1 (default) or 0 to use a single
#                          global ceiling only
#   DRY_RUN=1            pass --dry-run
#   PACKAGES / PKGS      space-separated package patterns (default: the three
#                          Push Guard packages below). Set to
#                          './internal/backupguard' when QA mutates Backup Guard.
#   GOMUTANTS_VERSION    go install version pin (default: v0.6.1)
#   OUTPUT               --output JSON report path (default: mutation-report.json)
#
# Project defaults also live in .gomutants.yml (workers, exclusions, timeouts).
# Env / make vars override the YAML for a single run.
#
# Requirements / limitations:
#   - gomutants needs Go >= 1.26.1 to build and prefers a 1.26+ toolchain when
#     shelled out to `go test`. This repo's go.mod is 1.24; GOTOOLCHAIN=auto
#     (default here) downloads a newer toolchain for the tool install/run.
#   - Do not point this at cmd/push-guard E2E — too expensive. Unit tests in the
#     mutant's own package only.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

GOMUTANTS_VERSION=${GOMUTANTS_VERSION:-v0.6.1}
WORKERS=${WORKERS:-2}
TEST_CPU=${TEST_CPU:-1}
TIMEOUT_COEFFICIENT=${TIMEOUT_COEFFICIENT:-10}
TIMEOUT_MIN=${TIMEOUT_MIN:-2s}
TIMEOUT_MARGIN=${TIMEOUT_MARGIN:-3}
ADAPTIVE_TIMEOUT=${ADAPTIVE_TIMEOUT:-1}
OUTPUT=${OUTPUT:-mutation-report.json}
# PKGS is an alias for PACKAGES (QA convenience).
if [[ -n "${PKGS:-}" && -z "${PACKAGES:-}" ]]; then
	PACKAGES=$PKGS
fi
PACKAGES=${PACKAGES:-"./internal/rules ./internal/journal ./internal/config"}

export PATH="$(go env GOPATH)/bin:${PATH:-}"
export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"

if ! command -v gomutants >/dev/null 2>&1; then
	echo "mutate: installing gomutants@${GOMUTANTS_VERSION}" >&2
	# Explicit newer toolchain so install succeeds on hosts with Go 1.24.
	GOTOOLCHAIN=go1.26.1 go install "github.com/szhekpisov/gomutants@${GOMUTANTS_VERSION}"
fi

args=(
	--workers="${WORKERS}"
	--timeout-coefficient="${TIMEOUT_COEFFICIENT}"
	--timeout-min="${TIMEOUT_MIN}"
	--timeout-margin="${TIMEOUT_MARGIN}"
	--output="${OUTPUT}"
)
if [[ "${TEST_CPU}" != "0" ]]; then
	args+=(--test-cpu="${TEST_CPU}")
fi
if [[ "${ADAPTIVE_TIMEOUT}" == "0" ]]; then
	args+=(--adaptive-timeout=false)
fi
if [[ "${DRY_RUN:-0}" == "1" ]]; then
	args+=(--dry-run)
fi

# shellcheck disable=SC2086
echo "=== gomutants ${args[*]} ${PACKAGES} ==="
# shellcheck disable=SC2086
nice -n 10 gomutants "${args[@]}" ${PACKAGES}
