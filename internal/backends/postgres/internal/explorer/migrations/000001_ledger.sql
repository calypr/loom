-- pg-state-foundation-v1: create the per-schema migration ledger.
--
-- This mirrors the pg-store-02 ledger contract (internal/store/postgres/
-- migrations/000001_ledger.sql): version, name, and checksum only, never the
-- SQL body, with the same CHECK shapes. The MigrationRunner reads this table
-- from the target schema, so a schema whose history starts here bootstraps
-- its own ledger audited by the same runner. Unlike the pg-store-02 copy this
-- body is written CREATE TABLE IF NOT EXISTS so a crash in the narrow
-- commit-before-record window converges on retry, as the runner documentation
-- requires of feature migrations. It never drops or truncates anything.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    bigint      NOT NULL,
    name       text        NOT NULL,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schema_migrations_pkey PRIMARY KEY (version),
    CONSTRAINT schema_migrations_version_positive CHECK (version > 0),
    CONSTRAINT schema_migrations_name_shape CHECK (name ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT schema_migrations_checksum_hex CHECK (checksum ~ '^[0-9a-f]{64}$')
);
