.PHONY: build build-cli build-server clean compiler-bench dataframe-demo dataframe-profile dataframe-boundaries dataframe-test conformance generate generate-openapi generate-fhir openapi-check test test-scripts dev-test docker-build docker-run acceptance-real acceptance-performance demo-up demo-down demo-smoke demo-browser-smoke repository-up release-ui dev dev-rebuild dev-doctor verify-current verify-fast dev-down verify-api test-ui test-go test-integration e2e

GO ?= go
GO_VERSION ?= 1.27.4
GO_TOOLCHAIN ?= go$(GO_VERSION)
GOCACHE_DIR ?= $(CURDIR)/.gocache
GOFLAGS ?=
OAPI_CODEGEN_VERSION ?= v2.8.0
IMAGE ?= arango-fhir-proto:local
LOOM_URL ?= http://127.0.0.1:8080
DATAFRAME_REPEAT ?= 1
DATAFRAME_LIMIT ?= 0
DATAFRAME_TIMEOUT ?= 5m
DATAFRAME_PRINT_RESPONSE ?= false
DATAFRAME_REQUEST ?=
DATAFRAME_PROFILE_VARIABLES ?=
DATAFRAME_PROFILE_LIMIT ?= 1000

# Make treats the positional version as a second goal. Reject every ambiguous
# form while parsing so no release recipe can start with the wrong version.
ifneq ($(filter release-ui,$(MAKECMDGOALS)),)
ifneq ($(firstword $(MAKECMDGOALS)),release-ui)
$(error Usage: make release-ui X.Y.Z)
endif
ifneq ($(words $(MAKECMDGOALS)),2)
$(error Usage: make release-ui X.Y.Z)
endif
ifeq ($(origin VERSION),command line)
$(error Usage: do not set VERSION=; run make release-ui X.Y.Z)
endif
release-ui-version := $(word 2,$(MAKECMDGOALS))
.PHONY: $(release-ui-version)
$(release-ui-version):
	@:
endif

build: build-cli build-server

release-ui:
	./scripts/release-loom-ui.sh release "$(release-ui-version)"

generate: generate-fhir generate-openapi

build-cli:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) build $(GOFLAGS) -o bin/arango-fhir-proto ./cmd/arango-fhir-proto

build-server:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) build $(GOFLAGS) -o bin/arango-fhir-server ./cmd/arango-fhir-server

