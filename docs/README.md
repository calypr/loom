# Loom documentation

Use this page to choose the right document. The current runtime contract is
documented separately from migration notes and implementation plans.

## Start here

- [Quickstart](QUICKSTART.md) — run Loom locally, load sample data, and make a
  first dataframe request.
- [Explorer authoring](EXPLORER_AUTHORING.md) — current V1 Builder contract,
  compilation pipeline, receipts, draft/publish behavior, diagnostics, and
  offline default conversion.
- [Explorer compilation architecture](EXPLORER_COMPILATION_ARCHITECTURE.md) —
  the detailed intent-to-recipe-to-physical-plan-to-AQL path.
- [The Explorer recipe boundary](EXPLORER_RECIPE_BOUNDARY.md) — concrete
  examples of the compiler and backend behavior the frontend does not own.
- [GraphQL API](GRAPHQL_API.md) — graph, FHIR dataframe, and published-data
  GraphQL contracts.

## Developer and operator references

- [Developer architecture](DEVELOPER_ARCHITECTURE.md) — package boundaries,
  compiler ownership, storage model, and runtime call paths.
- [Dataframer recipes](DATAFRAMER_RECIPES.md) — native recipe authoring for
  repository, ETL, and developer workflows.
- [Dataframer recipe reference](DATAFRAMER_RECIPE_REFERENCE.md) — complete
  recipe language and deployment runbook.
- [Code generation](CODE_GENERATION.md) — generated sources and regeneration
  commands.
- [Compiler performance](COMPILER_PERFORMANCE.md) — benchmark and tuning
  guidance.
- [Dataframe federation](DATAFRAME_FEDERATION.md) — published-data source
  selection and cross-project reads.
- [Reliability contract](loom-reliability-contract.md) — immutable generations,
  publication, selectors, and error-state guarantees.

## Migration and planning material

- [Backend-owned Explorer authoring plan](PLAN_BACKEND_OWNED_EXPLORER_AUTHORING.md)
  — phased migration from browser-authored workspaces to server-owned intent
  commands, identities, canonical drafts, and stored-draft reconciliation.
- [Historical Explorer V2 migration](FRONTEND_EXPLORER_V2_MIGRATION.md) —
  retained for compatibility and ETL migration context. It is not the current
  browser authoring contract; use [Explorer authoring](EXPLORER_AUTHORING.md)
  for new Builder work.
- [Frontend facet performance plan](FRONTEND_DATAFRAME_FACET_PERFORMANCE_PLAN.md)
  — implementation planning material, not a runtime API reference.

Documents named `PLAN_*`, `*_PLAN`, or similar are planning artifacts when
present in a checkout. They may explain motivation or rollout history, but the
contract documents above are the source of truth for current behavior.
