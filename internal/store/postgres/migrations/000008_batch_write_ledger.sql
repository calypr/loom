-- pg-store-06: idempotent COPY batch-write ledger for the PostgreSQL source store.
--
-- One row per committed bulk write unit (resource and edge rows written with
-- PostgreSQL COPY through transaction-local staging). The row is claimed
-- inside the batch's own transaction, before any row is staged or published,
-- so the ledger marker and the published rows commit or roll back together:
-- a batch that failed before commit leaves neither rows nor marker, and a
-- retry of identical committed input reads the marker and stops.
--
-- Identity rules:
--   - batch_id is the caller's deterministic batch identity (stable across
--     retries of one write unit). PRIMARY KEY makes the database, not the
--     caller, the arbiter of "this identity has committed once"; concurrent
--     claimants of one identity serialize on the key.
--   - digest is the lowercase hex SHA-256 of the canonical encoding of the
--     batch content (scope, ordered rows, every column, payload bytes or
--     their absence). Replaying the same identity with an equal digest is a
--     no-op; reusing the identity with a different digest is refused by the
--     writer as a typed conflict, so an ambiguous retry can never replace
--     a different payload under one identity.
--   - project and dataset_generation record the batch scope exactly as
--     fhir_resource and fhir_edge_occurrence store it; the empty
--     dataset_generation is the legacy dataset namespace, never "any".
--   - resource_count and edge_count are the exact numbers of rows the
--     batch's publish statements inserted into their scoped tables (not the
--     number offered, which may collapse duplicates or skip identical
--     already-stored rows). Readers get exact commit accounting from here.
--   - committed_at records when the marker's transaction committed.
--
-- The DDL is idempotent (IF NOT EXISTS) because the runner records the
-- ledger row after commit and must converge on retry.
CREATE TABLE IF NOT EXISTS batch_write_ledger (
    batch_id           text        NOT NULL,
    project            text        NOT NULL,
    dataset_generation text        NOT NULL,
    digest             text        NOT NULL,
    resource_count     bigint      NOT NULL DEFAULT 0,
    edge_count         bigint      NOT NULL DEFAULT 0,
    committed_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT batch_write_ledger_pkey PRIMARY KEY (batch_id),
    CONSTRAINT batch_write_ledger_batch_present CHECK (batch_id <> ''),
    CONSTRAINT batch_write_ledger_project_present CHECK (project <> ''),
    CONSTRAINT batch_write_ledger_digest_hex CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT batch_write_ledger_counts_nonnegative
        CHECK (resource_count >= 0 AND edge_count >= 0)
);
