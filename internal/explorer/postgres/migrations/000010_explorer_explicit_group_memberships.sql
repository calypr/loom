-- pg-state-explicit-group-members-v1: explicit-group membership rows and
-- the guarded STAGING->COMPLETE lifecycle machine the definitions migration
-- explicitly deferred to this ticket.
--
-- 000009 created the revision header and the ordered definition rows and
-- pinned the header to STAGING outright, promising that "the membership
-- ticket's migration replaces that constraint together with the guarded
-- lifecycle machine it will introduce". This migration is that promise,
-- kept in full:
--
--   (1) explorer_explicit_group_memberships: one row per
--       (revision, group, reference). The primary key CARRIES THE GROUP ID
--       (revision_id, group_id, project, generation, resource_type,
--       resource_id), so one resource occurring under several GroupIDs is
--       several rows -- the overlap the domain contract requires -- while a
--       duplicate retry of the same (revision, group, reference) collides on
--       the primary key and creates nothing. Every identity column is
--       COLLATE "C" for exact byte-order uniqueness and index order
--       (the Arango adapter's byte-order membership walk).
--   (2) Membership rows are children twice over: a foreign key to the
--       header (ON DELETE RESTRICT -- a header is removable only after its
--       membership rows are gone, the store's abort order, not a caller
--       preference) and a compound foreign key to
--       (revision_id, group_id) in explorer_explicit_group_definitions --
--       so "membership references an undeclared group" is a database fact
--       (23503), not a Go convention.
--   (3) The membership guard trigger makes membership rows
--       insert-once/whole-row-retired: INSERT is admitted only under a
--       STAGING parent whose project, generation, and resource type the
--       reference repeats exactly (a completed or foreign-scoped revision
--       can never gain members, even by direct SQL); every real UPDATE is
--       refused; DELETE is admitted only as part of an authorized teardown
--       of the parent staging revision (owner-abort or expired-writer
--       cleanup, the same two GUC channels 000009 established).
--   (4) The completion machine replaces 000009's completion gate:
--       DROPping explorer_explicit_group_revisions_state_staging and
--       explorer_explicit_group_revisions_staging_values, then ADDing
--       explorer_explicit_group_revisions_lifecycle_values -- one named
--       constraint that closes BOTH the state domain and the value shape:
--       STAGING carries no completion values, COMPLETE carries non-empty
--       digests, group_count > 0, member_count >= 0, and completed_at, and
--       no other state is representable at all. Headers are still born
--       STAGING only: the header guard's new INSERT branch refuses any
--       other state at the door, and the ONLY path to COMPLETE is the
--       guarded UPDATE below.
--   (5) The header guard's UPDATE channel admits exactly three things:
--       a true no-op, the lease renewal 000009 established (writer-token
--       GUC, strictly forward), and the completion transition -- which is
--       admitted only from STAGING, only for the session holding the row's
--       own writer token, only with the writer columns untouched, only
--       with the identity/binding columns frozen, and only when the
--       claimed group_count and member_count EQUAL the definition and
--       membership rows actually stored for the revision at that moment
--       (counted inside the trigger, so "completion transition verifies
--       the definitions and membership set" is a database fact; the
--       digests themselves are content-addressed hashes the store
--       re-derives inside the same lock-held transaction, the exact
--       counterpart of the Arango adapter's digest-and-CAS pair). COMPLETE
--       rows are frozen history: no non-noop UPDATE ever touches them,
--       and the DELETE branch keeps refusing anything but the STAGING
--       teardown channels, so a completed revision is permanent.
--
-- The migration reruns converge: CREATE TABLE IF NOT EXISTS /
-- CREATE INDEX IF NOT EXISTS, DROP CONSTRAINT IF EXISTS before every ADD,
-- CREATE OR REPLACE FUNCTION, and DROP TRIGGER IF EXISTS + CREATE TRIGGER
-- re-establish the identical guards (crash in the commit-before-record
-- window repeats harmlessly and never weakens a guard). It never drops or
-- truncates state.

CREATE TABLE IF NOT EXISTS explorer_explicit_group_memberships (
    revision_id   text NOT NULL COLLATE "C",
    group_id      text NOT NULL COLLATE "C",
    project       text NOT NULL COLLATE "C",
    generation    text NOT NULL COLLATE "C",
    resource_type text NOT NULL COLLATE "C",
    resource_id   text NOT NULL COLLATE "C",
    -- The unique key includes the group ID so legitimate overlaps survive:
    -- one resource may belong to several groups, and only the exact
    -- (revision, group, reference) tuple is unique (parity_risks).
    CONSTRAINT explorer_explicit_group_memberships_pkey
        PRIMARY KEY (revision_id, group_id, project, generation, resource_type, resource_id),
    CONSTRAINT explorer_explicit_group_memberships_revision_present
        CHECK (revision_id <> ''),
    CONSTRAINT explorer_explicit_group_memberships_group_id_present
        CHECK (group_id <> ''),
    CONSTRAINT explorer_explicit_group_memberships_project_present
        CHECK (project <> ''),
    CONSTRAINT explorer_explicit_group_memberships_generation_present
        CHECK (generation <> ''),
    CONSTRAINT explorer_explicit_group_memberships_resource_type_present
        CHECK (resource_type <> ''),
    CONSTRAINT explorer_explicit_group_memberships_resource_id_present
        CHECK (resource_id <> ''),
    CONSTRAINT explorer_explicit_group_memberships_revision_fkey
        FOREIGN KEY (revision_id)
        REFERENCES explorer_explicit_group_revisions (id)
        ON DELETE RESTRICT ON UPDATE RESTRICT,
    -- A membership may name only a group this very revision declared: the
    -- declared-domain rule of CanonicalExplicitGroupMemberships as a
    -- relational foreign key.
    CONSTRAINT explorer_explicit_group_memberships_group_fkey
        FOREIGN KEY (revision_id, group_id)
        REFERENCES explorer_explicit_group_definitions (revision_id, group_id)
        ON DELETE RESTRICT ON UPDATE RESTRICT
);

-- The bounded keyset walk (digest stream and visit) pages by
-- (group_id, resource_id) within one revision -- the composite
-- continuation cursor of the Arango adapter's membership visit.
CREATE INDEX IF NOT EXISTS explorer_explicit_group_memberships_visit
    ON explorer_explicit_group_memberships (revision_id, group_id, resource_id);

-- The completion gate, version 2. 000009's two staging-only CHECKs are
-- replaced by one named lifecycle constraint that closes the state domain
-- and both value shapes in a single classifiable object.
ALTER TABLE explorer_explicit_group_revisions
    DROP CONSTRAINT IF EXISTS explorer_explicit_group_revisions_state_staging;
ALTER TABLE explorer_explicit_group_revisions
    DROP CONSTRAINT IF EXISTS explorer_explicit_group_revisions_staging_values;
ALTER TABLE explorer_explicit_group_revisions
    DROP CONSTRAINT IF EXISTS explorer_explicit_group_revisions_lifecycle_values;
ALTER TABLE explorer_explicit_group_revisions
    ADD CONSTRAINT explorer_explicit_group_revisions_lifecycle_values
        CHECK (
            (state = 'STAGING'
                AND definition_digest = '' AND membership_digest = ''
                AND group_count = 0 AND member_count = 0
                AND completed_at IS NULL)
            OR
            (state = 'COMPLETE'
                AND definition_digest <> '' AND membership_digest <> ''
                AND group_count > 0 AND member_count >= 0
                AND completed_at IS NOT NULL)
        );

CREATE OR REPLACE FUNCTION loom_explorer_explicit_group_revision_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    writer_token    text := current_setting('loom.explorer_explicit_group_writer_token', true);
    abort_token     text := current_setting('loom.explorer_explicit_group_abort_token', true);
    cleanup_now     text := current_setting('loom.explorer_explicit_group_cleanup_now', true);
    definition_rows bigint;
    membership_rows bigint;
BEGIN
    IF TG_OP = 'INSERT' THEN
        -- Headers are born STAGING. A COMPLETE row may never be inserted
        -- directly: completion is a transition, not an initial state, and
        -- only the guarded UPDATE channel below may perform it. (Before
        -- this migration the same fact lived in the state_staging CHECK;
        -- the lifecycle_values constraint keeps the VALUE shapes closed,
        -- this branch keeps birth STAGING-only.)
        IF NEW.state <> 'STAGING' THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: INSERT of revision % in state % is refused: headers are born STAGING and only the guarded completion transition may complete one',
                NEW.id, NEW.state
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        -- Only ever staging headers: a COMPLETE header is permanent
        -- history, exactly as before this migration.
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

    IF OLD.state = 'COMPLETE' THEN
        RAISE EXCEPTION
            'explorer_explicit_group_revisions guard: revision % is COMPLETE history and may never be modified',
            OLD.id
            USING ERRCODE = '23514';
    END IF;

    IF NEW.writer_token IS DISTINCT FROM OLD.writer_token THEN
        RAISE EXCEPTION
            'explorer_explicit_group_revisions guard: writer token of revision % changed', OLD.id
            USING ERRCODE = '23514';
    END IF;

    IF NEW.state IS DISTINCT FROM OLD.state THEN
        -- The completion channel: STAGING -> COMPLETE, and nothing else is
        -- representable here (any other pairing lands on this refusal or
        -- on the lifecycle_values CHECK).
        IF OLD.state <> 'STAGING' OR NEW.state <> 'COMPLETE' THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: revision % state % -> % is refused: the only admitted transition is STAGING -> COMPLETE',
                OLD.id, OLD.state, NEW.state
                USING ERRCODE = '23514';
        END IF;
        IF writer_token IS NULL OR OLD.writer_token IS NULL
            OR writer_token <> OLD.writer_token THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: completion of revision % refused without the row''s writer token', OLD.id
                USING ERRCODE = '23514';
        END IF;
        IF NEW.writer_expires_at IS DISTINCT FROM OLD.writer_expires_at THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: completion of revision % may not move the writer lease', OLD.id
                USING ERRCODE = '23514';
        END IF;
        IF NEW.id                                 IS DISTINCT FROM OLD.id
            OR NEW.project                        IS DISTINCT FROM OLD.project
            OR NEW.generation                     IS DISTINCT FROM OLD.generation
            OR NEW.scope_digest                   IS DISTINCT FROM OLD.scope_digest
            OR NEW.resource_type                  IS DISTINCT FROM OLD.resource_type
            OR NEW.source_selection_revision_id   IS DISTINCT FROM OLD.source_selection_revision_id
            OR NEW.source_membership_digest       IS DISTINCT FROM OLD.source_membership_digest
            OR NEW.idempotency_key                IS DISTINCT FROM OLD.idempotency_key
            OR NEW.created_at                     IS DISTINCT FROM OLD.created_at
        THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: frozen content of revision % changed during completion', OLD.id
                USING ERRCODE = '23514';
        END IF;
        -- The transition verifies the definitions and membership set at
        -- the database: the claimed counts must equal the child rows that
        -- actually exist. The header row is locked by the completing
        -- transaction (appends and aborts take the same lock first), so
        -- this count is stable; the digests are re-derived by the store
        -- inside that same lock.
        EXECUTE format(
            'SELECT count(*) FROM %I.explorer_explicit_group_definitions WHERE revision_id = $1',
            TG_TABLE_SCHEMA
        ) INTO definition_rows USING OLD.id;
        EXECUTE format(
            'SELECT count(*) FROM %I.explorer_explicit_group_memberships WHERE revision_id = $1',
            TG_TABLE_SCHEMA
        ) INTO membership_rows USING OLD.id;
        IF NEW.group_count <> definition_rows THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: completion of revision % claims group_count % but % definition rows are stored',
                OLD.id, NEW.group_count, definition_rows
                USING ERRCODE = '23514';
        END IF;
        IF NEW.member_count <> membership_rows THEN
            RAISE EXCEPTION
                'explorer_explicit_group_revisions guard: completion of revision % claims member_count % but % membership rows are stored',
                OLD.id, NEW.member_count, membership_rows
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    -- STAGING stays STAGING: the lease channel of 000009, unchanged.
    IF NEW.writer_expires_at IS DISTINCT FROM OLD.writer_expires_at THEN
        -- Lease renewal is the only admitted non-completion UPDATE: the
        -- session must hold this row's writer token, and the lease only
        -- moves forward -- it is never revoked and never shortened, so no
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

    -- Identity, binding, and completion columns are frozen while the row
    -- is STAGING. The state column is deliberately absent here: the named
    -- lifecycle CHECK owns the shape so its refusal is classified by name.
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
    BEFORE INSERT OR UPDATE OR DELETE ON explorer_explicit_group_revisions
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_explicit_group_revision_guard();

CREATE OR REPLACE FUNCTION loom_explorer_explicit_group_membership_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    parent_state         text;
    parent_project       text;
    parent_generation    text;
    parent_resource_type text;
    parent_writer_token  text;
    parent_expires_at    timestamptz;
    abort_token          text := current_setting('loom.explorer_explicit_group_abort_token', true);
    cleanup_now          text := current_setting('loom.explorer_explicit_group_cleanup_now', true);
    diag_rows            integer;
BEGIN
    -- The parent lookup resolves through TG_TABLE_SCHEMA so the guard
    -- behaves identically no matter what search_path the calling session
    -- carries (same discipline as the definitions guard). FOUND is
    -- unreliable after EXECUTE ... INTO, so the row count drives the
    -- absent-parent branch.
    EXECUTE format(
        'SELECT state, project, generation, resource_type, writer_token, writer_expires_at'
            || ' FROM %I.explorer_explicit_group_revisions WHERE id = $1',
        TG_TABLE_SCHEMA
    ) INTO parent_state, parent_project, parent_generation, parent_resource_type,
           parent_writer_token, parent_expires_at
        USING COALESCE(NEW.revision_id, OLD.revision_id);
    GET DIAGNOSTICS diag_rows = ROW_COUNT;
    IF diag_rows = 0 THEN
        RAISE EXCEPTION
            'explorer_explicit_group_memberships guard: operation on membership of revision % refused: parent revision is absent',
            COALESCE(NEW.revision_id, OLD.revision_id)
            USING ERRCODE = '23514';
    END IF;

    IF TG_OP = 'INSERT' THEN
        -- Memberships are staged, never backfilled: a COMPLETED revision
        -- can never gain members, and every reference repeats the parent's
        -- exact project/generation/resource-type scope (the domain's
        -- Ref.Validate as a database fact).
        IF parent_state <> 'STAGING' THEN
            RAISE EXCEPTION
                'explorer_explicit_group_memberships guard: INSERT refused: parent revision % is % and only STAGING revisions accept members',
                COALESCE(NEW.revision_id, OLD.revision_id), parent_state
                USING ERRCODE = '23514';
        END IF;
        IF NEW.project <> parent_project
            OR NEW.generation <> parent_generation
            OR NEW.resource_type <> parent_resource_type THEN
            RAISE EXCEPTION
                'explorer_explicit_group_memberships guard: INSERT refused for revision %: reference scope %.%.% does not repeat the parent scope %.%.%',
                NEW.revision_id,
                NEW.project, NEW.generation, NEW.resource_type,
                parent_project, parent_generation, parent_resource_type
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.*) IS NOT DISTINCT FROM ROW(OLD.*) THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION
            'explorer_explicit_group_memberships guard: membership % of revision % under group % is write-once',
            OLD.resource_id, OLD.revision_id, OLD.group_id
            USING ERRCODE = '23514';
    END IF;

    -- DELETE below: only ever as part of an authorized teardown of the
    -- parent staging revision, under the same two GUC channels the
    -- header and definition guards demand.
    IF parent_state <> 'STAGING' THEN
        RAISE EXCEPTION
            'explorer_explicit_group_memberships guard: DELETE refused: parent revision % is %',
            OLD.revision_id, parent_state
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
        'explorer_explicit_group_memberships guard: DELETE of membership % of revision % under group % refused without owner-abort or expired-writer-cleanup authorization',
        OLD.resource_id, OLD.revision_id, OLD.group_id
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS explorer_explicit_group_memberships_guard ON explorer_explicit_group_memberships;
CREATE TRIGGER explorer_explicit_group_memberships_guard
    BEFORE INSERT OR UPDATE OR DELETE ON explorer_explicit_group_memberships
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_explicit_group_membership_guard();
