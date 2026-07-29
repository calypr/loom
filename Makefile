.PHONY: build build-cli build-server clean compiler-bench compiler-arango-bench dataframe-demo dataframe-profile dataframe-boundaries dataframe-test conformance generate-fhir generate-graphql graphql-check gqlgen-check test docker-build docker-run

GO ?= go
GO_VERSION ?= 1.26.5
GO_TOOLCHAIN ?= go$(GO_VERSION)
GOCACHE_DIR ?= $(CURDIR)/.gocache
GOFLAGS ?=
SCHEMA_PATH ?= schemas/graph-fhir.json
IMAGE ?= arango-fhir-proto:local
BENCH_TIME ?= 10x
BENCH_COUNT ?= 5
GRAPHQL_URL ?= http://127.0.0.1:8080/graphql/dataframe
DATAFRAME_REPEAT ?= 1
DATAFRAME_LIMIT ?= 0
DATAFRAME_TIMEOUT ?= 5m
DATAFRAME_PRINT_RESPONSE ?= false
DATAFRAME_QUERY ?= examples/meta_gdc_case_matrix.graphql
DATAFRAME_VARIABLES ?= examples/meta_gdc_case_matrix.variables.json
DATAFRAME_PROFILE_VARIABLES ?= examples/meta_gdc_case_matrix.variables.json
DATAFRAME_PROFILE_LIMIT ?= 1000

build: build-cli build-server

build-cli:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) build $(GOFLAGS) -o bin/arango-fhir-proto ./cmd/arango-fhir-proto

build-server:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) build $(GOFLAGS) -o bin/arango-fhir-server ./cmd/arango-fhir-server

generate-graphql:
	mkdir -p $(GOCACHE_DIR)
	@status=0; fix_status=0; \
	for config in gqlgen.yml graphqlapi/clickhouse/gqlgen.yml; do \
		GOFLAGS="$(GOFLAGS) -mod=mod" GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run -mod=mod github.com/99designs/gqlgen generate --config $$config || status=$$?; \
	done; \
	for generated in graphqlapi/generated.go graphqlapi/clickhouse/generated.go; do \
		GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/gqlgenfix $$generated || fix_status=$$?; \
	done; \
	test $$fix_status -eq 0; \
	test $$status -eq 0

generate-fhir:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/generate -schema $(SCHEMA_PATH) -structs-out fhirstructs -metadata-out fhirschema/generated.go
	gofmt -w fhirstructs/model.go fhirstructs/validate.go fhirstructs/extract.go fhirstructs/helpers.go fhirschema/generated.go

graphql-check:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./graphqlapi ./internal/dataframe -count=1

gqlgen-check: graphql-check

test:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./... -count=1

compiler-bench:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./conformance/compiler -run '^$$' -bench '^BenchmarkCompilerOracle$$' -benchmem

# Requires a locally loaded META fixture. Override BENCH_TIME/BENCH_COUNT for
# a longer run, for example: make compiler-arango-bench BENCH_TIME=3s BENCH_COUNT=10.
compiler-arango-bench:
	mkdir -p $(GOCACHE_DIR)
	LOOM_COMPILER_ARANGO_INTEGRATION=1 GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./internal/dataframe -run '^$$' -bench '^BenchmarkGenericCompilerAgainstArango$$' -benchmem -benchtime=$(BENCH_TIME) -count=$(BENCH_COUNT)

# Requires `arango-fhir-server --no-auth` and a loaded META project. Prints the
# actual GraphQL dataframe response and per-request wall-clock timings.
dataframe-demo:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-query -url $(GRAPHQL_URL) -query $(DATAFRAME_QUERY) -variables $(DATAFRAME_VARIABLES) -repeat $(DATAFRAME_REPEAT) -limit $(DATAFRAME_LIMIT) -timeout $(DATAFRAME_TIMEOUT) -print-response=$(DATAFRAME_PRINT_RESPONSE)

# Requires a loaded META fixture database. Compiles the checked-in GDC fixture,
# writes exact rendered AQL, then runs Arango EXPLAIN and PROFILE 2.
dataframe-profile:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-profile -variables $(DATAFRAME_PROFILE_VARIABLES) -limit $(DATAFRAME_PROFILE_LIMIT)

dataframe-boundaries:
	./scripts/check_dataframe_package_boundaries.sh

dataframe-test: dataframe-boundaries
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./internal/dataframe/spec ./internal/dataframe/semantic ./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower ./internal/dataframe/compiler/optimize ./internal/dataframe/compiler/render/aql ./internal/dataframe/compiler ./internal/dataframe/runtime ./internal/dataframe -count=1

conformance:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./conformance/... -count=1

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 $(IMAGE)

clean:
	rm -rf bin
