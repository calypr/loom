-- pg-state-recipe-registries-v1: project recipe revision chains for the
-- PostgreSQL control plane.
--
-- The revision table mirrors the Arango collection
-- loom_dataframe_recipe_revisions (internal/dataframe/recipe/exec/arango/
-- revisions.go): one immutable row per (project, name, digest) carrying the
-- canonical Bundle payload and its parent digest, with the digest being the
-- canonical Bundle digest and therefore a safe optimistic-concurrency and
-- publication address. The heads table makes the chain's CAS explicit: one
-- row per (project, name) names the current head digest and counts every
-- committed extension, correlating repeated extensions of one chain through
-- epoch the way the publication leases correlate repeated owners.
--
-- Deterministic scope and ordering:
--   * project/name/digest carry COLLATE "C" so tenant/project scoping is
--     exact byte equality and List's (created_at DESC, digest) order is
--     stable under any database collation.
--   * The payload is authoritative for the RecipeRevision shape; the
--     searchable identity lives in columns and cannot disagree with what the
--     adapter wrote (one argument slice populates both).
-- Three-state discipline:
--   * parent_digest is NOT NULL DEFAULT '': the Go string has no missing
--     state, so "no parent" is the empty string (the payload omits the
--     key), never SQL NULL. A parent is either empty or a valid digest --
--     the CHECK rejects every other shape, so a malformed parent can never
--     masquerade as a chain link.
--   * head_digest is the only nullable column and it is a placeholder
--     state, not data: epoch = 0 with head_digest IS NULL means "the chain
--     row exists to serialize writers but no revision has ever committed";
--     epoch >= 1 always names a real digest. The pair CHECK makes the two
--     illegal combinations (NULL with epoch >= 1, digest with epoch 0)
--     unrepresentable, and no placeholder row can ever be read back as a
--     revision.
-- This migration never drops or truncates: rerunning bootstrap on an
-- existing schema is a no-op that preserves chain state.
CREATE TABLE IF NOT EXISTS recipe_revisions (
    project       text        NOT NULL COLLATE "C",
    name          text        NOT NULL COLLATE "C",
    digest        text        NOT NULL COLLATE "C",
    parent_digest text        NOT NULL DEFAULT '',
    payload       jsonb       NOT NULL,
    created_at    timestamptz NOT NULL,
    CONSTRAINT recipe_revisions_pkey PRIMARY KEY (project, name, digest),
    CONSTRAINT recipe_revisions_project_nonempty CHECK (project <> ''),
    CONSTRAINT recipe_revisions_name_nonempty CHECK (name <> ''),
    CONSTRAINT recipe_revisions_digest_hex CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT recipe_revisions_parent_shape
        CHECK (parent_digest = '' OR parent_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT recipe_revisions_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

-- List(project, name): every revision of one chain, deterministic newest
-- first with digest as the total-order tie-break.
CREATE INDEX IF NOT EXISTS recipe_revisions_chain_created
    ON recipe_revisions (project, name, created_at DESC, digest);

CREATE TABLE IF NOT EXISTS recipe_revision_heads (
    project     text        NOT NULL COLLATE "C",
    name        text        NOT NULL COLLATE "C",
    head_digest text,
    epoch       bigint      NOT NULL,
    updated_at  timestamptz NOT NULL,
    CONSTRAINT recipe_revision_heads_pkey PRIMARY KEY (project, name),
    CONSTRAINT recipe_revision_heads_project_nonempty CHECK (project <> ''),
    CONSTRAINT recipe_revision_heads_name_nonempty CHECK (name <> ''),
    CONSTRAINT recipe_revision_heads_epoch_nonnegative CHECK (epoch >= 0),
    CONSTRAINT recipe_revision_heads_state_consistent CHECK (
        (epoch = 0 AND head_digest IS NULL)
        OR (epoch >= 1 AND head_digest IS NOT NULL AND head_digest ~ '^[0-9a-f]{64}$')
    )
);
