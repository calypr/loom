-- pg-state-explicit-group-members-v1 (CAS escalation): harden the
-- explicit-group definition guard without touching merged history.
--
-- Migration 000009 is merged history and is never edited. Its
-- loom_explorer_explicit_group_definition_guard had two holes that the
-- CAS verifier turned into served-state witnesses:
--
--   * The trigger fired BEFORE UPDATE OR DELETE only, so a direct-SQL
--     definition INSERT never fired the guard at all. The verifier
--     attached a definition to a header mid-completion and served
--     diverged facts.
--   * The DELETE parent lookup was a plain SELECT (a snapshot read), so
--     a definition DELETE racing a completion could be authorized
--     against a stale STAGING image while the parent completed in
--     parallel -- E1 admitted the deletion 3/3 deterministically,
--     leaving a frozen group_count of 2 over one stored definition row.
--
-- This migration CREATE OR REPLACEs the same-named function with the
-- shape verified for the membership guard in 000010: a single shared
-- parent lookup before any branch decides, locked FOR KEY SHARE (the
-- foreign-key row-lock mode, which conflicts with every writer
-- transition -- the store claims the header FOR UPDATE -- so the
-- intruding session queues behind the completing transaction and
-- re-reads the committed state), plus the missing INSERT branch:
-- definitions are staged, never backfilled, and only a STAGING parent
-- accepts them. The trigger is recreated BEFORE INSERT OR UPDATE OR
-- DELETE. Authorization channels for DELETE are unchanged (STAGING
-- parent plus owner-abort token or expired-writer cleanup).
--
-- Convergence: CREATE OR REPLACE FUNCTION plus DROP TRIGGER IF EXISTS +
-- CREATE TRIGGER re-establish the identical guard on a rerun, never a
-- weakened one; the migration records no state and never drops or
-- truncates.

CREATE OR REPLACE FUNCTION loom_explorer_explicit_group_definition_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    parent_state        text;
    parent_writer_token text;
    parent_expires_at   timestamptz;
    abort_token         text := current_setting('loom.explorer_explicit_group_abort_token', true);
    cleanup_now         text := current_setting('loom.explorer_explicit_group_cleanup_now', true);
    diag_rows           integer;
BEGIN
    -- Shared parent lookup, placed before every branch so no branch can
    -- decide without it. Resolves through TG_TABLE_SCHEMA (search_path
    -- discipline, same as the original) and locks the parent row -- the
    -- TOCTOU close witnessed in 000010: without the row lock this is a
    -- snapshot read, and a direct-SQL INSERT (or an abort-channel
    -- DELETE) can be authorized against a parent image that a
    -- concurrent completion is about to flip. FOUND is unreliable after
    -- EXECUTE ... INTO, so the row count drives the absent-parent
    -- branch.
    EXECUTE format(
        'SELECT state, writer_token, writer_expires_at FROM %I.explorer_explicit_group_revisions WHERE id = $1'
            || ' FOR KEY SHARE',
        TG_TABLE_SCHEMA
    ) INTO parent_state, parent_writer_token, parent_expires_at
        USING COALESCE(NEW.revision_id, OLD.revision_id);
    GET DIAGNOSTICS diag_rows = ROW_COUNT;
    IF diag_rows = 0 THEN
        RAISE EXCEPTION
            'explorer_explicit_group_definitions guard: operation on a definition of revision % refused: parent revision is absent',
            COALESCE(NEW.revision_id, OLD.revision_id)
            USING ERRCODE = '23514';
    END IF;

    IF TG_OP = 'INSERT' THEN
        -- The closed hole: the 000009 trigger never fired here. A
        -- completed revision can never gain definitions, staged or
        -- otherwise -- same discipline the membership guard's INSERT
        -- branch carries.
        IF parent_state <> 'STAGING' THEN
            RAISE EXCEPTION
                'explorer_explicit_group_definitions guard: INSERT refused: parent revision % is % and only STAGING revisions accept definitions',
                COALESCE(NEW.revision_id, OLD.revision_id), parent_state
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

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
    -- parent staging revision -- same authorization as 000009, now read
    -- under the locked lookup above so the authorization cannot be
    -- stale.
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
    BEFORE INSERT OR UPDATE OR DELETE ON explorer_explicit_group_definitions
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_explicit_group_definition_guard();
