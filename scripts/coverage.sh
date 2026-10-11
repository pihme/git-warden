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
# Output goes to .cov/ (gitignored).
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
go tool covdata percent -i="$DATA"
go tool covdata textfmt -i="$DATA" -o "$OUT/all.out"
echo "== total =="
go tool cover -func="$OUT/all.out" | tail -1
