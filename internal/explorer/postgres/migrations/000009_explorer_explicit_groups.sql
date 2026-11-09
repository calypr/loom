-- pg-state-explicit-group-definitions-v1: explicit-group revision headers
-- and ordered definition rows.
--
-- Explicit groups use three separate records (internal/explorer/
-- explicit_group_repository.go): the revision header, the ordered
-- definition rows, and (a later ticket) membership rows. This migration
-- creates the first two. A definition row is a group's durable identity and
-- order independent of any membership, so a group with no members still
-- exists as a real, ordered group -- "empty" here is a declared group with
-- zero membership, never a missing row. Order is a stored column
-- (ordinal), never decode order or a JSON array: parity_risks forbid
-- encoding definitions only as JSONB.
--
-- Staging lifecycle is database-enforced, not advisory, via two BEFORE
-- triggers plus closed CHECK domains:
--
--   * The completion gate: nothing in this slice may mark a header
--     COMPLETE -- completion belongs to the whole later-defined revision
--     contract and requires the membership slice's transactionally
--     verified final digest/count operation. So explorer_explicit_group_
--     revisions_state_staging CHECKs state = 'STAGING' outright: neither
--     the store nor direct SQL can complete a header, and the membership
--     ticket's migration replaces this constraint together with the
--     guarded STAGING->COMPLETE lifecycle machine it will introduce.
--     Staging rows additionally carry no completion values
--     (explorer_explicit_group_revisions_staging_values), matching the
--     domain Validate rule "staging explicit group revision cannot
--     contain completion values".
--   * The idempotency identity stays honestly derived: the revision ID is
--     the deterministic hash of (project, idempotency key) computed by
--     explorer.ExplicitGroupRevisionIDFor, and
--     explorer_explicit_group_revisions_identity_binding recomputes the
--     exact length-prefixed SHA-256 frame in SQL, so a row whose id does
--     not match its own (project, idempotency_key) pair is refused at
--     insert -- the reservation identity cannot be forged.
--   * Header UPDATEs are a renewal-only channel: the BEFORE UPDATE
--     trigger loom_explorer_explicit_group_revision_guard refuses any
--     change to identity, binding, or completion columns, refuses any
--     change to the writer token itself, and admits writer_lease changes
--     only when the session stamped its local
--     loom.explorer_explicit_group_writer_token GUC with the row's own
--     writer token AND the lease moves strictly forward (never revoked,
--     never shortened -- a shrunken lease would let cleanup kill an
--     active writer). A true no-op UPDATE stays legal.
--   * Header DELETEs are authorization-gated cleanup/abort only: the same
--     trigger refuses a DELETE unless the session either (a) stamped
--     loom.explorer_explicit_group_abort_token with the row's own writer
--     token (owner abort), or (b) stamped
--     loom.explorer_explicit_group_cleanup_now with the sweep clock and
--     the row is expired at that clock (writer_expires_at IS NULL or
--     before it -- the Arango adapter's missing-field tolerance).
--     Active writers are therefore never removable by un-authorized SQL;
--     the store passes only its own real clock, so cleanup converges on
--     "expired at the sweep's clock".
--   * Definition rows are write-once: loom_explorer_explicit_group_
--     definition_guard refuses every UPDATE, and refuses DELETE unless
--     the parent header authorizes the same operation (its own STAGING
--     state plus a matching abort-token GUC or an expired-at-cleanup-now
--     lease), so children of a completed or foreign-owned revision can
--     never be removed. The FOREIGN KEY ... ON DELETE RESTRICT additionally
--     keeps a header removable only after its definition rows are gone --
--     the store's abort order, not a caller preference.
--   * Definition shape is a closed domain: group identity non-blank,
--     label non-blank, ordinal non-negative, one row per
--     (revision, group) -- PRIMARY KEY -- and one row per
--     (revision, ordinal) -- UNIQUE -- exactly the invariants the domain
--     CanonicalExplicitGroupDefinitions checks in Go, now also DB facts.
--
-- Column policy: identity columns carry COLLATE "C" for exact byte-order
-- uniqueness and index order (the Arango adapter's byte-order listing and
-- _key tiebreaks). completed_at and the writer columns are nullable
-- precisely where the domain/adapter tolerates absence (staging never
-- carries completed_at; cleanup tolerates a missing writerExpiresAt).
-- writer_token/writer_expires_at are storage-only lifecycle columns the
-- domain struct never carries, matching the adapter's header document.
--
-- This migration never drops or truncates state: rerunning it on an
-- existing schema (crash in the commit-before-record window) is a no-op
-- convergence; CREATE OR REPLACE FUNCTION plus DROP TRIGGER IF EXISTS +
-- CREATE TRIGGER re-establish the identical guards, never weakened ones.

CREATE TABLE IF NOT EXISTS explorer_explicit_group_revisions (
    id                           text        NOT NULL COLLATE "C",
    project                      text        NOT NULL COLLATE "C",
    generation                   text        NOT NULL COLLATE "C",
    scope_digest                 text        NOT NULL COLLATE "C",
    resource_type                text        NOT NULL COLLATE "C",
    source_selection_revision_id text        NOT NULL COLLATE "C",
    source_membership_digest     text        NOT NULL COLLATE "C",
    definition_digest            text        NOT NULL DEFAULT '',
    membership_digest            text        NOT NULL DEFAULT '',
    group_count                  bigint      NOT NULL DEFAULT 0,
    member_count                 bigint      NOT NULL DEFAULT 0,
    state                        text        NOT NULL,
    idempotency_key              text        NOT NULL COLLATE "C",
    created_at                   timestamptz NOT NULL,
    completed_at                 timestamptz,
    writer_token                 text        COLLATE "C",
    writer_expires_at            timestamptz,
    CONSTRAINT explorer_explicit_group_revisions_pkey
        PRIMARY KEY (id),
    -- Completion gate for this slice (replaced by the membership
    -- ticket's guarded lifecycle machine): no header may exist in any
    -- state other than STAGING, no matter who writes the statement.
    CONSTRAINT explorer_explicit_group_revisions_state_staging
        CHECK (state = 'STAGING'),
    -- Staging headers carry no completion values, per the domain rule.
    CONSTRAINT explorer_explicit_group_revisions_staging_values
        CHECK (definition_digest = '' AND membership_digest = ''
            AND group_count = 0 AND member_count = 0
            AND completed_at IS NULL),
    CONSTRAINT explorer_explicit_group_revisions_required_fields
        CHECK (project <> '' AND generation <> '' AND scope_digest <> ''
            AND resource_type <> '' AND source_selection_revision_id <> ''
            AND source_membership_digest <> '' AND idempotency_key <> ''),
    -- The idempotency identity is honestly derived: id equals the
    -- length-prefixed SHA-256 over the domain separator, project, and
    -- idempotency key -- the exact frame explorer.ExplicitGroupRevisionIDFor
    -- digests (8-byte big-endian length per field).
    CONSTRAINT explorer_explicit_group_revisions_identity_binding
        CHECK (id = 'grouprev_'::text || encode(
            sha256(
                decode(lpad(to_hex(octet_length('loom-explicit-group-revision-id-v1')), 16, '0'), 'hex')
                    || 'loom-explicit-group-revision-id-v1'::bytea
                || decode(lpad(to_hex(octet_length(project)), 16, '0'), 'hex')
                    || project::bytea
                || decode(lpad(to_hex(octet_length(idempotency_key)), 16, '0'), 'hex')
                    || idempotency_key::bytea
            ), 'hex'))
);

CREATE INDEX IF NOT EXISTS explorer_explicit_group_revisions_listing
    ON explorer_explicit_group_revisions
    (project, generation, resource_type, scope_digest, created_at DESC, id);

CREATE INDEX IF NOT EXISTS explorer_explicit_group_revisions_staging
    ON explorer_explicit_group_revisions (created_at, id)
    WHERE state = 'STAGING';

CREATE TABLE IF NOT EXISTS explorer_explicit_group_definitions (
    revision_id text   NOT NULL COLLATE "C",
    group_id    text   NOT NULL COLLATE "C",
    label       text   NOT NULL,
    ordinal     bigint NOT NULL,
    CONSTRAINT explorer_explicit_group_definitions_pkey
        PRIMARY KEY (revision_id, group_id),
    -- Ordinal is the group list's user-visible order: unique per
    -- revision, non-negative -- the adapter's canonicalization invariants
    -- as relational constraints.
    CONSTRAINT explorer_explicit_group_definitions_revision_ordinal_key
        UNIQUE (revision_id, ordinal),
    CONSTRAINT explorer_explicit_group_definitions_group_id_present
        CHECK (group_id <> ''),
    CONSTRAINT explorer_explicit_group_definitions_label_present
        CHECK (label <> ''),
    CONSTRAINT explorer_explicit_group_definitions_ordinal_non_negative
        CHECK (ordinal >= 0),
    CONSTRAINT explorer_explicit_group_definitions_revision_fkey
        FOREIGN KEY (revision_id)
        REFERENCES explorer_explicit_group_revisions (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
);

CREATE OR REPLACE FUNCTION loom_explorer_explicit_group_revision_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    writer_token text := current_setting('loom.explorer_explicit_group_writer_token', true);
    abort_token  text := current_setting('loom.explorer_explicit_group_abort_token', true);
    cleanup_now  text := current_setting('loom.explorer_explicit_group_cleanup_now', true);
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- Only ever staging headers: a header in any other state is
        -- permanent history (the state domain currently keeps this at
        -- STAGING; the guard stands ready for the membership ticket's
        -- lifecycle machine).
        IF OLD.state <> 'STAGING' THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: DELETE of revision % in state % is refused: only STAGING headers are ever removed',
                OLD.id, OLD.state
                USING ERRCODE = '23514';
        END IF;
        IF abort_token IS NOT NULL AND OLD.writer_token IS NOT NULL
            AND abort_token = OLD.writer_token THEN
            RETURN OLD; -- owner abort: the token holder ends its own staging revision
        END IF;
        IF cleanup_now IS NOT NULL
            AND (OLD.writer_expires_at IS NULL
                 OR OLD.writer_expires_at < cleanup_now::timestamptz) THEN
            RETURN OLD; -- expired-writer cleanup under the sweep's own clock
        END IF;
        RAISE EXCEPTION
            'explorer_explicit_group_revisions guard: DELETE of revision % is refused without owner-abort or expired-writer-cleanup authorization',
            OLD.id
            USING ERRCODE = '23514';
    END IF;

    -- UPDATE below. A true no-op changes nothing and stays legal.
    IF ROW(NEW.*) IS NOT DISTINCT FROM ROW(OLD.*) THEN
        RETURN NEW;
    END IF;

    IF NEW.writer_token IS DISTINCT FROM OLD.writer_token THEN
        RAISE EXCEPTION
            'explorer_explicit_group_revisions guard: writer token of revision % changed', OLD.id
            USING ERRCODE = '23514';
    END IF;

    IF NEW.writer_expires_at IS DISTINCT FROM OLD.writer_expires_at THEN
        -- Lease renewal is the only admitted UPDATE channel: the session
        -- must hold this row's writer token, and the lease only moves
        -- forward -- it is never revoked and never shortened, so no
        -- update can manufacture an "expired" row for cleanup to kill.
        IF NEW.writer_expires_at IS NULL THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: lease of revision % may never be revoked', OLD.id
                USING ERRCODE = '23514';
        END IF;
        IF writer_token IS NULL OR OLD.writer_token IS NULL
            OR writer_token <> OLD.writer_token THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: lease renewal of revision % refused without the row''s writer token', OLD.id
                USING ERRCODE = '23514';
        END IF;
        IF OLD.writer_expires_at IS NOT NULL
            AND NEW.writer_expires_at <= OLD.writer_expires_at THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: lease of revision % may never be shortened', OLD.id
                USING ERRCODE = '23514';
        END IF;
    END IF;

    -- Identity, binding, and completion columns are frozen. The state
    -- column is deliberately absent here: the named CHECK constraint
    -- owns the completion gate so its refusal is classified by name.
    IF NEW.id                          IS DISTINCT FROM OLD.id
        OR NEW.project                 IS DISTINCT FROM OLD.project
        OR NEW.generation              IS DISTINCT FROM OLD.generation
        OR NEW.scope_digest            IS DISTINCT FROM OLD.scope_digest
        OR NEW.resource_type           IS DISTINCT FROM OLD.resource_type
        OR NEW.source_selection_revision_id IS DISTINCT FROM OLD.source_selection_revision_id
        OR NEW.source_membership_digest     IS DISTINCT FROM OLD.source_membership_digest
        OR NEW.definition_digest       IS DISTINCT FROM OLD.definition_digest
        OR NEW.membership_digest       IS DISTINCT FROM OLD.membership_digest
        OR NEW.group_count             IS DISTINCT FROM OLD.group_count
        OR NEW.member_count            IS DISTINCT FROM OLD.member_count
        OR NEW.idempotency_key         IS DISTINCT FROM OLD.idempotency_key
        OR NEW.created_at              IS DISTINCT FROM OLD.created_at
        OR NEW.completed_at            IS DISTINCT FROM OLD.completed_at
    THEN
        RAISE EXCEPTION
            'explorer_explicit_group_revisions guard: frozen content of revision % changed', OLD.id
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS explorer_explicit_group_revisions_guard ON explorer_explicit_group_revisions;
CREATE TRIGGER explorer_explicit_group_revisions_guard
    BEFORE UPDATE OR DELETE ON explorer_explicit_group_revisions
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_explicit_group_revision_guard();

CREATE OR REPLACE FUNCTION loom_explorer_explicit_group_definition_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    parent_state        text;
    parent_writer_token text;
    parent_expires_at   timestamptz;
    abort_token         text := current_setting('loom.explorer_explicit_group_abort_token', true);
    cleanup_now         text := current_setting('loom.explorer_explicit_group_cleanup_now', true);
    diag_rows          integer;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.*) IS NOT DISTINCT FROM ROW(OLD.*) THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION
            'explorer_explicit_group_definitions guard: definition % of revision % is write-once',
            OLD.group_id, OLD.revision_id
            USING ERRCODE = '23514';
    END IF;

    -- DELETE below: only ever as part of an authorized teardown of the
    -- parent staging revision. The parent must exist, be STAGING, and
    -- carry the same authorization the header guard demands. The parent
    -- lookup resolves through TG_TABLE_SCHEMA so the guard behaves
    -- identically no matter what search_path the calling session carries
    -- (the trigger fires during ordinary store traffic, not just under
    -- the migration runner's SET LOCAL search_path).
    -- FOUND is unreliable after EXECUTE ... INTO here, so the row count
    -- drives the absent-parent branch instead.
    EXECUTE format(
        'SELECT state, writer_token, writer_expires_at FROM %I.explorer_explicit_group_revisions WHERE id = $1',
        TG_TABLE_SCHEMA
    ) INTO parent_state, parent_writer_token, parent_expires_at USING OLD.revision_id;
    GET DIAGNOSTICS diag_rows = ROW_COUNT;
    IF diag_rows = 0 THEN
        RAISE EXCEPTION
            'explorer_explicit_group_definitions guard: DELETE of definition % refused: parent revision % is absent',
            OLD.group_id, OLD.revision_id
            USING ERRCODE = '23514';
    END IF;
    IF parent_state <> 'STAGING' THEN
        RAISE EXCEPTION
            'explorer_explicit_group_definitions guard: DELETE of definition % refused: parent revision % is %',
            OLD.group_id, OLD.revision_id, parent_state
            USING ERRCODE = '23514';
    END IF;
    IF abort_token IS NOT NULL AND parent_writer_token IS NOT NULL
        AND abort_token = parent_writer_token THEN
        RETURN OLD; -- owner abort cascade
    END IF;
    IF cleanup_now IS NOT NULL
        AND (parent_expires_at IS NULL OR parent_expires_at < cleanup_now::timestamptz) THEN
        RETURN OLD; -- expired-writer cleanup cascade under the sweep's clock
    END IF;
    RAISE EXCEPTION
        'explorer_explicit_group_definitions guard: DELETE of definition % of revision % refused without owner-abort or expired-writer-cleanup authorization',
        OLD.group_id, OLD.revision_id
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS explorer_explicit_group_definitions_guard ON explorer_explicit_group_definitions;
CREATE TRIGGER explorer_explicit_group_definitions_guard
    BEFORE UPDATE OR DELETE ON explorer_explicit_group_definitions
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_explicit_group_definition_guard();
