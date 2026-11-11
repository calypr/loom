-- pg-state-interpretation-library-v1: interpretation library heads and
-- immutable project revisions for the Explorer control plane.
--
-- Two tables, composed after the owner history (ledger 1, owner 2, draft
-- revision 3) of this same Explorer control-plane fixture lineage:
--   * interpretation_revisions holds the immutable, content-addressed
--     facts. The ID is the domain's interpretation_<64 hex> hash over the
--     identity envelope and content_digest is sha256 over the canonical
--     content, so the same content re-derives the same row and different
--     immutable content can never share an identity. Rows are create-once:
--     a shared guard function plus a BEFORE UPDATE OR DELETE trigger refuse
--     any mutation of a revision row with SQLSTATE 45501, and the suite
--     proves the guards are not vacuous by dropping the trigger, repeating
--     the violating statement (which must then succeed), and restoring it.
--   * interpretation_libraries holds exactly one mutable authoring head per
--     (project, id): the CAS adapter statement advances head_revision_id
--     and head_digest only while the row still carries the expected head.
--     The library guard trigger refuses DELETE outright, refuses any change
--     to the identity columns (project, id, created_at), and refuses an
--     UPDATE that does not advance the head: the head pointer moves, the
--     row never disappears, nothing else is rewritten.
--
-- Integrity is enforced here, not only in Go:
--   * Identity columns carry COLLATE "C" for exact byte order, matching the
--     owner table and Arango's byte-order comparisons.
--   * Every shape rule the domain enforces is mirrored by a named CHECK:
--     revision id, library id, content digest, parent id/digest, head
--     revision/digest. The parent pair is NULL together (root) or non-NULL
--     together (child): interpretation_revisions_parent_pair.
--   * interpretation_revisions_parent_same_library_fk makes a parent that
--     does not exist in the SAME project AND library a database refusal
--     (23503). The pair is NULL for roots, so the composite key skips
--     them. interpretation_libraries_head_same_library_fk makes a head that
--     points at a nonexistent revision, or at a revision of another
--     library, a database refusal: the browse affordance the lifecycle
--     expands can never dangle by construction.
--   * The jsonb payload is authoritative for reads (the adapter re-verifies
--     payload self-consistency and mirror agreement on every read); the
--     scalar mirrors exist for scope guards, constraints, and ordered scans.
--     jsonb preserves the missing-versus-JSON-null member shapes of the
--     canonical revision document untouched.
--   * The DDL is rerun-safe (IF NOT EXISTS, DROP TRIGGER IF EXISTS before
--     each CREATE TRIGGER): the pg-store-02 runner may retry a migration
--     that committed before its ledger row was written. This migration
--     never drops or truncates tables.

