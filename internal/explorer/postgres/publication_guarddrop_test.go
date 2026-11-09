package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/explorer"
)

// ---------------------------------------------------------------------------
// T13: guard-drop non-vacuity. Every guard this suite relies on is dropped
// (or its predicate surgically removed), the violating write is witnessed
// succeeding, and the guard -- restored from the migration's own text where
// applicable -- refuses it again. Mirrors the sibling lanes' probes
// (receipts_test.go / revisions_test.go).
// ---------------------------------------------------------------------------

// publishGuardWorld publishes two revisions so rev1 is frozen SUPERSEDED,
// rev2 is ACTIVE, and the pointer sits at revision 2 -- the state every
// fence probe needs.
func publishGuardWorld(t *testing.T, f *pubAuthFixture, project, explorerID string,
) (revision1ID, revision2ID, release1ID, release2ID string, ps *PublishStore) {
	t.Helper()
	store := f.newStore(t)
	ps = f.newPublishStore(t)
	receipt1, revision1, release1 := f.seedPublishedWorld(t, store, project, explorerID, "gd1")
	f.publishOK(t, ps, receipt1, revision1, release1, 0)
	receipt2, revision2, release2 := secondPublishInputsSameExplorer(t, f, project, explorerID, "gd2")
	f.publishOK(t, ps, receipt2, revision2, release2, 1)
	return revision1.ID, revision2.ID, release1.ID, release2.ID, ps
}

// TestPublishAuthoringRevisionStateTriggerIsLoadBearing drops the 000006
// explorer_revisions_guard trigger and witnesses the resurrection it
// prevents, then restores the guard from the migration's own text and
// re-proves the exact-named refusal.
func TestPublishAuthoringRevisionStateTriggerIsLoadBearing(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	revision1ID, revision2ID, _, _, _ := publishGuardWorld(t, f, "proj_pubauth_gd_trigger", "exp_gd")
	revs := f.ctlTable(RevisionsTable)

	// With the guard in place: the exact-named refusal.
	if _, err := f.iso.Exec(f.ctx, "UPDATE "+revs+" SET status = 'READY' WHERE id = $1", revision1ID); err == nil {
		t.Fatal("resurrection SUPERSEDED -> READY accepted while the state-machine trigger is installed")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "guarded resurrection")
		if !strings.Contains(pgErr.Message, "illegal explorer revision") ||
			!strings.Contains(pgErr.Message, "status transition") {
			t.Fatalf("resurrection refused by something other than the state machine: %s", pgErr.Message)
		}
	}

	// Guard dropped: the violating write really lands (non-vacuity witness).
	if _, err := f.iso.Exec(f.ctx,
		"DROP TRIGGER explorer_revisions_guard ON "+revs); err != nil {
		f.fail("drop revision state-machine trigger", err)
	}
	if _, err := f.iso.Exec(f.ctx, "UPDATE "+revs+" SET status = 'READY' WHERE id = $1", revision1ID); err != nil {
		f.fail("resurrection without the trigger must succeed", err)
	}
	var resurrected string
	if err := f.iso.QueryRow(f.ctx, "SELECT status FROM "+revs+" WHERE id = $1", revision1ID).Scan(&resurrected); err != nil {
		f.fail("read resurrected status", err)
	}
	if resurrected != "READY" {
		t.Fatalf("guard-removal resurrection did not persist: status = %q", resurrected)
	}
	// Deletion (insert-once branch) is also a real hole without the trigger.
	if _, err := f.iso.Exec(f.ctx, "DELETE FROM "+revs+" WHERE id = $1", revision2ID); err != nil {
		f.fail("DELETE without the trigger must succeed", err)
	}
	if got := f.countRows(t, revs, "id = $1", revision2ID); got != 0 {
		t.Fatalf("guard-removal DELETE did not persist: %d rows remain", got)
	}

	// Restored from the migration's own text: illegal writes are refused
	// again with the same exact names. (rev1 sits READY after the
	// unguarded resurrection, so the probes use fresh illegal transitions:
	// READY -> SUPERSEDED is outside the legal edge set, and touching a
	// frozen column fires the frozen branch.)
	f.rerunMigration(t, 6)
	if _, err := f.iso.Exec(f.ctx, "UPDATE "+revs+" SET status = 'SUPERSEDED' WHERE id = $1", revision1ID); err == nil {
		t.Fatal("illegal READY -> SUPERSEDED transition accepted after the trigger was restored from the migration text")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "restored state machine")
		if !strings.Contains(pgErr.Message, "status transition") {
			t.Fatalf("restored refusal came from something other than the state machine: %s", pgErr.Message)
		}
	}
	if _, err := f.iso.Exec(f.ctx,
		"UPDATE "+revs+" SET content_digest = content_digest || 'x' WHERE id = $1", revision1ID); err == nil {
		t.Fatal("frozen-column tamper accepted after the trigger was restored")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "restored frozen guard")
		if !strings.Contains(pgErr.Message, "frozen content changed") {
			t.Fatalf("restored tamper refusal came from something else: %s", pgErr.Message)
		}
	}
	if _, err := f.iso.Exec(f.ctx, "DELETE FROM "+revs+" WHERE id = $1", revision1ID); err == nil {
		t.Fatal("DELETE accepted after the trigger was restored")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "restored insert-once guard")
		if !strings.Contains(pgErr.Message, "insert-once") {
			t.Fatalf("restored DELETE refusal came from something else: %s", pgErr.Message)
		}
	}
}

