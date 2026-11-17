-- pg-catalog-02: generation-scoped resource inventory counts with a
-- publish-last completeness marker in the PostgreSQL source store.
--
-- This mirrors the Arango fhir_resource_inventory collection
-- (internal/catalog/resource_inventory.go, internal/catalog/arango/
-- resource_inventory.go) with two tables instead of one polymorphic
-- collection:
--
--   - fhir_resource_inventory_count holds one row per
--     (build_id, project, dataset_generation, resource_type,
--     auth_resource_path) with the exact zero-inclusive document count.
--     Counts from abandoned builds stay invisible because readers bind to
--     the published marker's build id, never to "the latest rows".
--   - fhir_resource_inventory_build holds the one published completeness
--     marker per (project, dataset_generation). A successful rebuild
--     commits every count row first and swaps the marker last; the marker
--     row is the only evidence that a build_id's counts are complete.
--
-- Identity rules mirrored from the shared contract:
--   - count_key is the content-addressed ric_ identity assigned by
--     catalog.NewResourceInventoryBuild (project, generation, build id,
--     resource type, auth path); UNIQUE makes the database, not the
--     caller, the arbiter of one-row-per-scope.
--   - document_count is a non-negative exact count; zero rows are real
--     evidence ("this generated type is empty in this scope"), never a
--     missing row.
--   - auth_resource_path and dataset_generation are typed, non-null scope
--     values; the empty string is the legacy namespace, never "any".
--   - marker_key is the deterministic ribm_ identity derived from
--     (project, dataset_generation), so one scope holds exactly one
--     published marker and republishing is a replacement, never a second
--     marker.
--   - complete is deliberately un-constrained so a reader-side test can
--     seed an incomplete historical marker; the repository only ever
--     publishes complete markers after its commit guard passes.
--
-- The DDL is idempotent (IF NOT EXISTS) because the runner records the
-- ledger row after commit and must converge on retry.
CREATE TABLE IF NOT EXISTS fhir_resource_inventory_count (
    count_key          text        NOT NULL,
    build_id           text        NOT NULL,
    project            text        NOT NULL,
    dataset_generation text        NOT NULL,
    resource_type      text        NOT NULL,
    auth_resource_path text        NOT NULL,
    document_count     bigint      NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_resource_inventory_count_pkey
        PRIMARY KEY (build_id, project, dataset_generation, resource_type, auth_resource_path),
    CONSTRAINT fhir_resource_inventory_count_key_ux
        UNIQUE (count_key),
    CONSTRAINT fhir_resource_inventory_count_nonnegative
        CHECK (document_count >= 0),
    CONSTRAINT fhir_resource_inventory_count_build_present
        CHECK (build_id <> ''),
    CONSTRAINT fhir_resource_inventory_count_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_resource_inventory_count_type_present
        CHECK (resource_type <> '')
);

-- Reader path: sums for one published build inside one project/generation,
-- grouped per resource type and filtered by authorization path.
CREATE INDEX IF NOT EXISTS fhir_resource_inventory_count_scope_ix
    ON fhir_resource_inventory_count (project, dataset_generation, build_id, resource_type);

CREATE TABLE IF NOT EXISTS fhir_resource_inventory_build (
    marker_key          text        NOT NULL,
    project             text        NOT NULL,
    dataset_generation  text        NOT NULL,
    build_id            text        NOT NULL,
    coverage_digest     text        NOT NULL,
    resource_types      text[]      NOT NULL,
    resource_type_count integer     NOT NULL,
    count_row_count     integer     NOT NULL,
    complete            boolean     NOT NULL,
    published_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_resource_inventory_build_pkey
        PRIMARY KEY (project, dataset_generation),
    CONSTRAINT fhir_resource_inventory_build_key_ux
        UNIQUE (marker_key),
    CONSTRAINT fhir_resource_inventory_build_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_resource_inventory_build_build_present
        CHECK (build_id <> ''),
    CONSTRAINT fhir_resource_inventory_build_types_covered
        CHECK (resource_type_count >= 1 AND count_row_count >= resource_type_count)
);
