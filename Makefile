.PHONY: build build-cli build-server clean compiler-bench dataframe-demo dataframe-profile dataframe-boundaries dataframe-test explorer-contract-test explorer-query conformance generate-fhir generate-graphql graphql-check gqlgen-check test docker-build docker-run

GO ?= go
GO_VERSION ?= 1.26.5
GO_TOOLCHAIN ?= go$(GO_VERSION)
GOCACHE_DIR ?= $(CURDIR)/.gocache
GOFLAGS ?=
SCHEMA_PATH ?= schemas/graph-fhir.json
IMAGE ?= arango-fhir-proto:local
GRAPHQL_URL ?= http://127.0.0.1:8080/graphql/dataframe
DATAFRAME_REPEAT ?= 1
DATAFRAME_LIMIT ?= 0
DATAFRAME_TIMEOUT ?= 5m
DATAFRAME_PRINT_RESPONSE ?= false
DATAFRAME_QUERY ?= examples/meta_gdc_case_matrix.graphql
DATAFRAME_VARIABLES ?= examples/meta_gdc_case_matrix.variables.json
DATAFRAME_PROFILE_VARIABLES ?= examples/meta_gdc_case_matrix.variables.json
DATAFRAME_PROFILE_LIMIT ?= 1000
EXPLORER_QUERY ?= examples/explorer-client/rows.graphql
EXPLORER_VARIABLES ?= examples/explorer-client/exact-selector.variables.json
EXPLORER_GRAPHQL_URL ?= http://127.0.0.1:8080/graphql/graph

build: build-cli build-server

build-cli:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) build $(GOFLAGS) -o bin/arango-fhir-proto ./cmd/arango-fhir-proto

build-server:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) build $(GOFLAGS) -o bin/arango-fhir-server ./cmd/arango-fhir-server

generate-graphql:
	mkdir -p $(GOCACHE_DIR)
	GOFLAGS="$(GOFLAGS) -mod=mod" GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run -mod=mod github.com/99designs/gqlgen generate --config gqlgen.yml
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/gqlgenfix generated/graphql/graph/executor/schema.generated.go

generate-fhir:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/generate -schema $(SCHEMA_PATH) -structs-out generated/fhir -metadata-out generated/fhirschema/generated.go
	gofmt -w generated/fhir/*.go generated/fhirschema/generated.go

graphql-check:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./internal/api/graphql/... ./generated/graphql/... -count=1

gqlgen-check: graphql-check

test:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./... -count=1

compiler-bench:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./conformance/compiler -run '^$$' -bench '^BenchmarkCompilerOracle$$' -benchmem

# Requires `arango-fhir-server --no-auth` and a loaded META project. Prints the
# actual GraphQL dataframe response and per-request wall-clock timings.
dataframe-demo:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-query -url $(GRAPHQL_URL) -query $(DATAFRAME_QUERY) -variables $(DATAFRAME_VARIABLES) -repeat $(DATAFRAME_REPEAT) -limit $(DATAFRAME_LIMIT) -timeout $(DATAFRAME_TIMEOUT) -print-response=$(DATAFRAME_PRINT_RESPONSE)

explorer-contract-test:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./examples/explorer-client -count=1

# Runs the same exact-selector rows query handed to the external Explorer app.
# Requires an integrated reliability API and a matching local publication.
explorer-query:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-query -url $(EXPLORER_GRAPHQL_URL) -query $(EXPLORER_QUERY) -variables $(EXPLORER_VARIABLES) -repeat 1 -timeout $(DATAFRAME_TIMEOUT) -print-response=true

# Requires a loaded META fixture database. Compiles the checked-in GDC fixture,
# writes exact rendered AQL, then runs Arango EXPLAIN and PROFILE 2.
dataframe-profile:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) run ./cmd/dataframe-profile -variables $(DATAFRAME_PROFILE_VARIABLES) -limit $(DATAFRAME_PROFILE_LIMIT)

dataframe-boundaries:
	./scripts/check_dataframe_package_boundaries.sh

dataframe-test: dataframe-boundaries
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./internal/dataframe/spec ./internal/dataframe/semantic ./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower ./internal/dataframe/compiler/optimize ./internal/dataframe/compiler/render/aql ./internal/dataframe/compiler ./internal/dataframe/runtime -count=1

conformance:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test $(GOFLAGS) ./conformance/... -count=1

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 $(IMAGE)

clean:
	rm -rf bin
