# Local QA helpers. Mutation testing is intentionally NOT part of CI.
#
#   make mutate
#   make mutate WORKERS=2 DRY_RUN=1
#   make mutate PACKAGES='./internal/config' TIMEOUT_MIN=5s
#   make mutate PKGS='./internal/backupguard'
#
# See scripts/mutate.sh header and .gomutants.yml for tunables.

.PHONY: mutate

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

mutate:
	WORKERS=$(WORKERS) TEST_CPU=$(TEST_CPU) TIMEOUT_COEFFICIENT=$(TIMEOUT_COEFFICIENT) \
		TIMEOUT_MIN=$(TIMEOUT_MIN) TIMEOUT_MARGIN=$(TIMEOUT_MARGIN) \
		ADAPTIVE_TIMEOUT=$(ADAPTIVE_TIMEOUT) DRY_RUN=$(DRY_RUN) \
		PACKAGES='$(PACKAGES)' PKGS='$(PKGS)' OUTPUT=$(OUTPUT) ./scripts/mutate.sh
