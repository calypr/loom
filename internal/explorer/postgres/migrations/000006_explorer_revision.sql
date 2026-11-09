-- pg-state-receipt-revision-immutable-v1: immutable Explorer revision table.
--
-- One row is one Explorer revision, identified by its globally unique id.
-- A revision's frozen content (definition, recipe, digests, source
-- generation, materialization mappings, emitted columns, dataset metadata,
-- quality reports, authoring payloads) can never change after InsertRevision
-- -- the exact immutable boundary the Arango adapter enforces only by
-- refusing an overwrite in Go (internal/explorer/arango/revisions.go
-- sameRevisionContent). Here the DB enforces it too: the row-level BEFORE
-- UPDATE trigger loom_explorer_revision_guard raises a check_violation
-- (SQLSTATE 23514) whenever a frozen column changes, so no direct SQL can
-- rewrite frozen content. Tests prove the guard non-vacuously: with the
-- trigger dropped the same violating UPDATE succeeds, and recreating it from
-- this migration's text blocks it again.
--
-- Lifecycle transitions are a guarded monotone state machine, also enforced
-- here: only READY->ACTIVE, READY->FAILED, ACTIVE->SUPERSEDED and
-- ACTIVE->FAILED are legal, each in one statement. Re-setting a status
-- (the double transition, e.g. FAILED->FAILED with fresh diagnostics),
-- resurrecting a FAILED/SUPERSEDED revision, un-activating an ACTIVE
-- revision, or mutating mutable columns while keeping the same status is
-- refused with 23514. A no-op UPDATE that changes nothing is a harmless
-- no-op and stays allowed. Go-side CAS (FailRevision/FailRevisionFrom with
-- an expected-status token) refuses the same cases as typed errors without
-- ever writing.
--
-- Column policy:
--   - Identity/lifecycle scalars are typed columns; identity columns carry
--     COLLATE "C" for exact byte-order uniqueness and index order.
--   - config, authoring_bundle, public_output_contract preserve the Go
--     json.RawMessage tri-state losslessly: missing (nil) is SQL NULL,
--     JSON-null is the jsonb value 'null', a document is the document.
--     Nothing is coerced across those states; tests assert each with
--     literal rows.
--   - materializations, emitted_columns, quality_reports, diagnostics are
--     nullable jsonb arrays: a nil Go slice is SQL NULL (missing), an empty
--     slice is '[]', a populated slice is the array -- each survives a
--     fresh-connection read. dataset and publication are Go value structs
--     and therefore always objects.
--   - recipe is the canonical recipe Bundle (jsonb object) and content_digest
--     is the sha256 content identity of the frozen column set, computed by
--     the adapter over the exact same field set Arango compares
--     (immutableRevision). Same id + same digest is the idempotent replay;
--     same id + different digest is the typed ErrImmutableRevision refusal.
--     The CHECK keeps it a canonical hex digest so no row can arrive
--     without its content witness.
--   - explorer_revisions_status_known closes the status domain
--     (READY/ACTIVE/SUPERSEDED/FAILED).
--
-- This migration never drops or truncates state: rerunning it on an existing
-- schema (crash in the commit-before-record window) is a no-op convergence;
-- CREATE OR REPLACE FUNCTION plus DROP TRIGGER IF EXISTS + CREATE TRIGGER
-- re-establish the identical guard, never a weakened one.