generate-fhir:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/generate -structs-out fhir/model -metadata-out fhir/schema/generated.go
	gofmt -w fhir/model/*.go fhir/schema/generated.go

generate-openapi:
	mkdir -p api/loomapi $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) --config api/oapi-codegen.yaml api/openapi.yaml

openapi-check:
	bash scripts/check_openapi_route_ownership.sh
	python3 scripts/audit-http-500-responses.py >/dev/null

test:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./... -count=1
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) GO="$(GO)" bash tests/scripts/test-go-script.sh local-preview-index-migration $(GOFLAGS) -count=1
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run $(GOFLAGS) ./scripts/check_fhir_semantic_resource_types.go
	node --test tests/scripts/loom-dev.test.mjs
	bash tests/scripts/test_acceptance_real_cleanup.sh

dev-test:
	node --test tests/scripts/loom-dev.test.mjs

test-scripts:
	python3 -m unittest discover -s tests/scripts -p 'test_*.py'

compiler-bench:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./tests/conformance -run '^$$' -bench '^BenchmarkCompilerOracle$$' -benchmem

# Requires `arango-fhir-server --no-auth` and a loaded META project. Prints the
# actual REST dataframe response and per-request wall-clock timings.
dataframe-demo:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-query -url $(LOOM_URL) -request "$(DATAFRAME_REQUEST)" -repeat $(DATAFRAME_REPEAT) -limit $(DATAFRAME_LIMIT) -timeout $(DATAFRAME_TIMEOUT) -print-response=$(DATAFRAME_PRINT_RESPONSE)

# Requires a loaded META fixture database. Compiles the checked-in GDC fixture,
# writes exact rendered AQL, then runs Arango EXPLAIN and PROFILE 2.
dataframe-profile:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-profile -variables "$(DATAFRAME_PROFILE_VARIABLES)" -limit $(DATAFRAME_PROFILE_LIMIT)

dataframe-boundaries:
	./scripts/check_dataframe_package_boundaries.sh

dataframe-test: dataframe-boundaries
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./internal/dataframe/spec ./internal/dataframe/semantic ./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower ./internal/dataframe/compiler/optimize ./internal/dataframe/compiler/render/aql ./internal/dataframe/compiler ./internal/dataframe/execution -count=1

conformance:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./tests/conformance/... -count=1

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 $(IMAGE)

# Full real-data acceptance path. Rebuild the canonical loom-demo deployment
# without replacing its data, then verify the locked fixture in an isolated
# Compose project that is removed after evidence capture.
acceptance-real:
	./scripts/acceptance-real.sh

acceptance-performance:
	./scripts/acceptance-performance.sh

demo-up:
	./scripts/demo-up.sh

demo-down:
	./scripts/demo-down.sh

demo-smoke:
	./scripts/demo-smoke.sh

demo-browser-smoke:
	./scripts/demo-browser-smoke.sh

repository-up:
	./scripts/loom-repo-up.sh --repository "$(if $(REPOSITORY),$(REPOSITORY),$(CURDIR))"

# Development-only Compose target. It has its own project, volumes, ports, and
# fixture. Use dev-rebuild after dependency or toolchain changes.
dev:
	node scripts/loom-dev.mjs dev

dev-rebuild:
	node scripts/loom-dev.mjs dev-rebuild

dev-doctor:
	node scripts/loom-dev.mjs dev-doctor

.PHONY: dev-down clean
dev-down:
	node scripts/loom-dev.mjs dev-down

clean:
	rm -rf bin

# Direct product test entry points. Integration tests require owned Arango and ClickHouse services.
test-ui:
	npm --prefix ui test

.PHONY: lint-go-comments
lint-go-comments:
	$(GO) run ./cmd/check-go-comments

test-go:
	$(GO) test ./...

test-integration:
	@test -n "$$LOOM_TEST_ARANGO_URL" || (echo "Set LOOM_TEST_ARANGO_URL to an owned dev instance" >&2; exit 1)
	@test -n "$$LOOM_CLICKHOUSE_URL" || (echo "Set LOOM_CLICKHOUSE_URL to an owned dev instance" >&2; exit 1)
	$(GO) test -json -timeout=10m -count=1 -run '^(TestGraphRowsIndependentFixtureAgainstArango|TestDerivedArithmeticPoliciesAgainstArango|TestCurrentDraftAppendRowsAgainstArangoClickHouse|TestPublicationRowsAndFreshViewerAgainstArangoClickHouse|TestSavedRelatedRouteRowsAgainstArango|TestSavedRelatedRouteProposalApplyRestoreAndFreshRead|TestPublicationHTTPReceiptRevisionAndViewerAgreeAfterFreshRead|TestRestoreDraftRevisionPublicAPIIsScopedAndReloadVisible|TestApplyConstructionProposalReturnsExactPreviewOnlyAfterSave|TestApplyConstructionProposalCaptureFailureKeepsSaveAndOmitsAcceptedPreview|TestApplyConstructionProposalCapturesEmptyPreviewAsEmptyRowsArray|TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly|TestConstructionProposalCurrentCodingChoiceReconcilesAndPreviewsHTTP|TestConstructionProposalRelatedCodingGroupReconcilesReceiptMetadataHTTP|TestCoreAuthoringProtocolVerticalWitness|TestCoreAuthoringProtocolCompetingSaveDuringReconcile|TestCoreAuthoringProtocolErrorBoundaries)$$' ./internal/server | node tests/scripts/assert-go-tests.mjs TestGraphRowsIndependentFixtureAgainstArango TestDerivedArithmeticPoliciesAgainstArango TestCurrentDraftAppendRowsAgainstArangoClickHouse TestPublicationRowsAndFreshViewerAgainstArangoClickHouse TestSavedRelatedRouteRowsAgainstArango TestSavedRelatedRouteProposalApplyRestoreAndFreshRead TestPublicationHTTPReceiptRevisionAndViewerAgreeAfterFreshRead TestRestoreDraftRevisionPublicAPIIsScopedAndReloadVisible TestApplyConstructionProposalReturnsExactPreviewOnlyAfterSave TestApplyConstructionProposalCaptureFailureKeepsSaveAndOmitsAcceptedPreview TestApplyConstructionProposalCapturesEmptyPreviewAsEmptyRowsArray TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly TestConstructionProposalCurrentCodingChoiceReconcilesAndPreviewsHTTP TestConstructionProposalRelatedCodingGroupReconcilesReceiptMetadataHTTP TestCoreAuthoringProtocolVerticalWitness TestCoreAuthoringProtocolCompetingSaveDuringReconcile TestCoreAuthoringProtocolErrorBoundaries
	$(GO) test -json -timeout=10m -count=1 -run '^TestReconcileDanglesActiveReleaseAgainstArangoClickHouse$$' ./internal/dataframe/publication/arango | node tests/scripts/assert-go-tests.mjs TestReconcileDanglesActiveReleaseAgainstArangoClickHouse
	$(GO) test -json -timeout=10m -count=1 -run '^(TestDraftCASConcurrentWritersAgainstArango|TestDraftCASAfterWinnerCommitAgainstArango|TestDraftCASPublicApplyConflictClassAgainstArango|TestDraftCASUpdateFailureRollsBackHistoryAgainstArango)$$' ./internal/explorer/arango | node tests/scripts/assert-go-tests.mjs TestDraftCASConcurrentWritersAgainstArango TestDraftCASAfterWinnerCommitAgainstArango TestDraftCASPublicApplyConflictClassAgainstArango TestDraftCASUpdateFailureRollsBackHistoryAgainstArango
	node scripts/run-arango-integration.mjs --out "$${RUNNER_TEMP:-/tmp}/loom-arango-integration-$$$$"

# Direct Builder checkpoints. Full repository coverage remains in tests.yaml.
verify-fast: test-ui

verify-api: test-integration

verify-current: test-ui test-go

# Drive the running stack selected by this checkout; doctor rejects stale identity.
e2e: dev-doctor
	node tests/e2e/run.mjs
