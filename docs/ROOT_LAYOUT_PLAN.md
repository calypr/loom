# Root layout cleanup plan

Goal: the repository root holds only entry points (`README.md`, `AGENTS.md`,
`Makefile`, `go.mod`, `main.go`, `Dockerfile`, dotfiles) and a small set of
purpose-named directories. This plan moves files only; it does not change
behavior. Generating graph edges directly from the FHIR JSON Schema is out of
scope and belongs in a separate PR.

## Target layout

```
cmd/           executables (unchanged; dead commands removed in phase 0)
internal/      runtime code and Go unit tests (unchanged)
ui/            frontend (unchanged)
api/           OpenAPI contract and its generated Go server
  openapi.yaml, oapi-codegen.yaml, templates/
  loomapi/     (was generated/loomapi)
fhir/          graph FHIR schema and its generated Go code
  graph-fhir.json   (was schemas/graph-fhir.json)
  model/       (was generated/fhir)
  schema/      (was generated/fhirschema)
scripts/       runnable tools only, no tests
tests/         tests and local development infrastructure
  deploy/      Compose files, dev container, and local stack
    dev/       Dockerfile.dev, its ignore file, Air config, and helper scripts
  fixtures/    (was testdata/)
  conformance/ (was conformance/compiler)
  e2e/         (was scripts/e2e)
  scripts/     (tests for scripts/: *.test.mjs, *_test.go, scripts/tests/*.py)
docs/
```

### What stays where it is, and why

- **Go `_test.go` files stay next to their package.** Go compiles a package's
  tests together with that package, and the white-box tests use unexported
  identifiers. Moving them to `tests/` would mean either exporting internals or
  rewriting the tests, which adds debt instead of removing it. `tests/` holds
  black-box suites, fixtures, and script tests only. Per-package `testdata/`
  directories (for example `internal/server/testdata`) also stay, by Go
  convention.
- **`Dockerfile` and `.dockerignore` stay at the root.** `build.yaml` and
  `make docker-build` use the root build context, and Docker looks for the
  ignore file at the context root.
- **The schema is not fetched from a GitHub URL.** A link to
  `blob/main/...` is a moving target and would make generation, runtime
  startup, and CI depend on the network. It moves to `fhir/graph-fhir.json`.
  Optional follow-up: embed it with `go:embed` so `--schema` has a default and
  compose no longer has to mount the file.

## Phases

Each phase is one PR-sized commit. Use `git mv` to keep history, and run
`python3 scripts/package_audit_verify.py <location>` before and after every
Go package move.

### 0. Delete dead files (needs the operator to confirm each item)

- Candidates: `cmd/arango-preview-index-{audit,migration}`,
  `cmd/arango-workspace-migration`, `scripts/local-preview-index-migration*`
  (if the migration has run everywhere), `scripts/owned-query-probe*`,
  `scripts/audit-http-500*`, `docs/product/history/`, and finished plans in
  `docs/`.
- Remove these local, untracked files: root binaries (`loom`,
  `arango-fhir-*`, `compiler.test`), `error.log`,
  `loom-DocumentReference.csv`, `.openshell-transfer-*`, `outputs/`, and
  `ticket-backlog-20261105/`. Add the matching `.gitignore` entries.

### 1. `tests/` (test-only files)

- `testdata/` → `tests/fixtures/`. Update references in:
  - `compose.yaml` (`LOOM_DEMO_FIXTURE_DIR` defaults)
  - `.air.dev.toml` (`--dataframer-recipe`)
  - `scripts/{loom-dev,run-arango-integration}.mjs` and their tests
  - `scripts/{acceptance-real,demo-smoke}.sh`, `loom-dev-build-stamp.sh`
  - `cmd/loom-acceptance` and about 19 Go tests that resolve
    `../../testdata` (find them with
    `rg -l 'testdata' --glob '*.go' internal cmd`). Prefer a shared
    `internal/testkit` helper that locates the repository root over editing
    relative paths in each file.
  - `.codex/skills/verify/SKILL.md`