CREATE TABLE IF NOT EXISTS explorer_revisions (
    id                     text        NOT NULL COLLATE "C",
    project                text        NOT NULL COLLATE "C",
    explorer_id            text        NOT NULL COLLATE "C",
    config                 jsonb,
    authoring_bundle       jsonb,
    intent_digest          text        NOT NULL DEFAULT '',
    compilation_receipt_id text        NOT NULL DEFAULT '',
    public_output_contract jsonb,
    recipe                 jsonb       NOT NULL,
    recipe_digest          text        NOT NULL DEFAULT '',
    resolved_schema_digest text        NOT NULL DEFAULT '',
    source_generation      text        NOT NULL DEFAULT '',
    materializations       jsonb,
    emitted_columns        jsonb,
    dataset                jsonb       NOT NULL,
    quality_reports        jsonb,
    content_digest         text        NOT NULL,
    status                 text        NOT NULL,
    diagnostics            jsonb,
    publication            jsonb       NOT NULL,
    created_by             text        NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL,
    ready_at               timestamptz,
    activated_at           timestamptz,
    failed_at              timestamptz,
    CONSTRAINT explorer_revisions_pkey PRIMARY KEY (id),
    CONSTRAINT explorer_revisions_status_known
        CHECK (status IN ('READY', 'ACTIVE', 'SUPERSEDED', 'FAILED')),
    CONSTRAINT explorer_revisions_content_digest_shape
        CHECK (content_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT explorer_revisions_recipe_object
        CHECK (jsonb_typeof(recipe) = 'object'),
    CONSTRAINT explorer_revisions_dataset_object
        CHECK (jsonb_typeof(dataset) = 'object'),
    CONSTRAINT explorer_revisions_publication_object
        CHECK (jsonb_typeof(publication) = 'object'),
    CONSTRAINT explorer_revisions_materializations_shape
        CHECK (materializations IS NULL OR jsonb_typeof(materializations) = 'array'),
    CONSTRAINT explorer_revisions_emitted_columns_shape
        CHECK (emitted_columns IS NULL OR jsonb_typeof(emitted_columns) = 'array'),
    CONSTRAINT explorer_revisions_quality_reports_shape
        CHECK (quality_reports IS NULL OR jsonb_typeof(quality_reports) = 'array'),
    CONSTRAINT explorer_revisions_diagnostics_shape
        CHECK (diagnostics IS NULL OR jsonb_typeof(diagnostics) = 'array')
);

CREATE INDEX IF NOT EXISTS explorer_revisions_project_explorer
    ON explorer_revisions (project, explorer_id);

CREATE OR REPLACE FUNCTION loom_explorer_revision_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- A true no-op (every column identical) changes nothing and stays legal.
    IF ROW(NEW.*) IS NOT DISTINCT FROM ROW(OLD.*) THEN
        RETURN NEW;
    END IF;

    IF NEW.id                     IS DISTINCT FROM OLD.id
        OR NEW.project            IS DISTINCT FROM OLD.project
        OR NEW.explorer_id        IS DISTINCT FROM OLD.explorer_id
        OR NEW.config             IS DISTINCT FROM OLD.config
        OR NEW.authoring_bundle   IS DISTINCT FROM OLD.authoring_bundle
        OR NEW.intent_digest      IS DISTINCT FROM OLD.intent_digest
        OR NEW.compilation_receipt_id IS DISTINCT FROM OLD.compilation_receipt_id
        OR NEW.public_output_contract IS DISTINCT FROM OLD.public_output_contract
        OR NEW.recipe             IS DISTINCT FROM OLD.recipe
        OR NEW.recipe_digest      IS DISTINCT FROM OLD.recipe_digest
        OR NEW.resolved_schema_digest IS DISTINCT FROM OLD.resolved_schema_digest
        OR NEW.source_generation  IS DISTINCT FROM OLD.source_generation
        OR NEW.materializations   IS DISTINCT FROM OLD.materializations
        OR NEW.emitted_columns    IS DISTINCT FROM OLD.emitted_columns
        OR NEW.dataset            IS DISTINCT FROM OLD.dataset
        OR NEW.quality_reports    IS DISTINCT FROM OLD.quality_reports
        OR NEW.content_digest     IS DISTINCT FROM OLD.content_digest
        OR NEW.created_by         IS DISTINCT FROM OLD.created_by
        OR NEW.created_at         IS DISTINCT FROM OLD.created_at
    THEN
        RAISE EXCEPTION
            'explorer revision % frozen content changed', OLD.id
            USING ERRCODE = '23514';
    END IF;

    IF NOT (
           (OLD.status = 'READY'  AND NEW.status IN ('ACTIVE', 'FAILED'))
        OR (OLD.status = 'ACTIVE' AND NEW.status IN ('SUPERSEDED', 'FAILED'))
    ) THEN
        RAISE EXCEPTION
            'illegal explorer revision % status transition % -> %',
            OLD.id, OLD.status, NEW.status
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS explorer_revisions_guard ON explorer_revisions;
CREATE TRIGGER explorer_revisions_guard
    BEFORE UPDATE ON explorer_revisions
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_revision_guard();
