# Loom documentation

This directory contains current product contracts, architecture references,
and operating guides. Superseded product plans and their evidence live under
`product/history/`, separate from active execution instructions.

## Start here

- [Standalone Explorer demo](DEMO_QUICKSTART.md) runs the no-auth Builder and Viewer against the locked FHIR Aggregator cohort with one command.
- [Quickstart](QUICKSTART.md) — run Loom locally, load sample data, and make a
  first dataframe request.
- [HTTP API and OpenAPI contract](../api/openapi.yaml) — canonical paths,
  schemas, generator configuration, generated server package, and validation
  commands. The source specification is
  [`api/openapi.yaml`](../api/openapi.yaml).
- [Explorer authoring](EXPLORER_AUTHORING.md) — current V2 Builder contract,
  compilation pipeline, receipts, draft/publish behavior, diagnostics, and
  offline default conversion.
- [Explorer compilation architecture](EXPLORER_COMPILATION_ARCHITECTURE.md) —
  the detailed intent-to-recipe-to-physical-plan-to-AQL path.
- [Schema-driven dataframe construction](product/ML_DATAFRAMER_DELIVERY_PLAN.md)
  defines the five active work packages, implementation ownership, and user outcomes.
  [The product index](product/README.md) links the current verification protocol,
  execution ledger, and archived history.
- [Local verification](../.codex/skills/verify/SKILL.md) — focused UI/Go checks,
  owned development Doctor, and the five real-Chrome e2e specs.
- [Real-data acceptance](REAL_DATA_ACCEPTANCE.md) — locked NCPI ingestion,
  publication, physical storage, REST, performance, and cleanup.
- [Product backlog](BACKLOG.md) — open findings, incomplete acceptance, and
  deferred product decisions, with their current reproduction boundaries.
- [Dataframe API](DATAFRAME_API.md) covers FHIR queries and published-data reads.

## Developer and operator references

- [Developer architecture](DEVELOPER_ARCHITECTURE.md) — package boundaries,
  compiler ownership, storage model, and runtime call paths.
- [Package audit lookup table](PACKAGE_AUDIT.md) — complete package locations,
  responsibilities, contents, dependencies, importers, and consolidation evidence.
- [Interface audit](architecture/INTERFACE_AUDIT.md) — per-interface keep, collapse,
  or delete decisions across auth, ingest, catalog, and dataset boundaries.
- [Dataframer recipes](DATAFRAMER_RECIPES.md) — native recipe authoring for
  repository, ETL, and developer workflows.
- [Dataframer recipe reference](DATAFRAMER_RECIPE_REFERENCE.md) — complete
  recipe language and deployment runbook.
- [Code generation](CODE_GENERATION.md) — generated sources, source-of-truth
  files, and regeneration commands.
- [Compiler performance](COMPILER_PERFORMANCE.md) — benchmark and tuning
  guidance.
- [Reliability contract](loom-reliability-contract.md) — immutable generations,
  publication, selectors, and error-state guarantees.
- [Verification script guide](../scripts/README.md) — current commands and
  their test boundaries.
- [Retired verification guarantees](verification/DELETED_GUARANTEES.md) —
  deleted harness disposition and recovery information.
- [Repository cleanup goal](REPO_CLEANUP_GOAL.md) — active cleanup policy,
  candidate queues, decision rules, and acceptance criteria.

## Frontend contract

- [Explorer V2 capability and authoring](frontend/explorer-v2-capability-authoring.md)
  — exact Builder wire behavior, receipt lifecycle, diagnostics, and recovery.
