-- pg-store-05: relationship edges as scoped typed occurrences.
--
-- One table holds every relationship edge (no edge-collection-per-label
-- emulation). Endpoints are stored as typed identities -- resource type plus
-- logical FHIR id -- inside the same project/dataset-generation scope as the
-- resource table (000002_fhir_resource), not as physical document ids, so
-- edges survive the generation-qualified graph-key rewrite that
-- internal/ingest/generation_identity.go applies to Arango documents.
--
-- Identity rules mirrored from the Arango model
-- (internal/ingest/documents.go EdgeDocument, generation_identity.go):
--   - The Arango edge key is derived from source/target/label, so identical
--     repeated references from one source document collapse to one edge.
--     The PostgreSQL contract deliberately does NOT dedupe endpoints: the
--     occurrence identity is
--     (project, dataset_generation, source_resource_type, source_logical_id,
--     label, target_resource_type, target_logical_id, source_path,
--     source_ordinal). Two identical target/label references from different
--     source path occurrences are two rows; only the fully identical
--     occurrence is a conflict.
--   - source_path and source_ordinal carry the repeated-owner correlation:
--     the path locates the reference inside the owning source document and
--     the ordinal distinguishes identical references at one path. The empty
--     source_path is the unqualified (legacy) namespace, never "any path".
--   - logical_edge_key preserves the caller's logical edge key (the
--     collapsed Arango-style key) as a diagnostic only. It carries no
--     constraint: rows that share a logical_edge_key are exactly the
--     multiplicity an endpoint-keyed store would have collapsed. A SQL NULL
--     means the caller supplied no key, distinct from a present key whose
--     value is the empty string.
--   - The target may be unresolved (a dangling FHIR reference). Its
--     resource type and logical id are stored regardless; resolution is a
--     read-time fact about fhir_resource in the same scope, never a stored
--     flag that can go stale. The source, by contrast, must exist in the
--     same project/dataset_generation scope at insert time; that
--     precondition is enforced by the repository (a plain column pair, not
--     a foreign key, so generation cleanup and bulk writes stay in their
--     owning tickets and no silent cascade can delete edges).
--   - project, dataset_generation, and auth_resource_path are typed,
--     non-null scope columns exactly as in fhir_resource; the empty strings
--     are the legacy namespaces.
--
-- The DDL is idempotent (IF NOT EXISTS) because the runner records the
-- ledger row after commit and must converge on retry.
CREATE TABLE IF NOT EXISTS fhir_edge_occurrence (
    edge_seq             bigint      GENERATED ALWAYS AS IDENTITY,
    project              text        NOT NULL,
    dataset_generation   text        NOT NULL,
    auth_resource_path   text        NOT NULL,
    label                text        NOT NULL,
    source_resource_type text        NOT NULL,
    source_logical_id    text        NOT NULL,
    target_resource_type text        NOT NULL,
    target_logical_id    text        NOT NULL,
    source_path          text        NOT NULL,
    source_ordinal       bigint      NOT NULL,
    logical_edge_key     text,
    recorded_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_edge_occurrence_pkey PRIMARY KEY (edge_seq),
    CONSTRAINT fhir_edge_occurrence_identity_ux
        UNIQUE (project, dataset_generation, source_resource_type,
                source_logical_id, label, target_resource_type,
                target_logical_id, source_path, source_ordinal),
    CONSTRAINT fhir_edge_occurrence_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_edge_occurrence_label_present
        CHECK (label <> ''),
    CONSTRAINT fhir_edge_occurrence_source_type_present
        CHECK (source_resource_type <> ''),
    CONSTRAINT fhir_edge_occurrence_source_id_present
        CHECK (source_logical_id <> ''),
    CONSTRAINT fhir_edge_occurrence_target_type_present
        CHECK (target_resource_type <> ''),
    CONSTRAINT fhir_edge_occurrence_target_id_present
        CHECK (target_logical_id <> ''),
    CONSTRAINT fhir_edge_occurrence_ordinal_nonnegative
        CHECK (source_ordinal >= 0)
);

-- Source-directed traversal. Binds project, dataset generation, the source
-- endpoint identity, and (in restricted mode) one auth_resource_path, then
-- walks edge_seq order with no sort. Like the fhir_resource keyset index,
-- multi-path restricted and unrestricted reads use the bound prefix and sort
-- the fetched rows by edge_seq within the scope.
CREATE INDEX IF NOT EXISTS fhir_edge_occurrence_source_traversal_ix
    ON fhir_edge_occurrence (project, dataset_generation,
                             source_resource_type, source_logical_id,
                             auth_resource_path, edge_seq);

-- Target-directed traversal (reverse lookup). Same shape, target-bound:
-- scoped edge lookup by target identity never resolves across
-- project/generation because project and dataset_generation lead the key.
CREATE INDEX IF NOT EXISTS fhir_edge_occurrence_target_traversal_ix
    ON fhir_edge_occurrence (project, dataset_generation,
                             target_resource_type, target_logical_id,
                             auth_resource_path, edge_seq);
