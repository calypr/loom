-- pg-publication-bundle-catalog-v1: PostgreSQL-side publication bundle catalog.
--
-- These tables persist execution/output metadata, quality/state metadata,
-- the exact logical pointer registry, and fenced bundle leases for the
-- PostgreSQL source/control backend. They hold metadata only: ClickHouse
-- remains the publication row store and no data rows move here. The version
-- deliberately follows the 000002_*/000003_* migrations owned by the
-- concurrent source-store tickets so the histories interleave without
-- collisions, and the DDL is idempotent because the runner may retry a
-- migration that committed before its ledger row was written.
--
-- Exact logical identity:
--   * An execution's searchable identity is split into explicit scope columns
--     (project, dataset_generation, recipe_name, translation_version) so an
--     exact lookup is a scope equality on all of them -- never a name-only
--     latest fallback.
--   * The full BundleExecution JSON body lives in payload. Reads decode the
--     payload and canonicalize legacy shapes in memory; nothing rewrites it,
--     so historical records keep their stored values (jsonb normalizes only
--     the lexical JSON form, never field values).
--   * Pointer names are arbitrary domain strings (they may embed NUL from the
--     length-prefixed PointerName encoding, which PostgreSQL text and jsonb
--     both reject), so the row key is the hex SHA-256 of the name -- the same
--     discipline as the Arango pointer documents -- and the exact bytes are
--     round-tripped losslessly through bytea.
CREATE TABLE IF NOT EXISTS publication_bundle_executions (
    id                  text        PRIMARY KEY,
    execution_key       text        NOT NULL,
    project             text        NOT NULL,
    dataset_generation  text        NOT NULL,
    recipe_name         text        NOT NULL,
    translation_version text        NOT NULL,
    state               text        NOT NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    ready_at            timestamptz,
    published_at        timestamptz,
    payload             jsonb       NOT NULL,
    CONSTRAINT publication_bundle_executions_payload_object
        CHECK (jsonb_typeof(payload) = 'object')
);

-- Exact publication lookup scope: project + generation + selector. The
-- published/ready/updated ordering mirrors the Arango exact query; id breaks
-- ties so the "latest" answer is deterministic.
CREATE INDEX IF NOT EXISTS publication_bundle_executions_exact_scope
    ON publication_bundle_executions (
        project, dataset_generation, recipe_name, translation_version,
        state, published_at DESC NULLS LAST, ready_at DESC NULLS LAST,
        updated_at DESC, id
    );

-- FindExecutionByKey: newest created_at first, id tie-break for determinism.
CREATE INDEX IF NOT EXISTS publication_bundle_executions_key_created
    ON publication_bundle_executions (execution_key, created_at DESC, id);

-- Reconciliation scans and stable keyset pages: (state, updated_at, id).
CREATE INDEX IF NOT EXISTS publication_bundle_executions_state_scan
    ON publication_bundle_executions (state, updated_at, id);

CREATE TABLE IF NOT EXISTS publication_bundle_pointers (
    pointer_key  text        PRIMARY KEY,
    -- Exact pointer-name bytes. PostgreSQL text and jsonb both reject NUL,
    -- and a PointerName may legally embed one, so the name lives in bytea.
    name_bytes   bytea       NOT NULL,
    execution_id text        NOT NULL,
    updated_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS publication_bundle_leases (
    key         text        PRIMARY KEY,
    owner_id    text        NOT NULL,
    expires_at  timestamptz NOT NULL,
    acquired_at timestamptz NOT NULL,
    -- epoch increments on every grant (including a same-owner reclaim after
    -- expiry), correlating repeated ownership of one lease key so a witness
    -- can distinguish grants.
    epoch       bigint      NOT NULL DEFAULT 1,
    CONSTRAINT publication_bundle_leases_epoch_positive
        CHECK (epoch >= 1)
);