CREATE TABLE IF NOT EXISTS interpretation_revisions (
    project             text        NOT NULL COLLATE "C",
    id                  text        NOT NULL COLLATE "C",
    library_id          text        NOT NULL COLLATE "C",
    parent_revision_id  text        COLLATE "C",
    parent_digest       text,
    content_digest      text        NOT NULL,
    author              text        NOT NULL,
    explanation         text        NOT NULL,
    created_at          timestamptz NOT NULL,
    payload             jsonb       NOT NULL,
    CONSTRAINT interpretation_revisions_identity_pk
        PRIMARY KEY (project, id),
    CONSTRAINT interpretation_revisions_library_id_ux
        UNIQUE (project, id, library_id),
    CONSTRAINT interpretation_revisions_project_non_empty
        CHECK (project <> ''),
    CONSTRAINT interpretation_revisions_id_shape
        CHECK (id ~ '^interpretation_[0-9a-f]{64}$'),
    CONSTRAINT interpretation_revisions_library_id_shape
        CHECK (library_id ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
    CONSTRAINT interpretation_revisions_content_digest_shape
        CHECK (content_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT interpretation_revisions_parent_pair
        CHECK ((parent_revision_id IS NULL) = (parent_digest IS NULL)),
    CONSTRAINT interpretation_revisions_parent_revision_shape
        CHECK (parent_revision_id IS NULL OR parent_revision_id ~ '^interpretation_[0-9a-f]{64}$'),
    CONSTRAINT interpretation_revisions_parent_digest_shape
        CHECK (parent_digest IS NULL OR parent_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT interpretation_revisions_author_non_empty
        CHECK (author <> ''),
    CONSTRAINT interpretation_revisions_explanation_non_empty
        CHECK (explanation <> ''),
    CONSTRAINT interpretation_revisions_payload_object
        CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT interpretation_revisions_parent_same_library_fk
        FOREIGN KEY (project, parent_revision_id, library_id)
        REFERENCES interpretation_revisions (project, id, library_id)
);

-- Retrieval mirrors the Arango adapter's revision lookup (identity PK) plus
-- the library-scoped chain scans.
CREATE INDEX IF NOT EXISTS interpretation_revisions_library_ix
    ON interpretation_revisions (project, library_id, id);

CREATE TABLE IF NOT EXISTS interpretation_libraries (
    project           text        NOT NULL COLLATE "C",
    id                text        NOT NULL COLLATE "C",
    head_revision_id  text        NOT NULL COLLATE "C",
    head_digest       text        NOT NULL,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT interpretation_libraries_identity_pk
        PRIMARY KEY (project, id),
    CONSTRAINT interpretation_libraries_project_non_empty
        CHECK (project <> ''),
    CONSTRAINT interpretation_libraries_id_shape
        CHECK (id ~ '^[A-Za-z][A-Za-z0-9_.:-]{0,127}$'),
    CONSTRAINT interpretation_libraries_head_revision_shape
        CHECK (head_revision_id ~ '^interpretation_[0-9a-f]{64}$'),
    CONSTRAINT interpretation_libraries_head_digest_shape
        CHECK (head_digest ~ '^sha256:[0-9a-f]{64}$'),
    CONSTRAINT interpretation_libraries_head_same_library_fk
        FOREIGN KEY (project, head_revision_id, id)
        REFERENCES interpretation_revisions (project, id, library_id)
);

-- The immutability guard: revision rows are create-once. Any UPDATE or
-- DELETE -- from any session, including one holding the table owner's
-- rights -- aborts with SQLSTATE 45501 before touching a page. The adapter
-- never issues UPDATE/DELETE against these tables; the trigger is what
-- makes that a database-enforced property rather than a convention.
CREATE OR REPLACE FUNCTION loom_interpretation_revision_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'loom: interpretation revision rows are immutable (create-once)'
        USING ERRCODE = '45501';
END;
$$;

-- The head-advance guard: a library row may only ever advance its head.
-- DELETE is refused outright; UPDATE must keep project, id, and created_at
-- byte-identical and must actually move head_revision_id. The adapter's
-- single CAS statement is the only writer that satisfies this contract.
CREATE OR REPLACE FUNCTION loom_interpretation_library_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'loom: interpretation library heads are never deleted'
            USING ERRCODE = '45501';
    END IF;
    IF NEW.project IS DISTINCT FROM OLD.project
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.head_revision_id = OLD.head_revision_id THEN
        RAISE EXCEPTION 'loom: interpretation library identity is stable and every update must advance the head'
            USING ERRCODE = '45501';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS interpretation_revisions_immutable ON interpretation_revisions;
CREATE TRIGGER interpretation_revisions_immutable
    BEFORE UPDATE OR DELETE ON interpretation_revisions
    FOR EACH ROW EXECUTE FUNCTION loom_interpretation_revision_immutable();

DROP TRIGGER IF EXISTS interpretation_libraries_head_guard ON interpretation_libraries;
CREATE TRIGGER interpretation_libraries_head_guard
    BEFORE UPDATE OR DELETE ON interpretation_libraries
    FOR EACH ROW EXECUTE FUNCTION loom_interpretation_library_guard();
