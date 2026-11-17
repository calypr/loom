-- pg-state-explorer-draft-cas-v1: immutable exact-preimage history for
-- Explorer draft saves.
--
-- One row is the lossless snapshot of the owner state that a SaveDraft CAS
-- winner replaced: the preimage's draft version, digest, config packet,
-- title, source context, author, and timestamp. Together with the owner row
-- it forms the Undo chain the restore command reads (previous_draft_revision
-- _id on explorers points here). SaveDraft inserts this row and updates the
-- owner inside ONE transaction, so a failed or aborted CAS leaves neither a
-- head change nor a history row (pinned by TestDraftCASRollsBackHistory* on
-- real PG18).
--
-- Identity policy (parity with the Arango adapter's draftRevisionKey):
--   * The version identity (project, explorer_id, draft_version) is the
--     primary key: exactly one exact preimage may ever exist for one
--     Explorer draft version. Competing same-version writers serialize on
--     the unique index inside PostgreSQL; the loser receives a 23505 that
--     SaveDraft maps to explorer.ErrDraftConflict retaining the
--     *pgconn.PgError cause. The row is append-only: nothing here is ever
--     updated, and the CAS flow never deletes history.
--   * The opaque id is the derived draft_revision_<64 hex> key, unique per
--     (project, explorer_id) so restore lookups resolve it through the
--     scoped unique index and a foreign project/Explorer never sees it.
--     explorers_draft_revision_id_shape keeps ids in the derived shape.
--   * Identity columns carry COLLATE "C" for exact byte order, matching the
--     owner table and Arango's byte-order comparisons.
--
-- Column policy mirrors 000002_explorer_owner.sql: Go strings have no
-- missing/empty distinction, so optional text columns are NOT NULL DEFAULT
-- ''; draft_config is nullable jsonb preserving the three json.RawMessage
-- states losslessly (missing is SQL NULL, JSON-null is the jsonb value
-- 'null', a document is the document). draft_version >= 0 keeps preimages
-- in the CAS counter's meaningful range (version 0 is the legitimate empty
-- first draft). This migration never drops or truncates: the runner may
-- rerun a migration that committed before its ledger row was written.
CREATE TABLE IF NOT EXISTS explorers_draft_revisions (
    project                  text        NOT NULL COLLATE "C",
    explorer_id              text        NOT NULL COLLATE "C",
    draft_version            bigint      NOT NULL,
    id                       text        NOT NULL COLLATE "C",
    title                    text        NOT NULL,
    draft_digest             text        NOT NULL DEFAULT '',
    draft_config             jsonb,
    snapshot_token           text        NOT NULL DEFAULT '',
    source_generation        text        NOT NULL DEFAULT '',
    authorization_scope_digest text      NOT NULL DEFAULT '',
    updated_by               text        NOT NULL DEFAULT '',
    updated_at               timestamptz NOT NULL,
    CONSTRAINT explorers_draft_revisions_identity_pk
        PRIMARY KEY (project, explorer_id, draft_version),
    CONSTRAINT explorers_draft_revisions_id_ux
        UNIQUE (project, explorer_id, id),
    CONSTRAINT explorers_draft_revisions_version_non_negative
        CHECK (draft_version >= 0),
    CONSTRAINT explorers_draft_revisions_id_shape
        CHECK (id ~ '^draft_revision_[0-9a-f]{64}$')
);
