-- pg-catalog-01: bounded field-profile records persisted from the write-side
-- catalog.Profiler (internal/catalog/write_profiler.go Documents()) in the
-- PostgreSQL source store, replacing the Arango fhir_field_catalog
-- collection for this backend (internal/catalog/arango/fields.go is the
-- behavioral reference).
--
-- Two tables instead of one polymorphic Arango collection:
--
--   - fhir_field_profile holds one row per field-profile identity
--     (project, dataset_generation, auth_resource_path, resource_type, path)
--     plus the scope-level profile_status record (path = '', record_type =
--     'profile_status') that the profiler emits when enrichment truncated
--     with no fields retained. Query identity is normalized into typed
--     columns; JSONB is used only for the correlated structured subrecords
--     (extension_values, semantic_observations), where each element is one
--     correlated observation and the correlation must never be flattened
--     into independently cross-combinable value sets.
--   - fhir_field_profile_coverage holds one coverage marker per profiler
--     scope (project, dataset_generation, auth_resource_path, resource_type).
--     It is what makes a persisted known-empty profile distinguishable from
--     a scope that was never profiled: the profiler emits zero documents for
--     a clean empty population, so without this marker "empty" and
--     "unavailable" would collapse into the same zero-row read (the Arango
--     backend gets this for free from collection existence; a relational
--     table created by a migration always exists).
--
-- Fidelity rules pinned by internal/catalog/postgres tests:
--   - Every public field of catalog.FieldCatalogDocument round-trips
--     unchanged, including false booleans and zero counts.
--   - Optional text columns are NOT NULL with no DEFAULT: Go strings have
--     no missing/empty distinction, the repository always supplies all 26
--     column values, so the Go zero value round-trips exactly, and a direct
--     SQL writer that omits a column gets a NOT NULL violation rather than
--     a silent empty string.
--   - enrichment_complete is a nullable boolean: SQL NULL is the missing
--     *bool (nil), true/false are the explicit states. It is never coerced.
--   - distinct_values, pivot_columns, and pivot_value_selectors are text[]:
--     SQL NULL and '{}' are preserved distinctly in the database, and both
--     decode to a nil Go slice (the profiler only ever emits nil-or-populated
--     via append(nil, ...) copies). The adapter passes nil for an empty Go
--     slice, so a profiler round-trip keeps nil as nil.
--   - extension_values and semantic_observations are jsonb arrays: SQL NULL
--     is the missing (nil) Go slice, '[]' is an empty-but-non-nil slice,
--     and a populated array holds one object per correlated observation.
--     The adapter encodes the Go nil/empty/populated distinction explicitly
--     (encoding/json's omitempty would otherwise erase it inside member
--     slices such as a profiler-retained empty Examples list). A jsonb
--     'null' or malformed non-array value is refused by the type checks
--     below (which pass on SQL NULL because jsonb_typeof(NULL) is NULL and
--     CHECK accepts unknown).
--   - Identity columns carry COLLATE "C" so the primary key and ORDER BY
--     use exact byte order, matching Arango's byte-order SORT and the
--     profiler's Go byte-order sort of paths.
--   - profile_key is stored as written (the profiler's canonical
--     field-catalog key) and returned unchanged. It is deliberately NOT the
--     primary key: the legacy (empty-generation) key derivation sanitizes
--     its components many-to-one, so the full identity tuple -- not the
--     derived key -- is the one-row-per-profile arbiter. Two distinct
--     scopes are therefore never merged, and upsert replaces exactly one
--     identity.
--   - The DDL is idempotent (IF NOT EXISTS) because the runner records the
--     ledger row after commit and must converge on retry.
--
-- Versioning: this migration is 000007, not 000006. Version 000006 was
-- reserved for pg-catalog-04's semantic_inventory_build migration while
-- this ticket was in flight, so this migration was numbered 7 and the
-- branch history temporarily skipped version 6 (the runner tolerates the
-- gap). That reservation has been delivered: #73 (eafbf05a) merged
-- 000006_semantic_inventory_build.sql, and the embedded history is the
-- contiguous chain 1..7. The jump in numbering existed because of the
-- runner contract: MigrationRunner.pending() refuses any pending
-- migration whose version is below the highest already-recorded version
-- (*OutOfOrderMigrationError, ErrMigrationOutOfOrder), so once 000007 is
-- applied to a database, 000006 can never be applied there. The merge
-- queue therefore sequenced catalog-04 first: 000006 merged AND was
-- applied to every database before 000007 reached any. (A database that
-- had applied 000007 during the in-flight gap window would have been
-- permanently unable to receive 000006 -- which is why this ticket
-- waited for #73 to land before merging.)
CREATE TABLE IF NOT EXISTS fhir_field_profile (
    project                  text    NOT NULL COLLATE "C",
    dataset_generation       text    NOT NULL COLLATE "C",
    auth_resource_path       text    NOT NULL COLLATE "C",
    resource_type            text    NOT NULL COLLATE "C",
    path                     text    NOT NULL COLLATE "C",
    profile_key              text    NOT NULL,
    record_type              text    NOT NULL,
    kind                     text    NOT NULL,
    doc_count                bigint  NOT NULL,
    max_items                bigint  NOT NULL,
    sample_count             bigint  NOT NULL,
    distinct_values          text[],
    distinct_truncated       boolean NOT NULL,
    enrichment_complete      boolean,
    enrichment_truncated     boolean NOT NULL,
    pivot_candidate          boolean NOT NULL,
    pivot_kind               text    NOT NULL,
    pivot_columns            text[],
    pivot_family             text    NOT NULL,
    pivot_column_selector    text    NOT NULL,
    pivot_value_selector     text    NOT NULL,
    pivot_item_source        text    NOT NULL,
    pivot_item_resource_type text    NOT NULL,
    pivot_value_selectors    text[],
    extension_values         jsonb,
    semantic_observations    jsonb,
    CONSTRAINT fhir_field_profile_pkey
        PRIMARY KEY (project, dataset_generation, auth_resource_path, resource_type, path),
    CONSTRAINT fhir_field_profile_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_field_profile_type_present
        CHECK (resource_type <> ''),
    CONSTRAINT fhir_field_profile_key_present
        CHECK (profile_key <> ''),
    -- One shape per record kind: a field profile always has a path and a
    -- kind; the scope-level profile_status marker never does. Mixed shapes
    -- (a status row with a path, a field row without a kind) are refused.
    CONSTRAINT fhir_field_profile_record_shape
        CHECK ((record_type = '' AND path <> '' AND kind <> '')
            OR (record_type = 'profile_status' AND path = '')),
    CONSTRAINT fhir_field_profile_record_type_known
        CHECK (record_type IN ('', 'profile_status')),
    CONSTRAINT fhir_field_profile_counts_nonnegative
        CHECK (doc_count >= 0 AND max_items >= 0 AND sample_count >= 0),
    -- Correlated subrecords are jsonb arrays (or SQL NULL when missing).
    CONSTRAINT fhir_field_profile_extension_is_array
        CHECK (jsonb_typeof(extension_values) = 'array'),
    CONSTRAINT fhir_field_profile_semantic_is_array
        CHECK (jsonb_typeof(semantic_observations) = 'array')
);

-- Reader path: discovery reads bind project, dataset generation, then
-- filter resource types and (in restricted mode) authorization paths.
CREATE INDEX IF NOT EXISTS fhir_field_profile_scope_ix
    ON fhir_field_profile (project, dataset_generation, resource_type, auth_resource_path);

CREATE TABLE IF NOT EXISTS fhir_field_profile_coverage (
    project              text        NOT NULL COLLATE "C",
    dataset_generation   text        NOT NULL COLLATE "C",
    auth_resource_path   text        NOT NULL COLLATE "C",
    resource_type        text        NOT NULL COLLATE "C",
    -- Nullable: SQL NULL is the missing *bool enrichment-completeness
    -- claim, exactly like fhir_field_profile.enrichment_complete.
    enrichment_complete  boolean,
    enrichment_truncated boolean     NOT NULL,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fhir_field_profile_coverage_pkey
        PRIMARY KEY (project, dataset_generation, auth_resource_path, resource_type),
    CONSTRAINT fhir_field_profile_coverage_project_present
        CHECK (project <> ''),
    CONSTRAINT fhir_field_profile_coverage_type_present
        CHECK (resource_type <> '')
);
