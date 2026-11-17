-- pg-catalog-05: deterministic semantic-inventory contribution persistence
-- for the PostgreSQL source-store retained-source backfill
-- (internal/ingest/semantic_inventory_backfill.go), owned by
-- internal/catalog/postgres/semantic_contribution.go. The Arango behavioral
-- reference is the fhir_semantic_inventory collection written by
-- internal/catalog/arango/semantic_inventory.go WriteSemanticInventory-
-- Contributions.
--
-- One row per contribution identity. The contribution key is the
-- content-addressed digest the semantic emitter derives from
-- (project, dataset generation, auth path, resource type, build id, source
-- id, ordinal[, source kind]) — so identity is build/source/ordinal scoped,
-- and replaying one generation replaces the same key, never a near-duplicate.
--
-- Identity and replay rules (mirroring the pg-store-06 batch ledger digest
-- discipline):
--   - contribution_key is the content-addressed identity. UNIQUE makes the
--     database, not the caller, the arbiter of "this identity has written
--     once"; the writer replays it idempotently by payload digest and
--     refuses a differing payload at that identity as a typed conflict
--     instead of overwriting.
--   - The primary key is the (build, source, ordinal) slot — the
--     architecture's contribution identity — so a second, differently keyed
--     contribution can never claim one source slot, and every guard is one
--     statement.
--   - payload_digest is the lowercase hex SHA-256 of the canonical Go
--     encoding of the payload-bearing content (auth path, resource type,
--     concept, binding, example, observation). Equal digest at one key is a
--     no-op replay; a differing digest is refused. PostgreSQL stores the
--     digest as an opaque exact string: no SQL-side re-encoding, trim, or
--     lowering participates in identity comparison (the single source of
--     truth is Go post-canonicalization).
--   - observation is the observation JSONB. PostgreSQL canonicalizes JSONB
--     (key order, duplicate keys, numeric literals), so no byte-exact round
--     trip is promised; the semantic identity of "same payload" is carried
--     by payload_digest, and the observation type check refuses anything
--     that is not a JSON object.
--   - Identity columns carry COLLATE "C" so the primary key, the unique
--     key, and the classification lookups use exact byte order, matching
--     migration 000007's convention and Go string comparison.
--   - Every column is NOT NULL: a missing domain value is the typed empty
--     string or zero, never SQL NULL, so SQL NULL carries no meaning here.
--
-- The supporting index (project, dataset_generation, build_id, source_kind,
-- binding_id, concept_id, auth_resource_path) mirrors the Arango
-- contribution collection's page index for later bounded catalog page
-- reads; the source index supports per-source replay audits.
--
-- The trailing CREATE INDEX extends the pg-store-04 fhir_resource table
-- with the byte-order keyset index this slice's retained-source page
-- reader needs: pages walk (project, dataset_generation, resource_type,
-- logical_id COLLATE "C") with no sort, matching the adapter's COLLATE "C"
-- predicate exactly. It is purely additive (IF NOT EXISTS): it modifies no
-- column, constraint, or existing index of that table.
--
-- The DDL is idempotent (IF NOT EXISTS) because the runner records the
-- ledger row after commit and must converge on retry.
CREATE TABLE IF NOT EXISTS fhir_semantic_inventory_contribution (
    contribution_key   text        NOT NULL COLLATE "C",
    project            text        NOT NULL COLLATE "C",
    dataset_generation text        NOT NULL COLLATE "C",
    build_id           text        NOT NULL COLLATE "C",
    source_kind        text        NOT NULL COLLATE "C",
    auth_resource_path text        NOT NULL COLLATE "C",
    resource_type      text        NOT NULL COLLATE "C",
    source_id          text        NOT NULL COLLATE "C",
    ordinal            integer     NOT NULL,
    concept_id         text        NOT NULL COLLATE "C",
    binding_id         text        NOT NULL COLLATE "C",
    example            text        NOT NULL COLLATE "C",
    payload_digest     text        NOT NULL COLLATE "C",
    observation        jsonb       NOT NULL,
    recorded_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_semantic_inventory_contribution_pkey
        PRIMARY KEY (project, dataset_generation, build_id, source_kind, source_id, ordinal),
    CONSTRAINT fhir_semantic_inventory_contribution_key_ux
        UNIQUE (contribution_key),
    CONSTRAINT fhir_semantic_inventory_contribution_key_shape
        CHECK (contribution_key ~ '^[0-9a-f]{64}$'),
    CONSTRAINT fhir_semantic_inventory_contribution_digest_shape
        CHECK (payload_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT fhir_semantic_inventory_contribution_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_semantic_inventory_contribution_build_present
        CHECK (build_id <> ''),
    CONSTRAINT fhir_semantic_inventory_contribution_build_shape
        CHECK (build_id ~ '^[0-9a-f]{64}$'),
    CONSTRAINT fhir_semantic_inventory_contribution_resource_type_present
        CHECK (resource_type <> ''),
    CONSTRAINT fhir_semantic_inventory_contribution_source_present
        CHECK (source_id <> ''),
    CONSTRAINT fhir_semantic_inventory_contribution_concept_present
        CHECK (concept_id <> ''),
    CONSTRAINT fhir_semantic_inventory_contribution_binding_present
        CHECK (binding_id <> ''),
    CONSTRAINT fhir_semantic_inventory_contribution_ordinal_nonnegative
        CHECK (ordinal >= 0),
    CONSTRAINT fhir_semantic_inventory_contribution_source_kind_known
        CHECK (source_kind IN ('file', 'retained_vertex')),
    CONSTRAINT fhir_semantic_inventory_contribution_observation_object
        CHECK (jsonb_typeof(observation) = 'object')
);

CREATE INDEX IF NOT EXISTS fhir_semantic_inventory_contribution_scope_ix
    ON fhir_semantic_inventory_contribution
    (project, dataset_generation, build_id, source_kind, binding_id, concept_id, auth_resource_path);

CREATE INDEX IF NOT EXISTS fhir_semantic_inventory_contribution_source_ix
    ON fhir_semantic_inventory_contribution
    (project, dataset_generation, build_id, source_kind, source_id);

CREATE INDEX IF NOT EXISTS fhir_resource_semantic_keyset_ix
    ON fhir_resource (project, dataset_generation, resource_type, logical_id COLLATE "C");
