# Local QA helpers. Mutation testing is intentionally NOT part of CI.
#
#   make mutate
#   make mutate WORKERS=2 DRY_RUN=1
#   make mutate PACKAGES='./internal/config' TIMEOUT_MIN=5s
#   make mutate PKGS='./internal/backupguard'
#   make fuzz
#   make fuzz FUZZTIME=5s
#
# See scripts/mutate.sh header and .gomutants.yml for tunables.
# `make fuzz` runs property/fuzz tests under //go:build fuzz (seeds + rapid).
# With FUZZTIME set, each Go fuzzer also runs for that duration (`-fuzz`).

.PHONY: mutate fuzz

WORKERS ?= 2
TEST_CPU ?= 1
TIMEOUT_COEFFICIENT ?= 10
TIMEOUT_MIN ?= 2s
TIMEOUT_MARGIN ?= 3
ADAPTIVE_TIMEOUT ?= 1
DRY_RUN ?= 0
PACKAGES ?= ./internal/rules ./internal/journal ./internal/config
PKGS ?=
OUTPUT ?= mutation-report.json
FUZZTIME ?=

mutate:
	WORKERS=$(WORKERS) TEST_CPU=$(TEST_CPU) TIMEOUT_COEFFICIENT=$(TIMEOUT_COEFFICIENT) \
		TIMEOUT_MIN=$(TIMEOUT_MIN) TIMEOUT_MARGIN=$(TIMEOUT_MARGIN) \
		ADAPTIVE_TIMEOUT=$(ADAPTIVE_TIMEOUT) DRY_RUN=$(DRY_RUN) \
		PACKAGES='$(PACKAGES)' PKGS='$(PKGS)' OUTPUT=$(OUTPUT) ./scripts/mutate.sh

# Seeds and rapid property tests. Optional FUZZTIME runs each Go fuzzer that long.
fuzz:
	go test -tags=fuzz ./...
ifneq ($(FUZZTIME),)
	@for pkg in $$(go list -tags=fuzz ./...); do \
		for fuzz in $$(go test -tags=fuzz -list '^Fuzz' "$$pkg" 2>/dev/null | grep '^Fuzz' || true); do \
			echo "fuzz $$pkg $$fuzz ($(FUZZTIME))"; \
			go test -tags=fuzz -fuzz="^$$fuzz\$$" -fuzztime=$(FUZZTIME) "$$pkg" || exit 1; \
		done; \
	done
endif
