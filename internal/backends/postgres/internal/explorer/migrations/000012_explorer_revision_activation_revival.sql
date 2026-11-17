-- pg-state-revision-activation-revival-v1 (guard evolution): admit the
-- activation-shaped FAILED->ACTIVE revival without editing merged history.
--
-- Migration 000006 is merged history and is never edited. Its state machine
-- admits only READY->ACTIVE, READY->FAILED, ACTIVE->SUPERSEDED and
-- ACTIVE->FAILED, so a FAILED revision can never come back. That made one
-- consumer-visible outcome permanent on the PostgreSQL lane that the Arango
-- lane recovers from: the shared lifecycle marks the candidate FAILED on
-- every activation miss and a retried deploy converges onto the same
-- deterministic revision identity (content-digest keyed), so a lost
-- activation race pinned the content identity to a permanent refusal, while
-- the Arango baseline (candidate filter READY-or-FAILED plus the ACTIVE
-- replay arm, clearing failedAt/diagnostics on activation) reactivates on
-- retry.
--
-- This migration CREATE OR REPLACEs loom_explorer_revision_guard with one
-- additional arm, following the sanctioned guard-evolution precedent of
-- 000011_explorer_explicit_group_definition_guard_hardening: same-named
-- function replaced whole, trigger dropped and recreated, convergence on
-- rerun, never a weakened one, no state recorded, no drops or truncates.
--
-- The arm is the activation shape only -- strictly narrower than the Arango
-- baseline, which admits READY-or-FAILED blindly:
--
--   * status must become exactly 'ACTIVE';
--   * failed_at must be cleared to NULL (the activation write clears it);
--   * activated_at must be set (an activation always stamps it);
--   * diagnostics must be present (activation rewrites the diagnostic set,
--     never nulls it);
--   * ready_at must be unchanged (readiness is a one-time fact).
--
-- A bare resurrection (FAILED->ACTIVE keeping failed_at, or with no
-- activated_at, or nulling diagnostics) is refused with 23514, and any
-- frozen-column smuggling during the transition is refused by the
-- untouched frozen check that runs first. All other FAILED departures
-- (FAILED->READY, FAILED->FAILED, FAILED->SUPERSEDED) and every transition
-- out of SUPERSEDED stay refused exactly as 000006 had them. The adapter's
-- revival write (candidateRevive, WHERE id = $1 AND status = 'FAILED') is
-- the only store path that produces the admitted shape; the first-CAS
-- statement candidateActive stays READY-only and the authoring publish path
-- is untouched.
--
-- Convergence: CREATE OR REPLACE FUNCTION plus DROP TRIGGER IF EXISTS +
-- CREATE TRIGGER re-establish the identical guard on a rerun; the migration
-- records no ledger row of its own beyond the runner's and never weakens
-- the law.

CREATE OR REPLACE FUNCTION loom_explorer_revision_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- Insert-once against direct SQL: a DELETE followed by a re-INSERT of
    -- different frozen content would fire no guard on the fresh row, so
    -- deletions are refused outright. NEW is NULL in a BEFORE DELETE row
    -- trigger, so the no-op shortcut and the frozen-column comparisons
    -- below cannot be relied on for deletes; the refusal carries its own
    -- message instead.
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION
            'explorer_revisions is insert-once: DELETE of revision % is refused',
            OLD.id
            USING ERRCODE = '23514';
    END IF;

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

    -- The 000012 arm: a FAILED revision may return to ACTIVE only through
    -- the activation shape, so revival cannot forge an activation witness
    -- and cannot bypass the READY path's CAS discipline. Every other
    -- departure from FAILED stays illegal.
    IF OLD.status = 'FAILED' AND NEW.status = 'ACTIVE' THEN
        IF NEW.failed_at IS NULL
            AND NEW.activated_at IS NOT NULL
            AND NEW.diagnostics IS NOT NULL
            AND NEW.ready_at IS NOT DISTINCT FROM OLD.ready_at
        THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION
            'illegal explorer revision % status transition % -> %: FAILED->ACTIVE is admitted only as the activation-shaped write (failed_at cleared, activated_at set, diagnostics present, ready_at unchanged)',
            OLD.id, OLD.status, NEW.status
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
    BEFORE UPDATE OR DELETE ON explorer_revisions
    FOR EACH ROW
    EXECUTE FUNCTION loom_explorer_revision_guard();