- `conformance/compiler` → `tests/conformance` (1 importer:
  `cmd/dataframe-profile`; update its `-fixtures` default). Update the CI
  package list in `tests.yaml:62`, the Makefile `conformance` and
  `compiler-bench` targets, and `scripts/package_audit.py`.
- `scripts/e2e/` → `tests/e2e/`; update `make e2e`.
- Script tests → `tests/scripts/` (`*.test.mjs`, `*_test.go`,
  `scripts/tests/*.py`, `assert-go-tests.mjs`). Update the
  `ci/list-node-tests.mjs` globs, `make test-scripts`, and the `go test` file
  lists in the Makefile and `tests.yaml`. Note: a `//go:build ignore` script
  and its `_test.go` are tested as a file pair, so they must move together or
  the test has to stop listing files explicitly.

### 2. `api/` and `fhir/` (merge `openapi/`, `generated/`, `schemas/`)

- `openapi/*` → `api/`, and `generated/loomapi` → `api/loomapi`
  (70 importers). Update `oapi-codegen.yaml`'s output path,
  `make generate-openapi`, `scripts/check_openapi_route_ownership.sh`,
  `scripts/audit-http-500-responses.py`,
  `scripts/lib/builder-route-evidence.mjs`,
  `ui/packages/loom-ui/src/types.openapi.unit.test.ts`, and the UI type
  generation input if it reads `openapi/openapi.yaml`.
- `schemas/graph-fhir.json` → `fhir/graph-fhir.json`;
  `generated/fhir` → `fhir/model` (7 importers);
  `generated/fhirschema` → `fhir/schema` (2 importers). Update `SCHEMA_PATH`
  and `generate-fhir` in the Makefile, the defaults in `cmd/generate` and
  `cmd/arango-fhir-proto`, the compose `--schema` mounts, `.air.dev.toml`,
  `scripts/run-arango-integration.mjs` (its source-directory list), and
  `.gitnexusignore`.
- Regenerate with `make generate`, and confirm a zero diff apart from paths
  and package names.

### 3. `tests/deploy/` (compose, dev container, local stack)

- `compose*.yaml` → `tests/deploy/`. Compose resolves relative paths from the
  compose file's directory, so rewrite `context: .` → `..`, `./ui` → `../ui`,
  and the fixture paths, or set `--project-directory .` in every invocation.
  Pick one approach and use it everywhere. Callers: the Makefile `demo-*`,
  `dev*`, and `repository-up` targets, `scripts/loom-dev.mjs`,
  `scripts/loom-repo-up.sh`, `scripts/acceptance-*.sh`, `README.md`, and the
  verify skills.
- `experimental/docker-compose.yml` → `tests/deploy/compose.local.yaml`, then remove
  `experimental/`. Its bind mounts (`./data`, `./clickhouse-data`) move to
  named volumes, or to an ignored `tests/deploy/.data/`.
- `Dockerfile.dev`, `Dockerfile.dev.dockerignore`, `.air.dev.toml`, and
  `loom-dev-*.sh` → `tests/deploy/dev/`. Update the `dockerfile:` path in
  `compose.dev.yaml` and the `COPY` sources in `Dockerfile.dev` (the build
  context stays at the repository root). The scripts keep their in-container
  `/workspace/...` paths, so only the `COPY` lines change.

## Verification for each phase

- `make test-go test-ui test-scripts conformance openapi-check`, plus
  `make generate` with an empty diff.
- `rg -n '<old path>'` across the repository (excluding `docs/` history)
  returns nothing.
- Phase 3 also needs `make dev` and `make demo-up` / `demo-smoke` to start
  cleanly, because compose path errors only show up at runtime.
- CI: `tests.yaml` and `build.yaml` pass on the branch.

## Out of scope

- Deriving edges from the upstream FHIR JSON Schema instead of
  `graph-fhir.json`'s `links`. Note for that PR: the official
  `fhir.schema.json` does not record `Reference` target types, so edge targets
  must come from the StructureDefinitions (`targetProfile`), for example the
  FHIR definitions bundle.
- Shrinking `internal/` and the per-scenario Arango tests.
