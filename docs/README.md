# Loom documentation

This directory contains current product contracts, architecture references,
and operating guides. Superseded product plans and their evidence live under
`product/history/`, separate from active execution instructions.

## Start here

- [Standalone Explorer demo](DEMO_QUICKSTART.md) runs the no-auth Builder and Viewer against the locked FHIR Aggregator cohort with one command.
- [Quickstart](QUICKSTART.md) — run Loom locally, load sample data, and make a
  first dataframe request.
- [HTTP API and OpenAPI contract](../openapi/openapi.yaml) — canonical paths,
  schemas, generator configuration, generated server package, and validation
  commands. The source specification is
  [`openapi/openapi.yaml`](../openapi/openapi.yaml).
- [Explorer authoring](EXPLORER_AUTHORING.md) — current V2 Builder contract,
  compilation pipeline, receipts, draft/publish behavior, diagnostics, and
  offline default conversion.
- [Explorer compilation architecture](EXPLORER_COMPILATION_ARCHITECTURE.md) —
  the detailed intent-to-recipe-to-physical-plan-to-AQL path.
- [Schema-driven dataframe construction](product/ML_DATAFRAMER_DELIVERY_PLAN.md)
  defines the five active work packages, implementation ownership, and user outcomes.
  [The product index](product/README.md) links the current verification protocol,
  execution ledger, and archived history.
- [Builder formal verification plan](verification/BUILDER_FORMAL_VERIFICATION_PLAN.md) covers Tier 1 lifecycle and Tier 2 route-conformance work.
- [Developer journey native cases](DEV_JOURNEY_NATIVE_CASES.md) maps the current `loom-dev` journeys to native Playwright cases.
- [Builder reconcile transitions](verification/builder-reconcile-transitions.md) maps reconcile states, guards, effects, and model actions to the implementation.
- [Route proof chain](verification/ROUTE_PROOF_CHAIN.md) defines the evidence a route needs before it is route-proven.
- [GraphQL API](GRAPHQL_API.md) — graph, FHIR dataframe, and published-data
  GraphQL contracts.

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
- [UI interaction verification](UI_VERIFICATION.md) — current native Playwright
  commands, owned-stack requirements, and evidence interpretation.
- [Repository cleanup goal](REPO_CLEANUP_GOAL.md) — active cleanup policy,
  candidate queues, decision rules, and acceptance criteria.

## Frontend contract

- [Explorer V2 capability and authoring](frontend/explorer-v2-capability-authoring.md)
  — exact Builder wire behavior, receipt lifecycle, diagnostics, and recovery.
