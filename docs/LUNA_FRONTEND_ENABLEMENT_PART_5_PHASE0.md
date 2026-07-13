# Part 5 Phase 0 contract freeze

This file records the coordinator freeze used for Phase 1. It is intentionally
an internal-service contract; GraphQL schema changes and generated code remain
coordinator-owned and are deferred until the service lanes have completed.

## Frozen boundaries

- Catalog discovery returns persistence-neutral dataset summaries and never
  scans without an explicit project allowlist.
- Template availability consumes a catalog-backed capability snapshot and
  returns semantic starter intent; it does not emit AQL or collection names.
- `dataframe.Service.Validate` shares the exact preparation and compiler
  boundary with `Run`, returns a normalized builder, a request fingerprint,
  plan metadata, warnings, and timing diagnostics, and never executes rows.
- User-facing errors use the stable code registry in
  `internal/dataframe/errors.go`. GraphQL and HTTP adapters map the same
  semantic error without exposing backend details.
- Project, active generation, and authorization scope are resolved once by
  the request adapter and propagated to every catalog and compiler call.
- Generated GraphQL files, schema, route registration, and server wiring are
  coordinator-owned. Phase 1 workers must report those wiring requirements
  rather than editing shared files.

## Phase 1 acceptance gate

Each lane must provide focused unit tests, deterministic ordering and defensive
copy behavior where applicable, `git diff --check` output, and a list of
coordinator wiring decisions. No lane may introduce a FHIR-type-, project-,
fixture-, or edge-label-specific production branch.
