-- pg-publication-read-pins-reconcile-v1: read retention pins and cleanup
-- claims for the PostgreSQL publication control plane.
--
-- Read pins keep the physical publication tables of an exact execution
-- reader-visible while exact export / row streaming runs; a cleanup claim is
-- the reconciler's exclusive right to remove them. The two operations race by
-- design, so they must serialize: every pin and cleanup transition for one
-- execution locks the single publication_execution_retention anchor row
-- first, which is the one lock point both paths take before any pin or claim
-- row is read or written. This migration holds control facts only: the
-- physical tables live in ClickHouse, which remains the publication row
-- store, and no data rows move here. The version continues the coordinator's
-- cross-directory reservation sequence (000004 catalog, 000006 lease
-- checkpoints, 000007 chartifact/interpretations, 000008 selections), and
-- the DDL is idempotent because the runner may retry a migration that
-- committed before its ledger row was written.
--
-- Retention identity:
--   * Every fact is keyed by the execution id, the same identity the pin and
--     cleanup APIs address, so pins, claims, and the anchor for one
--     execution always colocate on one lock.
--   * Pins are one row per (execution, owner): a repeated acquire by the same
--     owner refreshes that row rather than duplicating a reader, which is
--     how the Arango pins array already collapses, and the row preserves
--     each owner's own expiry (owner-correlated retention).
--   * Cleanup is at most one live claim per execution: a missing claim row is
--     "no cleanup", which stays distinct from any expiry timestamp; the
--     claim stores its owning reconciler and expiry so renewal and release
--     are owner-conditional and a witness can attribute every claim episode.
--   * No column is nullable-ambiguous: expiry is always a timestamptz, so
--     "absent" is expressed only by row absence and never by a NULL stand-in.
CREATE TABLE IF NOT EXISTS publication_execution_retention (
    execution_id text        PRIMARY KEY,
    updated_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS publication_execution_read_pins (
    execution_id text        NOT NULL REFERENCES publication_execution_retention (execution_id) ON DELETE CASCADE,
    owner_id     text        NOT NULL CHECK (owner_id <> ''),
    expires_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    PRIMARY KEY (execution_id, owner_id)
);

-- Active-reader and expiry sweeps for one execution: the cleanup guard scans
-- live pins by (execution, expiry), and every successful arbitration GCs
-- expired pins with the same access shape.
CREATE INDEX IF NOT EXISTS publication_execution_read_pins_expiry
    ON publication_execution_read_pins (execution_id, expires_at);

CREATE TABLE IF NOT EXISTS publication_execution_cleanup_claims (
    execution_id text        PRIMARY KEY REFERENCES publication_execution_retention (execution_id) ON DELETE CASCADE,
    owner_id     text        NOT NULL CHECK (owner_id <> ''),
    expires_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);