// TestPublishAuthoringCandidateStatusFenceIsLoadBearing removes the
// status='READY' predicate from the activation CAS statement itself and
// witnesses that a SUPERSEDED revision becomes ACTIVE -- proving the fence,
// not the surrounding flow, is what refuses. The state-machine trigger is
// shown to be the independent backstop behind the fence.
func TestPublishAuthoringCandidateStatusFenceIsLoadBearing(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	revision1ID, _, _, _, ps := publishGuardWorld(t, f, "proj_pubauth_gd_cand", "exp_gd")

	guarded := ps.stmts.candidateActive
	fenceless := strings.Replace(guarded, " AND status = 'READY'", "", 1)
	if guarded == fenceless {
		t.Fatal("fence-removal probe did not change the statement -- probe is vacuous")
	}
	args := []any{revision1ID, time.Now().UTC(), []byte("[]"), []byte(`{"state":"READY"}`)}

	// With trigger installed: the fenceless statement is still refused --
	// by the trigger, proving it is an independent layer, not a re-check.
	if _, err := f.iso.Exec(f.ctx, fenceless, args...); err == nil {
		t.Fatal("fenceless activation CAS landed while the state-machine trigger was installed")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "fenceless activation with trigger")
		if !strings.Contains(pgErr.Message, "status transition") {
			t.Fatalf("fenceless activation refused by something other than the state machine: %s", pgErr.Message)
		}
	}

	// Trigger dropped: the GUARDED statement still refuses (0 rows -- the
	// fence alone is load-bearing) while the FENCELESS statement lands and
	// the SUPERSEDED row really becomes ACTIVE.
	if _, err := f.iso.Exec(f.ctx,
		"DROP TRIGGER explorer_revisions_guard ON "+f.ctlTable(RevisionsTable)); err != nil {
		f.fail("drop revision state-machine trigger", err)
	}
	tag, err := f.iso.Exec(f.ctx, guarded, args...)
	if err != nil {
		f.fail("guarded activation CAS", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("guarded activation CAS moved a SUPERSEDED row (%d rows) -- the test world is wrong", tag.RowsAffected())
	}
	if _, err := f.iso.Exec(f.ctx, fenceless, args...); err != nil {
		f.fail("fenceless activation without the trigger must succeed", err)
	}
	var status string
	if err := f.iso.QueryRow(f.ctx, "SELECT status FROM "+f.ctlTable(RevisionsTable)+" WHERE id = $1",
		revision1ID).Scan(&status); err != nil {
		f.fail("read fenceless-activated status", err)
	}
	if status != "ACTIVE" {
		t.Fatalf("fence-removal activation did not persist: status = %q", status)
	}
	f.rerunMigration(t, 6)
}

