#!/usr/bin/env bash
# Module-wide statement coverage, including the push-guard binary that the
# end-to-end tests build and run. This is the authoritative coverage number.
#
#   make coverage            # or: ./scripts/coverage.sh
#
# Method:
#   1. All packages with -coverpkg=./..., so every test counts for every package.
#   2. The cmd/push-guard tests again as a compiled test binary (go test -c),
#      run with GOCOVERDIR so the push-guard binary it builds is measured too.
#      Plain `go test ./...` does not collect that data.
#   3. Merge and print per-package and total coverage.
# Runs under nice with -p 1 to keep the shared machine responsive.
# Output goes to .cov/ (gitignored): all.out, per-package.txt, total.txt.
#
# COVERAGE_MIN (percent, default 0 = no check): fail if the total is lower.
# CI sets COVERAGE_MIN=85.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=.cov
DATA="$PWD/$OUT/data"
rm -rf "$OUT"
mkdir -p "$DATA"

GOFLAGS=-cover GOCOVERDIR="$DATA" nice -n 15 \
  go test -count=1 -p 1 -cover -coverpkg=./... ./... -args -test.gocoverdir="$DATA"

go test -c -cover -coverpkg=./... -o "$OUT/pg.test" ./cmd/push-guard
(cd cmd/push-guard && GOFLAGS=-cover GOCOVERDIR="$DATA" nice -n 15 \
  "../../$OUT/pg.test" -test.count=1 -test.gocoverdir="$DATA")

echo "== per package =="
go tool covdata percent -i="$DATA" | tee "$OUT/per-package.txt"
go tool covdata textfmt -i="$DATA" -o "$OUT/all.out"
echo "== total =="
go tool cover -func="$OUT/all.out" | tail -1 | tee "$OUT/total.txt"

COVERAGE_MIN=${COVERAGE_MIN:-0}
total=$(awk '{gsub("%","",$NF); print $NF}' "$OUT/total.txt")
if awk -v t="$total" -v m="$COVERAGE_MIN" 'BEGIN{exit !(t+0 < m+0)}'; then
  echo "coverage: total ${total}% is below COVERAGE_MIN ${COVERAGE_MIN}%" >&2
  exit 1
fi
echo "coverage: total ${total}% (minimum ${COVERAGE_MIN}%)"
