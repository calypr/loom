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
