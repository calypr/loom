-- pg-state-recipe-registries-v1: immutable name/version recipe registrations
-- and the fenced execution-occurrence usage markers for the PostgreSQL
-- recipe-execution control plane.
--
-- The registration table mirrors the Arango collection loom_dataframe_recipes
-- (internal/dataframe/recipe/exec/arango/registry.go): one row per
-- (name, translationVersion), the canonical Bundle plus digest in a jsonb
-- payload, and identity columns split out so lookup is a scope equality --
-- never a name-only "latest" guess beyond the contract's LoadRecipe current
-- read. Identity columns carry COLLATE "C" so uniqueness and LoadRecipe's
-- translationVersion DESC "current" order use exact byte order -- the same
-- deterministic order Arango's byte-order SORT gives -- independent of the
-- database's default collation.
--
-- Usage marking mirrors the Arango usage probe against
-- loom_dataframe_bundle_executions, but as a first-class occurrence table in
-- this history: the PostgreSQL control plane forbids cross-database
-- atomicity, so an execution occurrence and the version it consumes are
-- fenced through one row lock on the recipe row (see the package
-- documentation). Each durable use is one occurrence row keyed by its own
-- occurrence identity -- retries of one occurrence idempotently reconverge
-- on the same row -- and repeated occurrences by one owner correlate through
-- owner_sequence, assigned under the recipe-row lock so witnesses can order
-- them.
--
-- Column policy follows the sibling control-plane tickets:
--   * Go strings have no missing/empty distinction, so scope columns are
--     NOT NULL with non-empty CHECKs; empty identity never round-trips.
--   * The payload is authoritative for the exec.Entry shape (Bundle +
--     Digest) and jsonb preserves JSON null versus absent members; the
--     digest column carries the CHECK that it is a lowercase-hex SHA-256 so
--     a garbage digest cannot key a version.
--   * This migration never drops or truncates: rerunning bootstrap on an
--     existing schema is a no-op that preserves registered state, as the
--     runner's commit-before-record retry requires.
CREATE TABLE IF NOT EXISTS recipe_exec_recipes (
    recipe_name         text        NOT NULL COLLATE "C",
    translation_version text        NOT NULL COLLATE "C",
    digest              text        NOT NULL,
    payload             jsonb       NOT NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    CONSTRAINT recipe_exec_recipes_pkey PRIMARY KEY (recipe_name, translation_version),
    CONSTRAINT recipe_exec_recipes_name_nonempty CHECK (recipe_name <> ''),
    CONSTRAINT recipe_exec_recipes_version_nonempty CHECK (translation_version <> ''),
    CONSTRAINT recipe_exec_recipes_digest_hex CHECK (digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT recipe_exec_recipes_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

-- LoadRecipe "current": highest translationVersion in byte order first. The
-- primary key already orders (recipe_name, translation_version); this index
-- exists so the LIMIT 1 current read descends the version spine directly.
CREATE INDEX IF NOT EXISTS recipe_exec_recipes_current
    ON recipe_exec_recipes (recipe_name, translation_version DESC);

CREATE TABLE IF NOT EXISTS recipe_exec_occurrences (
    recipe_name         text        NOT NULL COLLATE "C",
    translation_version text        NOT NULL COLLATE "C",
    occurrence_id       text        NOT NULL COLLATE "C",
    owner_id            text        NOT NULL DEFAULT '',
    -- Per-(version, owner) ordinal assigned under the recipe-row lock:
    -- repeated occurrences from one owner on one version correlate through
    -- it, and it is strictly increasing in recording order.
    owner_sequence      bigint      NOT NULL,
    recorded_at         timestamptz NOT NULL,
    CONSTRAINT recipe_exec_occurrences_pkey
        PRIMARY KEY (recipe_name, translation_version, occurrence_id),
    CONSTRAINT recipe_exec_occurrences_name_nonempty CHECK (recipe_name <> ''),
    CONSTRAINT recipe_exec_occurrences_version_nonempty CHECK (translation_version <> ''),
    CONSTRAINT recipe_exec_occurrences_occurrence_nonempty CHECK (occurrence_id <> ''),
    CONSTRAINT recipe_exec_occurrences_owner_sequence_positive
        CHECK (owner_sequence >= 1)
);

-- Repeated-owner correlation reads: MAX(owner_sequence) per (version, owner).
CREATE INDEX IF NOT EXISTS recipe_exec_occurrences_owner_sequence
    ON recipe_exec_occurrences (recipe_name, translation_version, owner_id, owner_sequence DESC);
