# API packages

This directory contains Loom's external API adapters. Packages here own route
registration, request decoding, authorization handoff, and response encoding;
they call domain services but do not define storage or ingestion behavior.

- `http` provides shared Fiber transport middleware, authentication, errors,
  and health checks.
- `bulk/load` owns the multipart generation upload and activation endpoints.
- `graphql/graph` owns both the graph and ClickHouse-backed published-dataframe fields.

`internal/server` composes these adapters and supplies their dependencies.
