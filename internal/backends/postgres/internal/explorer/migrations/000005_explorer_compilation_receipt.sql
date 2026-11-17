-- pg-state-receipt-revision-immutable-v1: compilation receipt table.
--
-- One row is one durable, content-addressed CompilationReceipt for exactly
-- one Explorer, identified by the unique (project, explorer_id, id) triple.
-- Receipts are insert-once: the Arango adapter's insert-once + scoped-reread
-- contract (internal/explorer/arango/receipts.go) becomes a relational
-- insert-once claim, and the DB itself refuses every later mutation.
--
-- Column policy:
--   - Identity columns carry COLLATE "C" so uniqueness uses exact byte order,
--     matching the owner table and Arango's byte-order _key semantics.
--   - The digest/generation/recipe-identity fields the adapter re-checks on
--     every conflict (intent_digest, snapshot_token, source_generation,
--     recipe_digest, resolved_recipe_digest, compilation_key) plus the
--     contract columns are projected into typed columns so collision
--     reports name real columns, and every one of them is frozen below.
--   - doc is the full serialized CompilationReceipt as jsonb: lossless for
--     the embedded json.RawMessage tri-state (missing key, the jsonb value
--     'null', or a document), the canonical recipe Bundle, and the exact
--     RFC3339Nano createdAt string. jsonb normalizes key order/whitespace
--     only (never a member or value); the canonical ReceiptID digest -- not
--     jsonb byte order -- is the content check.
--   - compilation_receipts_doc_object refuses a scalar/array doc (23514);
--     compilation_receipts_format_version_non_negative keeps the format
--     version in range.
--
-- Immutability is ENFORCED, not documented: the row-level BEFORE UPDATE OR
-- DELETE trigger loom_explorer_receipts_frozen raises a check_violation
-- (SQLSTATE 23514) for every mutation attempt, so digests, generation,
-- recipe content inside doc, and every other column cannot be changed or
-- deleted by any SQL, including direct psql. Tests prove the guard
-- non-vacuously: with the trigger dropped the same violating write succeeds,
-- and recreating it from this migration's text blocks it again.
--
-- This migration never drops or truncates state: rerunning it on an existing
-- schema (crash in the commit-before-record window) is a no-op convergence.

CREATE TABLE IF NOT EXISTS compilation_receipts (
    project                  text   NOT NULL COLLATE "C",
    explorer_id              text   NOT NULL COLLATE "C",
    id                       text   NOT NULL COLLATE "C",
    receipt_format_version   bigint NOT NULL,
    compiler_contract_version text  NOT NULL DEFAULT '',
    compilation_key          text   NOT NULL DEFAULT '',
    intent_digest            text   NOT NULL DEFAULT '',
    snapshot_token           text   NOT NULL DEFAULT '',
    source_generation        text   NOT NULL DEFAULT '',
    recipe_digest            text   NOT NULL DEFAULT '',
    resolved_recipe_digest   text   NOT NULL DEFAULT '',
    doc                      jsonb  NOT NULL,
    CONSTRAINT compilation_receipts_pkey PRIMARY KEY (project, explorer_id, id),
    CONSTRAINT compilation_receipts_format_version_non_negative
        CHECK (receipt_format_version >= 0),
    CONSTRAINT compilation_receipts_doc_object
        CHECK (jsonb_typeof(doc) = 'object')
);

CREATE OR REPLACE FUNCTION loom_explorer_receipts_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION
        'compilation_receipts is insert-once: % of receipt %/%/% is refused',
        TG_OP, OLD.project, OLD.explorer_id, OLD.id
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS compilation_receipts_frozen ON compilation_receipts;
CREATE TRIGGER compilation_receipts_frozen
    BEFORE UPDATE OR DELETE ON compilation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_receipts_frozen();
