package postgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/explorer"
)

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

func TestPublishAuthoringRevisionStateTriggerIsLoadBearing(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	revision1ID, revision2ID, _, _, _ := publishGuardWorld(t, f, "proj_pubauth_gd_trigger", "exp_gd")
	revs := f.ctlTable(RevisionsTable)

	if _, err := f.iso.Exec(f.ctx, "UPDATE "+revs+" SET status = 'READY' WHERE id = $1", revision1ID); err == nil {
		t.Fatal("resurrection SUPERSEDED -> READY accepted while the state-machine trigger is installed")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "guarded resurrection")
		if !strings.Contains(pgErr.Message, "illegal explorer revision") ||
			!strings.Contains(pgErr.Message, "status transition") {
			t.Fatalf("resurrection refused by something other than the state machine: %s", pgErr.Message)
		}
	}

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
	if _, err := f.iso.Exec(f.ctx, "DELETE FROM "+revs+" WHERE id = $1", revision2ID); err != nil {
		f.fail("DELETE without the trigger must succeed", err)
	}
	if got := f.countRows(t, revs, "id = $1", revision2ID); got != 0 {
		t.Fatalf("guard-removal DELETE did not persist: %d rows remain", got)
	}

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

	if _, err := f.iso.Exec(f.ctx, fenceless, args...); err == nil {
		t.Fatal("fenceless activation CAS landed while the state-machine trigger was installed")
	} else {
		pgErr := requirePgErrPubAuth(t, err, "23514", "fenceless activation with trigger")
		if !strings.Contains(pgErr.Message, "status transition") {
			t.Fatalf("fenceless activation refused by something other than the state machine: %s", pgErr.Message)
		}
	}

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
	if err := f.iso.QueryRow(f.ctx, guarded, "proj_pubauth_gd_ptr", release1ID, int64(99), int64(999)).Scan(new(int64)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("guarded pointer CAS accepted a stale expected revision: %v", err)
	}
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
	tag, err := f.iso.Exec(f.ctx, guarded, "proj_pubauth_gd_mode", "exp_repo", "INTERACTIVE")
	if err != nil {
		f.fail("guarded owner guard read", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("owner guard matched a wrong-mode row (%d rows) -- the test world is wrong", tag.RowsAffected())
	}
	tag, err = f.iso.Exec(f.ctx, fenceless, "proj_pubauth_gd_mode", "exp_repo")
	if err != nil {
		f.fail("predicateless owner guard read", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("predicateless guard affected %d rows, want 1 -- the mode predicate is what refuses", tag.RowsAffected())
	}
}
