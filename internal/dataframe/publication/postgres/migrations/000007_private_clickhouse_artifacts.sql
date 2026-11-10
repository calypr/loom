-- pg-private-clickhouse-artifact-catalog-v1: temporary private ClickHouse
-- artifact manifests and the cleanup-lease catalog for the PostgreSQL control
-- plane.
--
-- Physical data tables stay in ClickHouse: this catalog persists only the
-- durable manifest (immutable identity, schema, and physical-table binding)
-- and the mutable progress/lease fields around it. It registers nothing as a
-- published output, never advances a visibility pointer, and is deliberately
-- separate from the published BundleCatalog (tests/migrations/000004) and
-- from the Explorer filesystem ZIP ArtifactStore. The version continues the
-- coordinator-ordered fixture history after the 000006 lease-checkpoint
-- reservation, and the DDL is idempotent because the pg-store-02 runner may
-- retry a migration that committed before its ledger row was written.
--
-- Manifest identity:
--   * artifact_id is the immutable primary key; Create is insert-only, so a
--     concurrent duplicate converges on the existing row instead of
--     replacing it. The (execution, output, stage, project, generation)
--     identity columns and the physical-table binding mirror the payload and
--     are only ever written by Create; the catalog's Update, Renew,
--     ClaimCleanup, Delete, and ReleaseCleanup statements touch progress and
--     lease fields only, so identity and table fields survive every later
--     write unchanged.
--   * payload is the authoritative jsonb manifest: nanosecond timestamps and
--     the missing-versus-JSON-null member shape round-trip losslessly. The
--     scan columns (state, counts, lease, identity scope) mirror the payload
--     for constraints and ordered scans; every statement writes both from
--     the same values. No manifest member is ever SQL NULL: every column is
--     NOT NULL, so a missing value fails explicitly rather than silently
--     reading back as null.
--   * state is closed to the four domain states; counts are nonnegative; the
--     lease owner, physical table, and identity scope must be present. An
--     unsupported value is refused by the constraint, never coerced.
--
-- Lease/cleanup scan: ListExpired pages the lease_until index in
-- (lease_until, artifact_id COLLATE "C") order -- the deterministic
-- byte-order tie-break the Arango collection gives _key -- so a bounded page
-- is stable across repeated reconciler passes, and ClaimCleanup rechecks
-- lease_until <= expiredBefore inside the same UPDATE so a concurrent
-- renewer wins and only one cleanup owner ever claims a manifest.
CREATE TABLE IF NOT EXISTS chartifact_private_artifacts (
    artifact_id        text        PRIMARY KEY,
    physical_table     text        NOT NULL,
    state              text        NOT NULL,
    row_count          bigint      NOT NULL,
    byte_count         bigint      NOT NULL,
    lease_owner        text        NOT NULL,
    lease_until        timestamptz NOT NULL,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    execution_id       text        NOT NULL,
    output_id          text        NOT NULL,
    stage_id           text        NOT NULL,
    project            text        NOT NULL,
    dataset_generation text        NOT NULL,
    auth_scope_mode    text        NOT NULL,
    scope_mode         text        NOT NULL,
    schema_digest      text        NOT NULL,
    scope_digest       text        NOT NULL,
    payload            jsonb       NOT NULL,
    CONSTRAINT chartifact_private_artifacts_id_present
        CHECK (artifact_id <> '' AND artifact_id = btrim(artifact_id)),
    CONSTRAINT chartifact_private_artifacts_table_present
        CHECK (physical_table <> '' AND physical_table = btrim(physical_table)),
    CONSTRAINT chartifact_private_artifacts_owner_present
        CHECK (lease_owner <> '' AND lease_owner = btrim(lease_owner)),
    CONSTRAINT chartifact_private_artifacts_state_closed
        CHECK (state IN ('CREATING', 'WRITING', 'READY', 'CLEANUP_PENDING')),
    CONSTRAINT chartifact_private_artifacts_counts_nonnegative
        CHECK (row_count >= 0 AND byte_count >= 0),
    CONSTRAINT chartifact_private_artifacts_identity_present
        CHECK (execution_id <> '' AND output_id <> '' AND stage_id <> ''
               AND project <> '' AND dataset_generation <> ''),
    CONSTRAINT chartifact_private_artifacts_payload_object
        CHECK (jsonb_typeof(payload) = 'object')
);

-- Bounded expired-lease scan: the reconciler's page walks this index in
-- lease order; the claim path rechecks the same column inside its UPDATE.
CREATE INDEX IF NOT EXISTS chartifact_private_artifacts_lease_until
    ON chartifact_private_artifacts (lease_until);
