-- pg-store-04: scoped FHIR resource persistence in the PostgreSQL source store.
--
-- One table holds every FHIR resource type (no collection-per-resource
-- emulation). The storage identity fields that are also graph fields on the
-- Arango vertex (project, dataset generation, resource type, logical FHIR id,
-- auth path) become typed columns, and the original resource object is stored
-- as JSONB in payload. PostgreSQL canonicalizes JSONB -- object key order,
-- duplicate keys (last value wins), and numeric literals (1e2 becomes 100) --
-- so no byte-exact round trip is promised; the guarantees are the semantic
-- ones below.
--
-- Identity and scope rules mirrored from the Arango model
-- (internal/ingest/generation_identity.go, internal/ingest/documents.go):
--   - The natural identity is (project, dataset_generation, resource_type,
--     logical_id). The generation-qualified Arango _key is derived from
--     exactly these fields, so the same natural identity may never carry a
--     second payload: the UNIQUE constraint makes the database, not the
--     caller, the arbiter of "insert only".
--   - auth_resource_path is a typed, non-null scope value. The empty string
--     is the legacy (unscoped) namespace, mirroring the omitempty vertex
--     field; restricted readers bind it explicitly alongside project and
--     dataset generation.
--   - dataset_generation is a typed, non-null scope value. The empty string
--     is the legacy dataset namespace, never "any generation", matching the
--     catalog contract that readers bind either an exact value or the legacy
--     empty value.
--   - payload is nullable so an absent resource object (SQL NULL) stays
--     distinct from an explicit JSON null document and from JSON values
--     inside a document. JSONB preserves present-but-null keys, empty
--     arrays, and empty objects; a missing key never appears.
--   - resource_seq is the server-assigned storage identity. It is immutable
--     once assigned, so keyset reads ordered by it are stable regardless of
--     later inserts.
--
-- The DDL is idempotent (IF NOT EXISTS) because the runner records the
-- ledger row after commit and must converge on retry.
CREATE TABLE IF NOT EXISTS fhir_resource (
    resource_seq       bigint      GENERATED ALWAYS AS IDENTITY,
    project            text        NOT NULL,
    dataset_generation text        NOT NULL,
    resource_type      text        NOT NULL,
    logical_id         text        NOT NULL,
    auth_resource_path text        NOT NULL,
    payload            jsonb,
    recorded_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_resource_pkey PRIMARY KEY (resource_seq),
    CONSTRAINT fhir_resource_natural_identity_ux
        UNIQUE (project, dataset_generation, resource_type, logical_id),
    CONSTRAINT fhir_resource_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_resource_resource_type_present
        CHECK (resource_type <> ''),
    CONSTRAINT fhir_resource_logical_id_present
        CHECK (logical_id <> '')
);

-- Scope-scoped keyset scans. A single-path restricted read binds project,
-- dataset generation, and one auth_resource_path, so it walks this index in
-- resource_seq order with no sort. A multi-path restricted read
-- (auth_resource_path = ANY(...)) and the unrestricted mode leave
-- auth_resource_path unbound or multi-valued: PostgreSQL still uses this
-- index for the bound prefix but must sort the fetched rows by resource_seq
-- within the scope. That sort is bounded by the scope's row count and the
-- keyset predicate; revisit the index shape if it ever shows up in capacity
-- profiles.
CREATE INDEX IF NOT EXISTS fhir_resource_scope_seq_ix
    ON fhir_resource (project, dataset_generation, auth_resource_path, resource_seq);
