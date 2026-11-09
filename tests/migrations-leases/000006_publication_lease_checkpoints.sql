-- pg-publication-leases-checkpoints-v1: owner-conditional checkpoint records
-- for the PostgreSQL publication control plane.
--
-- Execution owners and reconcilers fence their progress checkpoints with the
-- fenced leases already created by 000004_publication_bundle_catalog.sql;
-- this migration adds only the durable record each fenced checkpoint writes.
-- It holds metadata and progress facts only: ClickHouse remains the
-- publication row store and no data rows move here. The version takes the
-- coordinator reservation 000006_* (000005 belongs to the catalog ticket),
-- and the DDL is idempotent because the runner may retry a migration that
-- committed before its ledger row was written.
--
-- Checkpoint identity:
--   * A checkpoint belongs to one lease key and pins the exact ownership
--     grant that wrote it: owner_id and generation (the lease epoch at write
--     time) are recorded beside the payload, so a witness can reconstruct
--     which owner-episode produced every checkpoint even after takeovers and
--     same-owner reclaims.
--   * sequence is the per-key monotonic position assigned inside the same
--     guarded write that applies the checkpoint, so ordering across
--     ownership changes is gap-free and a stale token cannot pre-fill it.
-- Grant high-water: the FencedLeases keeper consults and bumps this row on
-- every committed Acquire, so a generation it has handed out is never
-- reissued even after the lease row itself is deleted (by ReleaseLease, by
-- Registry.ReleaseBundleLease, or by any other path). The row is never
-- deleted; last_generation only increases, which is what makes the keeper's
-- generation a monotonic fencing token per key across the lease row's whole
-- lifetime. Registry's own Arango-parity lease methods keep their baseline
-- epoch behaviour and do not participate in this protocol.
CREATE TABLE IF NOT EXISTS publication_lease_grants (
    key             text    PRIMARY KEY,
    last_generation bigint  NOT NULL DEFAULT 0 CHECK (last_generation >= 0)
);

CREATE TABLE IF NOT EXISTS publication_execution_checkpoints (
    key             text        NOT NULL,
    sequence        bigint      NOT NULL,
    owner_id        text        NOT NULL,
    generation      bigint      NOT NULL,
    checkpointed_at timestamptz NOT NULL,
    payload         jsonb       NOT NULL,
    PRIMARY KEY (key, sequence),
    CONSTRAINT publication_execution_checkpoints_sequence_positive
        CHECK (sequence >= 1),
    CONSTRAINT publication_execution_checkpoints_generation_positive
        CHECK (generation >= 1),
    CONSTRAINT publication_execution_checkpoints_payload_object
        CHECK (jsonb_typeof(payload) = 'object')
);

-- Ownership audit: which grant wrote which checkpoints for one key, newest
-- first, matching how a resuming writer finds its last checkpoint.
CREATE INDEX IF NOT EXISTS publication_execution_checkpoints_owner_generation
    ON publication_execution_checkpoints (key, owner_id, generation, sequence);
