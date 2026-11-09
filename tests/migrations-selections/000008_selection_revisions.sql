-- pg-state-selection-revision-owner-v1: staged selection revision
-- identity, project-scoped idempotency key, and expiring writer owner.
--
-- Two tables form the relational counterpart of the Arango adapter's
-- SelectionsCollection and SelectionMembersCollection
-- (internal/explorer/arango/selections.go). The version continues the
-- Explorer control-plane fixture history after the 000004 capability
-- reservation (pg-state-immutable-capability-receipts), and the DDL is
-- idempotent because the pg-store-02 runner may retry a migration that
-- committed before its ledger row was written. It never drops or truncates
-- existing state.
--
-- Selection revision identity:
--   * id is the global selection ID -- Arango keys these documents by
--     _key == id, so the primary key is id alone, and reads that carry a
--     project (GetSelection) stay scoped by it: a foreign project asking
--     for an id that exists elsewhere gets the concealed missing answer.
--   * (project, generation, resource_type, rule_digest, scope_digest) plus
--     the idempotency key are the identity the adapter rereads after any
--     arbiter skip and compares field by field; a key never rebinds
--     different intent. Identity columns carry COLLATE "C" for exact byte
--     order, matching the member-key walk and Arango's byte-order SORT.
--   * The idempotency key is unique per project via a partial unique
--     index: an empty key is omitted (like the model's omitempty and
--     Arango's missing attribute) and never collides; any non-empty key is
--     exactly one revision per project. That index is the single-winner
--     arbiter making concurrent same-key/same-content begins converge.
--   * state and complete agree by CHECK, a COMPLETE row must carry its
--     frozen membership facts (digest, completed_at), counters are
--     non-negative, and identity fields are non-empty -- BeginSelection's
--     validation closes the same domain at the database.
--
-- Expiring writer owner:
--   * writer_token/writer_expires_at bind the staged revision to its
--     writer; the adapter claims a foreign row only with a guarded UPDATE
--     whose WHERE re-evaluates writer_expires_at < now in PostgreSQL, so
--     an unexpired writer is unclaimable by construction and expired
--     recovery follows the current lease boundary.
--
-- Member insert-once:
--   * member_key is the derived immutable key (selection id + "_" + the
--     shared key() hash over project/generation/resource type/id), unique
--     per selection as the primary key; the (selection_id,
--     project, generation, resource_type, id) unique keeps reference
--     identity distinct even against an adapter derivation bug. Retries
--     are idempotent through ON CONFLICT DO NOTHING on the primary key.
--   * The key-shape CHECK keeps every member key in the derived
--     "_<64 lowercase hex>" suffix form.
--
-- Immutability is enforced here, not only in Go: a shared guard per table
-- plus BEFORE UPDATE triggers refuse identity tampering, completed-fact
-- tampering (completion is one-way), and ANY update of a staged member
-- with SQLSTATE 45501 before it can run. Member DELETE remains legal
-- because abort and bounded staging cleanup remove abandoned rows as
-- whole rows -- member content is never rewritten, only retired. The
-- suite proves the guards are not vacuous by dropping each trigger,
-- repeating the violating statement (which must then succeed), and
-- restoring the trigger.

CREATE TABLE IF NOT EXISTS selection_revisions (
    id text NOT NULL COLLATE "C",
    project text NOT NULL COLLATE "C",
    generation text NOT NULL COLLATE "C",
    resource_type text NOT NULL COLLATE "C",
    rule_digest text NOT NULL COLLATE "C",
    scope_digest text NOT NULL COLLATE "C",
    idempotency_key text NOT NULL COLLATE "C",
    state text NOT NULL,
    writer_token text NOT NULL,
    writer_expires_at timestamptz NOT NULL,
    membership_digest text NOT NULL DEFAULT '',
    member_count bigint NOT NULL DEFAULT 0,
    member_bytes bigint NOT NULL DEFAULT 0,
    complete boolean NOT NULL,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    rule jsonb NOT NULL
        CONSTRAINT selection_revisions_rule_object CHECK (jsonb_typeof(rule) = 'object'),
    source jsonb NOT NULL
        CONSTRAINT selection_revisions_source_object CHECK (jsonb_typeof(source) = 'object'),
    exclusions jsonb
        CONSTRAINT selection_revisions_exclusions_array CHECK (exclusions IS NULL OR jsonb_typeof(exclusions) = 'array'),
    CONSTRAINT selection_revisions_id_pk PRIMARY KEY (id),
    CONSTRAINT selection_revisions_identity_non_empty
        CHECK (id <> '' AND project <> '' AND generation <> '' AND resource_type <> ''),
    CONSTRAINT selection_revisions_digests_present
        CHECK (rule_digest <> '' AND scope_digest <> ''),
    CONSTRAINT selection_revisions_writer_present
        CHECK (writer_token <> ''),
    CONSTRAINT selection_revisions_state_known
        CHECK (state IN ('STAGING', 'COMPLETE')),
    CONSTRAINT selection_revisions_complete_matches_state
        CHECK ((state = 'COMPLETE') = complete),
    CONSTRAINT selection_revisions_complete_facts
        CHECK (complete = FALSE OR (membership_digest <> '' AND completed_at IS NOT NULL)),
    CONSTRAINT selection_revisions_membership_non_negative
        CHECK (member_count >= 0 AND member_bytes >= 0)
);

-- The project-scoped idempotency arbiter. Partial so an omitted (empty)
-- key never participates: like Arango's filter on a missing attribute,
-- keyless revisions coexist, and any non-empty key is exactly one
-- revision per project.
CREATE UNIQUE INDEX IF NOT EXISTS selection_revisions_project_key_ux
    ON selection_revisions (project, idempotency_key)
    WHERE idempotency_key <> '';

-- Retrieval mirror for the bounded staging cleanup scan (expired STAGING
-- headers by age, then lease).
CREATE INDEX IF NOT EXISTS selection_revisions_staging_ix
    ON selection_revisions (state, created_at, writer_expires_at);

CREATE TABLE IF NOT EXISTS selection_members (
    selection_id text NOT NULL COLLATE "C",
    member_key text NOT NULL COLLATE "C",
    project text NOT NULL COLLATE "C",
    generation text NOT NULL COLLATE "C",
    resource_type text NOT NULL COLLATE "C",
    id text NOT NULL COLLATE "C",
    ordinal bigint NOT NULL DEFAULT 0,
    CONSTRAINT selection_members_key_pk
        PRIMARY KEY (selection_id, member_key),
    CONSTRAINT selection_members_ref_ux
        UNIQUE (selection_id, project, generation, resource_type, id),
    CONSTRAINT selection_members_identity_non_empty
        CHECK (selection_id <> '' AND project <> '' AND generation <> ''
               AND resource_type <> '' AND id <> ''),
    CONSTRAINT selection_members_key_shape
        CHECK (member_key ~ '_[0-9a-f]{64}$')
);

-- The selection-revision guard: identity columns are insert-once, and a
-- COMPLETE row's state/facts are frozen (completion is one-way). The
-- writer lease and pre-completion staging facts stay live precisely
-- because they are outside the frozen set.
CREATE OR REPLACE FUNCTION loom_selection_revision_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.project IS DISTINCT FROM OLD.project
        OR NEW.generation IS DISTINCT FROM OLD.generation
        OR NEW.resource_type IS DISTINCT FROM OLD.resource_type
        OR NEW.rule_digest IS DISTINCT FROM OLD.rule_digest
        OR NEW.scope_digest IS DISTINCT FROM OLD.scope_digest
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.rule IS DISTINCT FROM OLD.rule
        OR NEW.source IS DISTINCT FROM OLD.source
        OR NEW.exclusions IS DISTINCT FROM OLD.exclusions
    THEN
        RAISE EXCEPTION 'loom: selection revision identity columns are immutable (insert-once)'
            USING ERRCODE = '45501';
    END IF;
    IF OLD.complete AND (
        NEW.state IS DISTINCT FROM OLD.state
        OR NEW.complete IS DISTINCT FROM OLD.complete
        OR NEW.membership_digest IS DISTINCT FROM OLD.membership_digest
        OR NEW.member_count IS DISTINCT FROM OLD.member_count
        OR NEW.member_bytes IS DISTINCT FROM OLD.member_bytes
        OR NEW.completed_at IS DISTINCT FROM OLD.completed_at
    ) THEN
        RAISE EXCEPTION 'loom: completed selection facts are immutable (completion is one-way)'
            USING ERRCODE = '45501';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS selection_revisions_immutable ON selection_revisions;
CREATE TRIGGER selection_revisions_immutable
    BEFORE UPDATE ON selection_revisions
    FOR EACH ROW EXECUTE FUNCTION loom_selection_revision_immutable();

-- The member guard: staged membership content is insert-once. Any UPDATE
-- aborts with 45501 before touching a page; removal is lifecycle-only
-- (abort and bounded staging cleanup delete whole rows, they never
-- rewrite one).
CREATE OR REPLACE FUNCTION loom_selection_member_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'loom: staged selection member rows are immutable (insert-once; removal is lifecycle-only)'
        USING ERRCODE = '45501';
END;
$$;

DROP TRIGGER IF EXISTS selection_members_immutable ON selection_members;
CREATE TRIGGER selection_members_immutable
    BEFORE UPDATE ON selection_members
    FOR EACH ROW EXECUTE FUNCTION loom_selection_member_immutable();
