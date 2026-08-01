# API packages

This directory contains Loom's external API adapters. Packages here own route
registration, request decoding, authorization handoff, and response encoding;
they call domain services but do not define storage or ingestion behavior.

- `http` provides shared Fiber transport middleware, authentication, errors,
  and health checks.
- `bulk/load` owns import and raw/generation load endpoints.
- `bulk/dump` owns raw, generation, and dataframe export endpoints.
- `graphql/graph` owns the graph and FHIR/dataframe GraphQL surface.
- `graphql/flat` owns the ClickHouse-backed flat GraphQL surface.

`internal/server` composes these adapters and supplies their dependencies.
