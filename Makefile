.PHONY: build build-cli build-server clean compiler-bench conformance generate-fhir generate-graphql graphql-check gqlgen-check test docker-build docker-run

GO ?= go
GOCACHE_DIR ?= $(CURDIR)/.gocache
GOFLAGS ?=
SCHEMA_PATH ?= schemas/graph-fhir.json
IMAGE ?= arango-fhir-proto:local

build: build-cli build-server

build-cli:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) build $(GOFLAGS) -o bin/arango-fhir-proto ./cmd/arango-fhir-proto

build-server:
	mkdir -p bin $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) build $(GOFLAGS) -o bin/arango-fhir-server ./cmd/arango-fhir-server

generate-graphql:
	rm -f internal/graphqlapi/generated.go internal/graphqlapi/model/models.go internal/graphqlapi/schema.resolvers.go
	mkdir -p $(GOCACHE_DIR)
	@status=0; \
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) run github.com/99designs/gqlgen generate || status=$$?; \
	if [ -f internal/graphqlapi/generated.go ]; then \
		perl -0pi -e 's/return &res, graphql::ErrorOnPath\\(ctx, err\\)/return res, graphql::ErrorOnPath(ctx, err)/g; s/return &res, graphql\\.ErrorOnPath\\(ctx, err\\)/return res, graphql.ErrorOnPath(ctx, err)/g' internal/graphqlapi/generated.go; \
	fi; \
	test $$status -eq 0 -o -f internal/graphqlapi/generated.go

generate-fhir:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) run ./cmd/generate -schema $(SCHEMA_PATH) -out-dir internal/fhir
	gofmt -w internal/fhir/model.go internal/fhir/validate.go internal/fhir/extract.go internal/fhirschema/generated.go

graphql-check:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) test $(GOFLAGS) ./internal/graphqlapi ./internal/dataframe -count=1

gqlgen-check: graphql-check

test:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) test $(GOFLAGS) ./... -count=1

compiler-bench:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) test $(GOFLAGS) ./conformance/compiler -run '^$$' -bench '^BenchmarkCompilerOracle$$' -benchmem

conformance:
	mkdir -p $(GOCACHE_DIR)
	GOCACHE=$(GOCACHE_DIR) GOTOOLCHAIN=auto $(GO) test $(GOFLAGS) ./conformance/... -count=1

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p 8080:8080 $(IMAGE)

clean:
	rm -rf bin
