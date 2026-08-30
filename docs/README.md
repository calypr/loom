# Loom documentation

This directory contains current product contracts, architecture references,
and operating guides. Completed plans, migration handoffs, and historical
audits do not belong here.

## Start here

- [Quickstart](QUICKSTART.md) — run Loom locally, load sample data, and make a
  first dataframe request.
- [HTTP API and OpenAPI contract](../openapi/README.md) — canonical paths,
  schemas, generator configuration, generated server package, and validation
  commands. The source specification is
  [`openapi/openapi.yaml`](../openapi/openapi.yaml).
- [Explorer authoring](EXPLORER_AUTHORING.md) — current V2 Builder contract,
  compilation pipeline, receipts, draft/publish behavior, diagnostics, and
  offline default conversion.
- [Explorer compilation architecture](EXPLORER_COMPILATION_ARCHITECTURE.md) —
  the detailed intent-to-recipe-to-physical-plan-to-AQL path.
- [GraphQL API](GRAPHQL_API.md) — graph, FHIR dataframe, and published-data
  GraphQL contracts.

## Developer and operator references

- [Developer architecture](DEVELOPER_ARCHITECTURE.md) — package boundaries,
  compiler ownership, storage model, and runtime call paths.
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

## Frontend contract

- [Explorer V2 capability and authoring](frontend/explorer-v2-capability-authoring.md)
  — exact Builder wire behavior, receipt lifecycle, diagnostics, and recovery.
