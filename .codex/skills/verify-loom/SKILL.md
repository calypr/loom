---
name: verify-loom
description: Verify real NCPI ingestion, Explorer publication, ClickHouse materialization, and GraphQL reads with owned acceptance projects.
---

# Verify real data

Follow [REAL_DATA_ACCEPTANCE.md](../../../docs/REAL_DATA_ACCEPTANCE.md) from the
repository root. It owns launch, prerequisites, fixture/oracle checks,
performance comparison, evidence, Doctor, and cleanup.

`make acceptance-real` redeploys canonical `loom-demo` before running an isolated
fixture proof. It is an active deployment command, not a passive health check.
`make acceptance-performance` compares isolated base/current variants.

Require the report and cleanup checks in the runbook. Preserve failure evidence.
Never remove canonical `loom-demo` volumes; delete only generated owned projects.