// TestPublishAuthoringSupersedeStatusFenceIsLoadBearing removes the
// status='ACTIVE' predicate from the supersede CAS and witnesses a frozen
// SUPERSEDED row being re-superseded -- the fence alone refuses it.
func TestPublishAuthoringSupersedeStatusFenceIsLoadBearing(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	revision1ID, _, _, _, ps := publishGuardWorld(t, f, "proj_pubauth_gd_super", "exp_gd")

	guarded := ps.stmts.priorSupersede
	fenceless := strings.Replace(guarded, " AND status = 'ACTIVE'", "", 1)
	if guarded == fenceless {
		t.Fatal("fence-removal probe did not change the statement -- probe is vacuous")
	}
	tag, err := f.iso.Exec(f.ctx, guarded, revision1ID)
	if err != nil {
		f.fail("guarded supersede CAS", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("guarded supersede moved an already-SUPERSEDED row -- the test world is wrong")
	}
	tag, err = f.iso.Exec(f.ctx, fenceless, revision1ID)
	if err != nil {
		f.fail("fenceless supersede", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("fenceless supersede affected %d rows, want 1 (the fence is what refuses)", tag.RowsAffected())
	}
}

// TestPublishAuthoringReleasePointerCASIsLoadBearing removes the
// revision-conditional predicate from the pointer CAS and witnesses a stale
// writer moving the pointer anyway -- the CAS condition alone enforces the
// expected-revision contract that T7 pins at publish level.
func TestPublishAuthoringReleasePointerCASIsLoadBearing(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	_, _, release1ID, _, ps := publishGuardWorld(t, f, "proj_pubauth_gd_ptr", "exp_gd")
	pointers := f.lanesTable("active_project_release")

	guarded := ps.stmts.releasePointerCAS
	fenceless := strings.Replace(guarded, " AND revision = $4", "", 1)
	if guarded == fenceless {
		t.Fatal("fence-removal probe did not change the statement -- probe is vacuous")
	}
	// Guarded: a stale expected revision (999 vs actual 2) moves nothing.
	if err := f.iso.QueryRow(f.ctx, guarded, "proj_pubauth_gd_ptr", release1ID, int64(99), int64(999)).Scan(new(int64)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("guarded pointer CAS accepted a stale expected revision: %v", err)
	}
	// Fenceless: the same stale writer lands -- the CAS predicate alone
	// enforces the contract. $4 existed only inside the removed fence, so
	// the fenceless variant takes the same statement minus that predicate
	// and drops the now-unreferenced expected-revision argument.
	if _, err := f.iso.Exec(f.ctx, fenceless, "proj_pubauth_gd_ptr", release1ID, int64(99)); err != nil {
		f.fail("fenceless pointer CAS must succeed", err)
	}
	var revision int64
	var releaseID string
	if err := f.iso.QueryRow(f.ctx,
		"SELECT revision, release_id FROM "+pointers+" WHERE project = $1", "proj_pubauth_gd_ptr").
		Scan(&revision, &releaseID); err != nil {
		f.fail("read fenced pointer", err)
	}
	if revision != 99 {
		t.Fatalf("fenceless pointer move did not persist: revision = %d", revision)
	}
}

// TestPublishAuthoringOwnerGuardModePredicateIsLoadBearing removes the
// management-mode predicate from the activation guard and witnesses the
// wrong-mode owner row matching -- the predicate alone produces the
// indistinguishable guard-miss refusal T8 pins at publish level.
func TestPublishAuthoringOwnerGuardModePredicateIsLoadBearing(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	if _, err := store.Create(f.ctx, pubauthOwner("proj_pubauth_gd_mode", "exp_repo", explorer.ManagementRepository)); err != nil {
		f.fail("create REPOSITORY-mode owner", err)
	}
	ps := f.newPublishStore(t)

	guarded := ps.stmts.ownerGuard
	fenceless := strings.Replace(guarded, " AND management_mode = $3", "", 1)
	if guarded == fenceless {
		t.Fatal("predicate-removal probe did not change the statement -- probe is vacuous")
	}
	// The publish path derives INTERACTIVE for a non-default explorer, so
	// the REPOSITORY row must not match the guard...
	tag, err := f.iso.Exec(f.ctx, guarded, "proj_pubauth_gd_mode", "exp_repo", "INTERACTIVE")
	if err != nil {
		f.fail("guarded owner guard read", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("owner guard matched a wrong-mode row (%d rows) -- the test world is wrong", tag.RowsAffected())
	}
	// ...and does match once the predicate is removed: the predicate is the
	// sole enforcement.
	tag, err = f.iso.Exec(f.ctx, fenceless, "proj_pubauth_gd_mode", "exp_repo")
	if err != nil {
		f.fail("predicateless owner guard read", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("predicateless guard affected %d rows, want 1 -- the mode predicate is what refuses", tag.RowsAffected())
	}
}
