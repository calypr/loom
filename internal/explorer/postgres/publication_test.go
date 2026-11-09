package postgres

// PublishAuthoring contract tests for pg-state-publish-authoring-atomic-v1.
//
// Every claim is made against a real PostgreSQL 18 with literal rows and
// direct-SQL witnesses (no mocks, no skips once a service is present):
// single-transaction commit-boundary visibility through an in-transaction
// crash seam observed from a second real backend, all-or-nothing crash
// windows at every commit seam with post-crash convergence, idempotent
// exact-payload replay proven zero-write by full-row byte identity, the
// verified-receipt / immutable-revision / release-CAS / activation-guard
// typed refusals with full cross-lane rollback, two-real-connection races
// with distinct pg_backend_pid witnesses, byte-frozen golden pins for the
// statement set, digests, and stored rows with break-one-pin proofs,
// guard-drop non-vacuity for every claimed guard, and Go-side
// canonicalization as the single source of truth.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataset"
	datasetpostgres "github.com/calypr/loom/internal/dataset/postgres"
	"github.com/calypr/loom/internal/explorer"
)

// clockColumns are the store-computed timestamptz columns of the four
// publish legs. Cross-run golden comparisons mask exactly these (and the
// lane defaults) -- every other column of every row must be byte-identical
// to the clean-run golden, and no mask exists for any other column.
var (
	maskRevision = []string{"activated_at"}
	maskOwner    = []string{"updated_at"}
	maskPointer  = []string{"updated_at", "created_at"}
	maskManifest = []string{"updated_at", "created_at"}
	maskAll      = []string{"activated_at", "updated_at", "created_at", "ready_at", "failed_at"}
)

// publishWitnessTables are every table a publish authoring commit touches.
var publishWitnessTables = []struct {
	table string // fixture-qualified
	mask  []string
}{
	{ReceiptsTable, nil},
	{RevisionsTable, maskAll},
	{OwnerTable, maskAll},
	{"project_release", nil},
	{"active_project_release", maskPointer},
	{"dataset_generation_manifest", maskManifest},
	{"dataset_active_generation", maskPointer},
}

// snapshotAll renders the deterministic witness string of every publish
// table in one call.
func (f *pubAuthFixture) snapshotAll(t *testing.T, maskClock bool) string {
	t.Helper()
	var b strings.Builder
	for _, w := range publishWitnessTables {
		table := w.table
		mask := w.mask
		switch table {
		case ReceiptsTable, RevisionsTable, OwnerTable:
			table = f.ctlTable(table)
		default:
			table = f.lanesTable(table)
		}
		if !maskClock {
			mask = nil
		}
		fmt.Fprintf(&b, "%s|%s\x1d\n", table, f.snapshot(t, table, mask...))
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// T1: the happy path commits all four legs and returns what was stored.
// ---------------------------------------------------------------------------

func TestPublishAuthoringCommitsAllFourLegsAtomically(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	ps := f.newPublishStore(t)
	project, explorerID, tag := "proj_pubauth_legs", "exp_legs", "legs"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)

	started := time.Now().UTC().Add(-time.Minute)
	out := f.publishOK(t, ps, receipt, revision, release, 0)

	// The returned revision is what PostgreSQL persisted.
	if out.ID != revision.ID || out.Status != explorer.RevisionActive {
		t.Fatalf("returned revision = %q/%q, want %q/ACTIVE", out.ID, out.Status, revision.ID)
	}
	if out.ActivatedAt == nil || out.ActivatedAt.Before(started) {
		t.Fatalf("returned activated_at = %v, want a fresh UTC time", out.ActivatedAt)
	}
	if out.Publication.State != string(explorer.RevisionActive) ||
		out.Publication.RevisionID != revision.ID ||
		out.Publication.ExecutionID != "exec_pubauth_"+tag ||
		out.Publication.Generation != pubauthGeneration ||
		out.Publication.UpdatedAt.Before(started) {
		t.Fatalf("returned publication metadata = %+v, want activation merge over the stored document", out.Publication)
	}
	if len(out.Diagnostics) != 0 {
		t.Fatalf("returned diagnostics = %+v, want the activation-cleared empty list", out.Diagnostics)
	}

	// Independent-connection witnesses: read every leg back through stores
	// and raw SQL on fresh pools, proving persistence past any single
	// connection.
	stored, err := store.GetRevisionForExplorer(f.ctx, project, explorerID, revision.ID)
	if err != nil {
		t.Fatalf("read back revision: %v", err)
	}
	if stored.Status != explorer.RevisionActive || stored.ActivatedAt == nil || stored.FailedAt != nil {
		t.Fatalf("stored revision lifecycle = %q activated=%v failed=%v, want ACTIVE/now/NULL",
			stored.Status, stored.ActivatedAt, stored.FailedAt)
	}
	owner, err := store.Get(f.ctx, project, explorerID)
	if err != nil {
		t.Fatalf("read back owner: %v", err)
	}
	if owner.ActiveRevisionID != revision.ID {
		t.Fatalf("owner active_revision_id = %q, want %q", owner.ActiveRevisionID, revision.ID)
	}
	rel := f.newReleaseStore(t)
	active, err := rel.ReadActiveRelease(f.ctx, project)
	if err != nil {
		t.Fatalf("read back active release: %v", err)
	}
	if active.Release.ID != release.ID || active.Revision != 1 {
		t.Fatalf("active release = %s@%d, want %s@1", active.Release.ID, active.Revision, release.ID)
	}
	if _, err := store.GetCompilationReceiptForExplorer(f.ctx, project, explorerID, receipt.ID); err != nil {
		t.Fatalf("read back receipt: %v", err)
	}

	// The activation publication document: stored jsonb carries the merge
	// exactly -- state/revisionId/updatedAt overwritten, executionId and
	// every other stored key carried over, generation defaulted.
	var publication map[string]json.RawMessage
	if err := f.iso.QueryRow(f.ctx, "SELECT publication FROM "+f.ctlTable(RevisionsTable)+" WHERE id = $1",
		revision.ID).Scan(&publication); err != nil {
		f.fail("read stored publication", err)
	}
	keys := make([]string, 0, len(publication))
	for key := range publication {
		keys = append(keys, key)
	}
	wantKeys := "executionId,generation,revisionId,state,updatedAt"
	if got := strings.Join(sortedJoin(keys), ","); got != wantKeys {
		t.Fatalf("activation publication key set = %s, want %s", got, wantKeys)
	}
	if string(publication["state"]) != `"ACTIVE"` || string(publication["revisionId"]) != `"`+revision.ID+`"` {
		t.Fatalf("activation publication overwrite fields = %s/%s, want ACTIVE/%s",
			publication["state"], publication["revisionId"], revision.ID)
	}
	if want := `"exec_pubauth_` + tag + `"`; string(publication["executionId"]) != want {
		t.Fatalf("activation publication lost the stored executionId: got %s, want %s", publication["executionId"], want)
	}
	var diagKind string
	if err := f.iso.QueryRow(f.ctx, "SELECT diagnostics::text FROM "+f.ctlTable(RevisionsTable)+" WHERE id = $1",
		revision.ID).Scan(&diagKind); err != nil {
		f.fail("read stored diagnostics", err)
	}
	if diagKind != "[]" {
		t.Fatalf("stored diagnostics = %s, want the activation-cleared [] document", diagKind)
	}

	// One statement on one fresh connection sees all four legs in a single
	// snapshot -- the read side of commit-boundary consistency.
	var (
		receiptID, revStatus, pubState, activeRevision, pointerRelease string
		pointerRevision                                                int64
	)
	err = f.iso.QueryRow(f.ctx, `
SELECT r.id, v.status, v.publication->>'state', e.active_revision_id, p.release_id, p.revision
FROM `+f.ctlTable(ReceiptsTable)+` r,
     `+f.ctlTable(RevisionsTable)+` v,
     `+f.ctlTable(OwnerTable)+` e,
     `+f.lanesTable("active_project_release")+` p
WHERE r.project = $1 AND r.explorer_id = $2 AND r.id = $3
  AND v.id = $4 AND v.project = $1 AND v.explorer_id = $2
  AND e.project = $1 AND e.explorer_id = $2
  AND p.project = $1`,
		project, explorerID, receipt.ID, revision.ID).
		Scan(&receiptID, &revStatus, &pubState, &activeRevision, &pointerRelease, &pointerRevision)
	if err != nil {
		f.fail("single-snapshot four-leg read", err)
	}
	if receiptID != receipt.ID || revStatus != "ACTIVE" || pubState != "ACTIVE" ||
		activeRevision != revision.ID || pointerRelease != release.ID || pointerRevision != 1 {
		t.Fatalf("four-leg snapshot = %q/%q/%q/%q/%q@%d, want all legs committed consistently",
			receiptID, revStatus, pubState, activeRevision, pointerRelease, pointerRevision)
	}
}

func sortedJoin(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// T2: commit-boundary visibility -- in-transaction observer from a second
// real backend sees NOTHING until COMMIT, and total rollback on abort.
// ---------------------------------------------------------------------------

func TestPublishAuthoringCommitBoundaryVisibility(t *testing.T) {
	t.Run("stopped-before-commit-hides-every-leg-and-locks-one-transaction", func(t *testing.T) {
		f, ps, writerPID, observer, observerPID, releaseGate, reached := stopPublishAtPreCommit(t)
		project, explorerID, tag := "proj_pubauth_boundary", "exp_boundary", "boundary"
		store := f.newStore(t)
		receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)

		done := make(chan error, 1)
		go func() {
			_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
			done <- err
		}()

		var stoppedPID int32
		select {
		case stoppedPID = <-reached:
		case <-time.After(30 * time.Second):
			t.Fatal("writer never reached the pre-commit seam")
		}
		if stoppedPID != writerPID {
			t.Fatalf("writer transaction runs on backend %d, want the dedicated %d", stoppedPID, writerPID)
		}
		requireDistinctBackendsPubAuth(t, writerPID, observerPID, "publish writer", "observer")

		// The observer on its own real backend sees none of the four legs.
		if got := f.countRows(t, f.ctlTable(ReceiptsTable), "project = $1", project); got != 0 {
			t.Fatalf("observer sees %d receipt rows before COMMIT, want 0", got)
		}
		if got := f.countRows(t, f.ctlTable(RevisionsTable), "project = $1", project); got != 0 {
			t.Fatalf("observer sees %d revision rows before COMMIT, want 0", got)
		}
		var ownerActive string
		if err := observer.QueryRow(f.ctx, "SELECT active_revision_id FROM "+f.ctlTable(OwnerTable)+" WHERE project = $1",
			project).Scan(&ownerActive); err != nil {
			f.fail("observer reads owner", err)
		}
		if ownerActive != "" {
			t.Fatalf("observer sees owner active_revision_id = %q before COMMIT, want empty", ownerActive)
		}
		if got := f.countRows(t, f.lanesTable("active_project_release"), "project = $1", project); got != 0 {
			t.Fatalf("observer sees %d active release pointers before COMMIT, want 0", got)
		}

		// Single-transaction witness: the writer backend holds row locks on
		// EVERY table across BOTH schemas at once, including the release and
		// generation lanes' rows.
		locks := f.locksHeldBy(t, observer, writerPID)
		for _, table := range []string{
			ReceiptsTable, RevisionsTable, OwnerTable,
			"project_release", "active_project_release",
			"dataset_generation_manifest", "dataset_active_generation",
		} {
			if !locks[table] {
				t.Fatalf("writer backend %d holds no lock on %s at the pre-commit seam; publish legs are not in one transaction: %v",
					writerPID, table, locks)
			}
		}

		// Release the writer to COMMIT.
		releaseGate <- nil
		if err := <-done; err != nil {
			t.Fatalf("PublishAuthoring after commit release: %v", err)
		}
		// Post-commit, the observer's single-snapshot sees all four legs.
		var legs int
		if err := observer.QueryRow(f.ctx, `
SELECT (SELECT count(*) FROM `+f.ctlTable(ReceiptsTable)+` WHERE project = $1)
     + (SELECT count(*) FROM `+f.ctlTable(RevisionsTable)+` WHERE project = $1 AND status = 'ACTIVE')
     + (SELECT count(*) FROM `+f.ctlTable(OwnerTable)+` WHERE project = $1 AND active_revision_id = $2)
     + (SELECT count(*) FROM `+f.lanesTable("active_project_release")+` WHERE project = $1 AND revision = 1)`,
			project, revision.ID).Scan(&legs); err != nil {
			f.fail("observer four-leg read", err)
		}
		if legs != 4 {
			t.Fatalf("post-commit four-leg count = %d, want 4 (all legs visible together)", legs)
		}
	})

	t.Run("abort-after-every-write-rolls-back-every-leg", func(t *testing.T) {
		f, ps, _, _, _, releaseGate, reached := stopPublishAtPreCommit(t)
		project, explorerID, tag := "proj_pubauth_abort", "exp_abort", "abort"
		store := f.newStore(t)
		receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
		pre := f.snapshotAll(t, false)

		done := make(chan error, 1)
		go func() {
			_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
			done <- err
		}()
		select {
		case <-reached:
		case <-time.After(30 * time.Second):
			t.Fatal("writer never reached the pre-commit seam")
		}
		releaseGate <- errors.New("injected crash after every write")
		err := <-done
		if err == nil {
			t.Fatal("PublishAuthoring succeeded despite the injected pre-commit crash")
		}
		if !strings.Contains(err.Error(), "injected crash after every write") {
			t.Fatalf("abort error = %v, want the injected fault surfaced", err)
		}
		// All-or-nothing: with every statement already applied inside the
		// transaction and then aborted, the stored world is byte-identical
		// to the pre-run snapshot -- full rows, every column, no mask.
		if post := f.snapshotAll(t, false); post != pre {
			t.Fatalf("post-abort witness diverged from the pre-run world:\npre:\n%s\npost:\n%s", pre, post)
		}
		// Post-crash convergence: a clean retry commits the whole thing.
		psClean := f.newPublishStore(t)
		f.publishOK(t, psClean, receipt, revision, release, 0)
		var legs int
		if err := f.iso.QueryRow(f.ctx, `
SELECT (SELECT count(*) FROM `+f.ctlTable(ReceiptsTable)+` WHERE project = $1)
     + (SELECT count(*) FROM `+f.ctlTable(RevisionsTable)+` WHERE project = $1 AND status = 'ACTIVE')
     + (SELECT count(*) FROM `+f.ctlTable(OwnerTable)+` WHERE project = $1 AND active_revision_id = $2)
     + (SELECT count(*) FROM `+f.lanesTable("active_project_release")+` WHERE project = $1 AND revision = 1)`,
			project, revision.ID).Scan(&legs); err != nil {
			f.fail("retry four-leg read", err)
		}
		if legs != 4 {
			t.Fatalf("post-crash retry four-leg count = %d, want 4", legs)
		}
	})
}

// stopPublishAtPreCommit builds a fixture with a dedicated-backend publish
// store whose transaction parks at the pre-commit seam until the test
// releases it, plus an independent observer backend.
func stopPublishAtPreCommit(t *testing.T) (
	*pubAuthFixture, *PublishStore, int32, *pgxpool.Pool, int32, chan error, chan int32,
) {
	t.Helper()
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	ps, writerPID := f.newDedicatedPublishStore(t)
	observer, observerPID := f.newDedicatedPool(t)
	reached := make(chan int32, 1)
	releaseGate := make(chan error, 1)
	ps.crashAt = func(ctx context.Context, tx pgx.Tx, step string) error {
		if step != "pre-commit" {
			return nil
		}
		var pid int32
		if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return fmt.Errorf("witness writer backend: %w", err)
		}
		reached <- pid
		return <-releaseGate
	}
	return f, ps, writerPID, observer, observerPID, releaseGate, reached
}

// ---------------------------------------------------------------------------
// T3: crash-window injection at EVERY commit seam is all-or-nothing, with
// post-crash convergence to the clean-run state.
// ---------------------------------------------------------------------------

func TestPublishAuthoringCrashWindowAtEverySeam(t *testing.T) {
	// The second publish of a project (owner already active on the first
	// revision) walks all seven legs, so every seam is reachable.
	steps := []string{
		"receipt-insert", "revision-insert", "release-pointer",
		"prior-supersede", "candidate-activate", "owner-activate", "pre-commit",
	}
	for _, crashStep := range steps {
		t.Run(crashStep, func(t *testing.T) {
			// The twins are the same logical world in two independent
			// databases, so every stored value (content-addressed ids
			// included) must agree byte-for-byte once the crashed twin
			// converges.
			project, explorerID := "proj_pubauth_twin", "exp_twin"
			fg := newPGPubAuthFixture(t)
			fg.migrate(t)
			storeG, psG := fg.newStore(t), fg.newPublishStore(t)
			fc := newPGPubAuthFixture(t)
			fc.migrate(t)
			storeC, psC := fc.newStore(t), fc.newPublishStore(t)

			// Phase 1: publish the first revision on both twins (no seam).
			receipt1, revision1, release1G :=
				fg.seedPublishedWorld(t, storeG, project, explorerID, "twin1")
			_, _, release1C := fc.seedPublishedWorld(t, storeC, project, explorerID, "twin1")
			fg.publishOK(t, psG, receipt1, revision1, release1G, 0)
			fc.publishOK(t, psC, receipt1, revision1, release1C, 0)

			// Phase 2 inputs: the second revision (a prior to supersede).
			// Built per-fixture (each saves its own candidate row); the
			// inputs are identical because every identity is derived from
			// the shared project/tag.
			receipt2G, revision2G, release2G :=
				secondPublishInputsSameExplorer(t, fg, project, explorerID, "twin2")
			receipt2C, revision2C, release2C :=
				secondPublishInputsSameExplorer(t, fc, project, explorerID, "twin2")
			pre := fc.snapshotAll(t, false)

			// Crashed run on the crashed twin: inject at the step under
			// test.
			psCrash := fc.newPublishStore(t)
			var recorded []string
			psCrash.crashAt = func(ctx context.Context, tx pgx.Tx, step string) error {
				recorded = append(recorded, step)
				if step == crashStep {
					return errors.New("injected crash at " + step)
				}
				return nil
			}
			_, err := psCrash.PublishAuthoring(fc.ctx, receipt2C, revision2C, release2C, 1)
			if err == nil || !strings.Contains(err.Error(), "injected crash at "+crashStep) {
				t.Fatalf("crash at %s: error = %v, want the injected fault surfaced", crashStep, err)
			}
			// Non-vacuous seam: the transaction really walked the legs up
			// to and including the crashed step, in contract order.
			want := stepsThrough(steps, crashStep)
			if strings.Join(recorded, ",") != strings.Join(want, ",") {
				t.Fatalf("crash at %s: recorded seam steps = %v, want %v", crashStep, recorded, want)
			}
			// All-or-nothing: the stored world is byte-identical to before
			// the crashed publish -- full rows, every column, no mask.
			if post := fc.snapshotAll(t, false); post != pre {
				t.Fatalf("crash at %s left visible state:\npre:\n%s\npost:\n%s", crashStep, pre, post)
			}

			// Post-crash convergence: the golden twin runs the same second
			// publish uninterrupted, and the crashed twin then retries
			// cleanly; their project-scoped states must agree byte-for-byte
			// apart from the clock-derived columns.
			fg.publishOK(t, psG, receipt2G, revision2G, release2G, 1)
			fc.publishOK(t, fc.newPublishStore(t), receipt2C, revision2C, release2C, 1)
			golden := fg.snapshotForProject(t, project)
			recovered := fc.snapshotForProject(t, project)
			if golden != recovered {
				t.Fatalf("crash at %s: recovered run diverged from the clean golden:\ngolden:\n%s\nrecovered:\n%s",
					crashStep, golden, recovered)
			}
		})
	}
}

// stepsThrough returns the seam steps a second publish records up to and
// including crashStep. The second publish always walks all seven legs, so
// the prefix order is exactly the declared step order.
func stepsThrough(steps []string, crashStep string) []string {
	for i, s := range steps {
		if s == crashStep {
			return steps[:i+1]
		}
	}
	panic("unknown crash step " + crashStep)
}

// snapshotForProject renders the publish tables scoped to one project,
// clock-masked -- the cross-run golden comparison form.
func (f *pubAuthFixture) snapshotForProject(t *testing.T, project string) string {
	t.Helper()
	specs := []struct {
		table, where string
		args         []any
		mask         []string
	}{
		{f.ctlTable(ReceiptsTable), "project = $1", []any{project}, nil},
		{f.ctlTable(RevisionsTable), "project = $1", []any{project}, maskAll},
		{f.ctlTable(OwnerTable), "project = $1", []any{project}, maskAll},
		{f.lanesTable("project_release"), "project = $1", []any{project}, nil},
		{f.lanesTable("active_project_release"), "project = $1", []any{project}, maskPointer},
		{f.lanesTable("dataset_generation_manifest"), "project = $1", []any{project}, maskManifest},
		{f.lanesTable("dataset_active_generation"), "project = $1", []any{project}, maskPointer},
	}
	var b strings.Builder
	for _, s := range specs {
		out := f.snapshotWhere(t, s.table, s.where, s.args, s.mask...)
		label := strings.NewReplacer(f.ctlSchema, "<ctl>", f.lanesSchema, "<lanes>").Replace(s.table)
		// Cross-run comparison tolerates only the execution clock: the
		// activation/supersede timestamps carried inside the stored
		// publication documents. Every other byte must agree.
		out = pubauthClockRe.ReplaceAllString(out, `"updatedAt":"<clock>"`)
		fmt.Fprintf(&b, "%s|%s\x1d\n", label, out)
	}
	return b.String()
}

// pubauthClockRe matches the run-derived updatedAt stamps inside stored
// publication jsonb (RFC 3339 with nanoseconds, UTC).
var pubauthClockRe = regexp.MustCompile(`"updatedAt": ?"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z"`)

// ---------------------------------------------------------------------------
// T4: idempotent replay is zero-write (full-row byte identity), and every
// replay guard is an exact-payload guard.
// ---------------------------------------------------------------------------

func TestPublishAuthoringIdempotentReplayIsZeroWrite(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	ps := f.newPublishStore(t)
	project, explorerID, tag := "proj_pubauth_replay", "exp_replay", "replay"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)

	f.publishOK(t, ps, receipt, revision, release, 0)
	first := f.snapshotAll(t, false)

	// Exact same inputs: converged success, byte-identical rows everywhere
	// INCLUDING clock columns -- the zero-write proof.
	out := f.publishOK(t, ps, receipt, revision, release, 0)
	if out.Status != explorer.RevisionActive || out.ID != revision.ID {
		t.Fatalf("replay returned %q/%q, want the stored ACTIVE revision", out.ID, out.Status)
	}
	if post := f.snapshotAll(t, false); post != first {
		t.Fatalf("identical replay wrote rows:\nfirst:\n%s\nreplay:\n%s", first, post)
	}

	// F-B15 precedence: the expected release revision is NOT compared
	// against a pointer that already names the candidate.
	f.publishOK(t, ps, receipt, revision, release, 999)
	if post := f.snapshotAll(t, false); post != first {
		t.Fatal("already-active replay with a stale expected revision must still be zero-write")
	}

	// RequestID/CreatedAt are excluded from the receipt content identity
	// by design: replaying with a new RequestID is still the same content.
	sameContent := receipt
	sameContent.RequestID = "req_replay_other"
	f.publishOK(t, ps, sameContent, revision, release, 0)
	if post := f.snapshotAll(t, false); post != first {
		t.Fatal("digest-equivalent receipt replay (RequestID only) must be zero-write")
	}

	// Exact-payload guard, revision leg: same id, different frozen content
	// -> ErrImmutableRevision, stored row byte-identical, everything else
	// byte-identical (the whole transaction rolled back).
	mutatedRevision := revision
	mutatedRevision.IntentDigest = "sha256:mutated"
	_, err := ps.PublishAuthoring(f.ctx, receipt, mutatedRevision, release, 0)
	if !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("mutated revision replay error = %v, want explorer.ErrImmutableRevision", err)
	}
	requireErrContainsPubAuth(t, err, "reused with different content", "mutated revision replay")
	if post := f.snapshotAll(t, false); post != first {
		t.Fatal("the refused revision mutation touched the stored world")
	}

	// Exact-payload guard, release leg: same candidate id, different
	// content -> typed activation conflict, pointer byte-identical.
	mutatedRelease := release
	mutatedRelease.GitCommit = "commit-mutated"
	_, err = ps.PublishAuthoring(f.ctx, receipt, revision, mutatedRelease, 0)
	if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
		t.Fatalf("mutated release replay error = %v, want dataset.ErrReleaseActivationConflict", err)
	}
	requireErrContainsPubAuth(t, err, "no longer matches the persisted document", "mutated release replay")
	if post := f.snapshotAll(t, false); post != first {
		t.Fatal("the refused release mutation touched the stored world")
	}

	// Cross-tenant revision-id reuse: same id, other project -> different
	// frozen content -> the same typed refusal (revision ids are globally
	// unique).
	_, err = ps.PublishAuthoring(f.ctx,
		pubauthReceipt(t, "proj_pubauth_other", "exp_replay", "replay_other"),
		pubauthRevision(revision.ID, "proj_pubauth_other", "exp_replay", "receipt_other", "replay_other"),
		pubauthRelease("proj_pubauth_other", pubauthGeneration, "rel_other"), 0)
	if !errors.Is(err, explorer.ErrImmutableRevision) {
		t.Fatalf("cross-tenant revision reuse error = %v, want explorer.ErrImmutableRevision", err)
	}

	// Exact-payload guard, receipt leg: same id, different canonical
	// content -> ErrCorruptReceipt, stored rows byte-identical. The claim
	// is manual-ID, contract-less -- the one shape the store accepts for
	// a hand-claimed id (the content-addressed shape makes id reuse
	// arithmetically impossible) -- so the publish reuse guard is what
	// refuses. This probe lives on its own project so claiming ids there
	// cannot perturb the converged world asserted above.
	reuseProject := "proj_pubauth_reuse"
	_, _, reuseRelease := f.seedPublishedWorld(t, store, reuseProject, "exp_reuse", "reuse")
	manualID := "receipt_pubauth_reuse"
	firstClaim := pubauthManualReceipt(t, reuseProject, "exp_reuse", "reuse1", manualID)
	f.publishOK(t, ps, firstClaim,
		pubauthRevision("rev_pubauth_reuse1", reuseProject, "exp_reuse", manualID, "reuse1"), reuseRelease, 0)
	reuseBase := f.snapshotAll(t, false)

	reuseClaim := pubauthManualReceipt(t, reuseProject, "exp_reuse", "reuse2", manualID)
	_, err = ps.PublishAuthoring(f.ctx, reuseClaim,
		pubauthRevision("rev_pubauth_reuse2", reuseProject, "exp_reuse", manualID, "reuse2"), reuseRelease, 0)
	if !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("reused receipt id error = %v, want explorer.ErrCorruptReceipt", err)
	}
	requireErrContainsPubAuth(t, err, "reused with different content", "reused receipt id")
	// Parity with the standalone insert on the identical claim.
	if _, insertErr := store.InsertCompilationReceipt(f.ctx, reuseClaim); insertErr == nil ||
		!errors.Is(insertErr, explorer.ErrCorruptReceipt) ||
		!strings.Contains(insertErr.Error(), "reused with different content") {
		t.Fatalf("standalone insert on the same reused id = %v, want the same typed refusal", insertErr)
	}
	if post := f.snapshotAll(t, false); post != reuseBase {
		t.Fatal("the refused receipt-id reuse touched the stored world")
	}
}

// ---------------------------------------------------------------------------
// T5: the verified receipt leg, tenant-scoped.
// ---------------------------------------------------------------------------

func TestPublishAuthoringVerifiedReceiptLeg(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	ps := f.newPublishStore(t)

	// A corrupt input receipt is refused before the transaction with the
	// same typed error the standalone insert gives: blank id.
	if _, err := ps.PublishAuthoring(f.ctx, explorer.CompilationReceipt{ID: "  "},
		pubauthRevision("rev_x", "p", "e", "r", "x"), pubauthRelease("p", "g", "r"), 0); !errors.Is(err, explorer.ErrCorruptReceipt) {
		t.Fatalf("blank-id receipt error = %v, want explorer.ErrCorruptReceipt", err)
	}

	// Tenant isolation: identical receipt content claimed under two
	// tenants is two independent rows -- identity is
	// (project, explorer_id, id), so the content-derived id differs with
	// the tenant and neither tenant's claim touches the other.
	projectA, projectB := "proj_pubauth_tenantA", "proj_pubauth_tenantB"
	for _, project := range []string{projectA, projectB} {
		if _, err := store.Create(f.ctx, pubauthOwner(project, "exp_tenant", explorer.ManagementInteractive)); err != nil {
			f.fail("create tenant owner "+project, err)
		}
	}
	receiptA := pubauthReceipt(t, projectA, "exp_tenant", "shared")
	revisionA := pubauthRevision("rev_tenant_a", projectA, "exp_tenant", receiptA.ID, "shared")
	releaseA := seedReleaseLaneOnly(t, f, projectA, "shared")
	f.publishOK(t, ps, receiptA, revisionA, releaseA, 0)

	receiptB := pubauthReceipt(t, projectB, "exp_tenant", "shared")
	if receiptB.ID == receiptA.ID {
		t.Fatal("content-addressed ids must differ across tenants (the tenant frame is inside the digest)")
	}
	revisionB := pubauthRevision("rev_tenant_b", projectB, "exp_tenant", receiptB.ID, "shared")
	releaseB := seedReleaseLaneOnly(t, f, projectB, "shared")
	f.publishOK(t, ps, receiptB, revisionB, releaseB, 0)

	if got := f.countRows(t, f.ctlTable(ReceiptsTable), "project = $1", projectA); got != 1 {
		t.Fatalf("tenant A receipt rows = %d, want 1", got)
	}
	if got := f.countRows(t, f.ctlTable(ReceiptsTable), "project = $1", projectB); got != 1 {
		t.Fatalf("tenant B receipt rows = %d, want 1", got)
	}

	// Wrong-name probes: each tenant's receipt id read under the other
	// tenant reports the shared not-found, with no information either way.
	if _, err := store.GetCompilationReceiptForExplorer(f.ctx, projectA, "exp_tenant", receiptB.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-tenant receipt read error = %v, want explorer.ErrNotFound", err)
	}
	if _, err := store.GetCompilationReceiptForExplorer(f.ctx, projectB, "exp_tenant", receiptA.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("reverse cross-tenant receipt read error = %v, want explorer.ErrNotFound", err)
	}
}

// seedReleaseLaneOnly creates the generation manifest, activates it, and
// saves the release candidate for a project whose owner already exists.
func seedReleaseLaneOnly(t *testing.T, f *pubAuthFixture, project, tag string) dataset.ProjectRelease {
	t.Helper()
	gen := f.newGenerationStore(t)
	stageAndActivate(t, f, gen, project, pubauthGeneration, nil)
	release := pubauthRelease(project, pubauthGeneration, "rel_"+project+"_"+tag)
	if _, err := f.newReleaseStore(t).SaveRelease(f.ctx, release); err != nil {
		f.fail("save release "+release.ID, err)
	}
	return release
}

// seedLanesOnly seeds a project WITHOUT the owner row: owner missing.
func seedLanesOnly(t *testing.T, f *pubAuthFixture, project, explorerID string,
) (explorer.CompilationReceipt, explorer.Revision, dataset.ProjectRelease) {
	t.Helper()
	release := seedReleaseLaneOnly(t, f, project, "lanes")
	receipt := pubauthReceipt(t, project, explorerID, "lanes")
	revision := pubauthRevision("rev_"+project+"_lanes", project, explorerID, receipt.ID, "lanes")
	return receipt, revision, release
}

// seedLanesOnlyForExistingOwner seeds generation+release for a project
// whose owner row already exists.
func seedLanesOnlyForExistingOwner(t *testing.T, f *pubAuthFixture, project, explorerID string,
) (explorer.CompilationReceipt, explorer.Revision, dataset.ProjectRelease) {
	t.Helper()
	return seedLanesOnly(t, f, project, explorerID)
}

// ---------------------------------------------------------------------------
// T6: the immutable revision leg refuses id reuse under mutation, leaving
// the entire stored world byte-identical.
// ---------------------------------------------------------------------------

func TestPublishAuthoringImmutableRevisionLeg(t *testing.T) {
	f := newPGPubAuthFixture(t)
	f.migrate(t)
	store := f.newStore(t)
	ps := f.newPublishStore(t)
	project, explorerID, tag := "proj_pubauth_immut", "exp_immut", "immut"
	receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
	f.publishOK(t, ps, receipt, revision, release, 0)
	before := f.snapshotAll(t, false)

	for name, mutate := range map[string]func(revision explorer.Revision) explorer.Revision{
		"config":     func(r explorer.Revision) explorer.Revision { r.Config = jsonRaw(`{"changed":true}`); return r },
		"recipeName": func(r explorer.Revision) explorer.Revision { r.Recipe.Name = "mutated"; return r },
		"emittedColumn": func(r explorer.Revision) explorer.Revision {
			// Copy the slice before writing: the range closure receives the
			// revision by value, so in-place element writes would alias
			// (and silently mutate) the original fixture revision.
			cols := append([]explorer.EmittedColumn(nil), r.EmittedColumns...)
			cols[0].EmissionID = "emit_mutated"
			r.EmittedColumns = cols
			return r
		},
		"datasetMetadata": func(r explorer.Revision) explorer.Revision {
			r.Dataset.SchemaDigest = "sha256:mutated"
			return r
		},
	} {
		mutated := mutate(revision)
		if digest, _ := revisionContentDigest(mutated); digest == func() string { d, _ := revisionContentDigest(revision); return d }() {
			t.Fatalf("mutation %q did not move the content digest -- probe is vacuous", name)
		}
		_, err := ps.PublishAuthoring(f.ctx, receipt, mutated, release, 0)
		if !errors.Is(err, explorer.ErrImmutableRevision) {
			t.Fatalf("mutation %q: error = %v, want explorer.ErrImmutableRevision", name, err)
		}
		if post := f.snapshotAll(t, false); post != before {
			t.Fatalf("mutation %q: the refused publish touched the stored world", name)
		}
	}
}

// ---------------------------------------------------------------------------
// T7: the release activation CAS, all publish-path refusals, each with
// cross-lane rollback (the receipt/revision legs vanish with the loss).
// ---------------------------------------------------------------------------

func TestPublishAuthoringReleaseCASRefusalsRollBackEverything(t *testing.T) {
	// Each case gets its own project; the whole published world is seeded
	// through the owning lanes first, then mutated at the refusal point.
	cases := []struct {
		name   string
		refuse func(t *testing.T, f *pubAuthFixture, project string)
		wants  []string // substrings of the typed refusal
	}{
		{
			name: "candidate-never-persisted",
			refuse: func(t *testing.T, f *pubAuthFixture, project string) {
				f.execRaw(t, "DELETE FROM "+f.lanesTable("project_release")+" WHERE project = $1", project)
			},
			wants: []string{"is not a persisted project release"},
		},
		{
			name: "manifest-missing",
			refuse: func(t *testing.T, f *pubAuthFixture, project string) {
				f.execRaw(t, "DELETE FROM "+f.lanesTable("dataset_active_generation")+" WHERE project = $1", project)
				f.execRaw(t, "DELETE FROM "+f.lanesTable("dataset_generation_manifest")+" WHERE project = $1", project)
			},
			wants: []string{"has no persisted manifest"},
		},
		{
			name: "manifest-loading",
			refuse: func(t *testing.T, f *pubAuthFixture, project string) {
				f.execRaw(t, "UPDATE "+f.lanesTable("dataset_generation_manifest")+
					" SET state = 'LOADING' WHERE project = $1", project)
			},
			wants: []string{"manifest is LOADING, not STAGED or READY"},
		},
		{
			name: "no-active-generation",
			refuse: func(t *testing.T, f *pubAuthFixture, project string) {
				f.execRaw(t, "DELETE FROM "+f.lanesTable("dataset_active_generation")+" WHERE project = $1", project)
			},
			wants: []string{"has no active generation"},
		},
		{
			name: "pointer-moves",
			refuse: func(t *testing.T, f *pubAuthFixture, project string) {
				// The pointer legally moves to a second generation through
				// the owning lane's CAS; the candidate still names the
				// first generation.
				replaced, err := dataset.NewRef(project, "gen_pubauth_01")
				if err != nil {
					f.fail("expected ref", err)
				}
				stageAndActivate(t, f, f.newGenerationStore(t), project, "gen_pubauth_02", &replaced)
			},
			wants: []string{"not the candidate generation"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPGPubAuthFixture(t)
			f.migrate(t)
			store := f.newStore(t)
			ps := f.newPublishStore(t)
			project, explorerID, tag := "proj_pubauth_"+strings.ReplaceAll(tc.name, "-", "_"), "exp_cas", "cas"
			receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
			tc.refuse(t, f, project)
			pre := f.snapshotAll(t, false)
			_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
			if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
				t.Fatalf("%s: error = %v, want dataset.ErrReleaseActivationConflict", tc.name, err)
			}
			for _, want := range tc.wants {
				requireErrContainsPubAuth(t, err, want, tc.name)
			}
			// Cross-lane all-or-nothing: the receipt and revision legs the
			// SAME transaction wrote are gone, the owner never activated.
			if got := f.countRows(t, f.ctlTable(ReceiptsTable), "project = $1", project); got != 0 {
				t.Fatalf("%s: %d receipt rows survived the CAS loss, want 0", tc.name, got)
			}
			if got := f.countRows(t, f.ctlTable(RevisionsTable), "project = $1", project); got != 0 {
				t.Fatalf("%s: %d revision rows survived the CAS loss, want 0", tc.name, got)
			}
			var ownerActive string
			if err := f.iso.QueryRow(f.ctx, "SELECT active_revision_id FROM "+f.ctlTable(OwnerTable)+
				" WHERE project = $1", project).Scan(&ownerActive); err != nil {
				f.fail("read owner", err)
			}
			if ownerActive != "" {
				t.Fatalf("%s: owner activated %q despite the CAS loss", tc.name, ownerActive)
			}
			if post := f.snapshotAll(t, false); post != pre {
				t.Fatalf("%s: the refused activation changed the stored world", tc.name)
			}
		})
	}

	t.Run("stale-expected-revision-conflicts-and-converges-on-retry", func(t *testing.T) {
		f := newPGPubAuthFixture(t)
		f.migrate(t)
		store := f.newStore(t)
		ps := f.newPublishStore(t)
		project, explorerID := "proj_pubauth_stale", "exp_stale"
		receipt1, revision1, release1 := f.seedPublishedWorld(t, store, project, explorerID, "stale1")
		f.publishOK(t, ps, receipt1, revision1, release1, 0) // pointer revision 1
		// A second candidate with a stale expected revision must conflict
		// entirely: no receipt, no revision, no activation, pointer intact.
		receipt2, revision2, release2 := secondPublishInputsSameExplorer(t, f, project, explorerID, "stale2")
		pointerBefore := f.snapshot(t, f.lanesTable("active_project_release"), maskPointer...)
		_, err := ps.PublishAuthoring(f.ctx, receipt2, revision2, release2, 0)
		if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
			t.Fatalf("stale expected: error = %v, want dataset.ErrReleaseActivationConflict", err)
		}
		requireErrContainsPubAuth(t, err, "expected active revision 0, found 1", "stale expected")
		if got := f.countRows(t, f.ctlTable(RevisionsTable), "id = $1", revision2.ID); got != 0 {
			t.Fatalf("stale expected: loser revision row survived the rollback")
		}
		if got := f.snapshot(t, f.lanesTable("active_project_release"), maskPointer...); got != pointerBefore {
			t.Fatal("stale expected: the pointer moved for the loser")
		}
		// Post-abort convergence: the loser retries with the true expected
		// revision and fully commits.
		f.publishOK(t, ps, receipt2, revision2, release2, 1)
		var ownerActive, pointerRelease string
		var pointerRevision int64
		if err := f.iso.QueryRow(f.ctx, "SELECT e.active_revision_id, p.release_id, p.revision FROM "+
			f.ctlTable(OwnerTable)+" e, "+f.lanesTable("active_project_release")+
			" p WHERE e.project = $1 AND p.project = $1", project).
			Scan(&ownerActive, &pointerRelease, &pointerRevision); err != nil {
			f.fail("read converged state", err)
		}
		if ownerActive != revision2.ID || pointerRelease != release2.ID || pointerRevision != 2 {
			t.Fatalf("converged = %s/%s@%d, want %s/%s@2", ownerActive, pointerRelease, pointerRevision,
				revision2.ID, release2.ID)
		}
		// And the superseded prior is frozen SUPERSEDED, never resurrected.
		var priorStatus string
		if err := f.iso.QueryRow(f.ctx, "SELECT status FROM "+f.ctlTable(RevisionsTable)+" WHERE id = $1",
			revision1.ID).Scan(&priorStatus); err != nil {
			f.fail("read prior status", err)
		}
		if priorStatus != "SUPERSEDED" {
			t.Fatalf("prior status = %s, want SUPERSEDED", priorStatus)
		}
	})

	t.Run("initial-pointer-requires-expected-zero", func(t *testing.T) {
		f := newPGPubAuthFixture(t)
		f.migrate(t)
		store := f.newStore(t)
		ps := f.newPublishStore(t)
		project, explorerID, tag := "proj_pubauth_initial", "exp_initial", "initial"
		receipt, revision, release := f.seedPublishedWorld(t, store, project, explorerID, tag)
		_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 1)
		if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
			t.Fatalf("initial expected=1: error = %v, want dataset.ErrReleaseActivationConflict", err)
		}
		requireErrContainsPubAuth(t, err, "found no active release", "initial expected=1")
		if got := f.countRows(t, f.ctlTable(RevisionsTable), "project = $1", project); got != 0 {
			t.Fatalf("initial expected=1: %d revision rows survived, want 0", got)
		}
	})

	t.Run("f-b13-retained-generation-conflicts-while-another-generation-is-active", func(t *testing.T) {
		// The cross_generation_publish_e2e baseline shape: a retained G1
		// candidate cannot publish while G2 owns the active-generation
		// pointer -- the publish-path current-generation guard.
		f := newPGPubAuthFixture(t)
		f.migrate(t)
		store := f.newStore(t)
		ps := f.newPublishStore(t)
		project, explorerID := "proj_pubauth_f13", "exp_f13"
		if _, err := store.Create(f.ctx, pubauthOwner(project, explorerID, explorer.ManagementInteractive)); err != nil {
			f.fail("create owner", err)
		}
		gen := f.newGenerationStore(t)
		stageAndActivate(t, f, gen, project, "gen_pubauth_01", nil)
		// G1 release saved while G1 is active; then G2 takes the pointer
		// through the owning lane's CAS (expected names G1).
		releaseG1 := pubauthRelease(project, "gen_pubauth_01", "rel_f13_g1")
		if _, err := f.newReleaseStore(t).SaveRelease(f.ctx, releaseG1); err != nil {
			f.fail("save G1 candidate", err)
		}
		replaced, err := dataset.NewRef(project, "gen_pubauth_01")
		if err != nil {
			f.fail("expected ref", err)
		}
		stageAndActivate(t, f, gen, project, "gen_pubauth_02", &replaced)
		receiptG1 := pubauthReceipt(t, project, explorerID, "f13_g1")
		revisionG1 := pubauthRevision("rev_f13_g1", project, explorerID, receiptG1.ID, "f13_g1")
		_, err = ps.PublishAuthoring(f.ctx, receiptG1, revisionG1, releaseG1, 0)
		if !errors.Is(err, dataset.ErrReleaseActivationConflict) {
			t.Fatalf("retained G1 publish: error = %v, want dataset.ErrReleaseActivationConflict", err)
		}
		requireErrContainsPubAuth(t, err, "active generation of", "retained G1 publish")
		requireErrContainsPubAuth(t, err, "not the candidate generation gen_pubauth_01", "retained G1 publish")
		if got := f.countRows(t, f.ctlTable(RevisionsTable), "project = $1", project); got != 0 {
			t.Fatalf("retained G1 publish wrote %d revision rows, want 0", got)
		}
	})
}

func secondPublishInputsSameExplorer(t *testing.T, f *pubAuthFixture, project, explorerID, tag string,
) (explorer.CompilationReceipt, explorer.Revision, dataset.ProjectRelease) {
	t.Helper()
	release := pubauthRelease(project, pubauthGeneration, "rel_"+project+"_"+tag)
	if _, err := f.newReleaseStore(t).SaveRelease(f.ctx, release); err != nil {
		f.fail("save second candidate", err)
	}
	receipt := pubauthReceipt(t, project, explorerID, tag)
	revision := pubauthRevision("rev_"+project+"_"+tag, project, explorerID, receipt.ID, tag)
	return receipt, revision, release
}

// stageAndActivate creates, stages, and activates one generation through
// the owning lane's production CAS: expected nil means "no active
// generation", a pointer names the generation being replaced.
func stageAndActivate(t *testing.T, f *pubAuthFixture, gen *datasetpostgres.Store, project, generation string, expected *dataset.Ref) {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		f.fail("ref", err)
	}
	snapshot, err := dataset.NewSchemaSnapshot("schema-pubauth", "4.0.1", pubauthSnapshotSHA256, []string{"Patient"})
	if err != nil {
		f.fail("snapshot", err)
	}
	loading, err := dataset.NewManifest(ref, snapshot)
	if err != nil {
		f.fail("manifest", err)
	}
	if _, err := gen.CreateManifest(f.ctx, loading); err != nil {
		f.fail("create manifest", err)
	}
	staged, err := gen.TransitionManifest(f.ctx, loading, dataset.StateStaged)
	if err != nil {
		f.fail("stage manifest", err)
	}
	if err := gen.ActivateGeneration(f.ctx, staged, expected); err != nil {
		f.fail("activate generation", err)
	}
}

// ---------------------------------------------------------------------------
// T8: the activation guard: owner/mode/candidate status, fail-closed prior.
// ---------------------------------------------------------------------------

func TestPublishAuthoringActivationGuardRefusals(t *testing.T) {
	t.Run("missing-owner-and-mode-mismatch-are-indistinguishable", func(t *testing.T) {
		f := newPGPubAuthFixture(t)
		f.migrate(t)
		store := f.newStore(t)
		ps := f.newPublishStore(t)

		// No owner at all.
		receipt, revision, release := seedLanesOnly(t, f, "proj_pubauth_noowner", "exp_none")
		_, err := ps.PublishAuthoring(f.ctx, receipt, revision, release, 0)
		if !errors.Is(err, explorer.ErrDraftConflict) {
			t.Fatalf("missing owner: error = %v, want explorer.ErrDraftConflict", err)
		}

		// Foreign management mode for a non-default Explorer.
		wrongModeProject := "proj_pubauth_wrongmode"
		if _, err := store.Create(f.ctx, pubauthOwner(wrongModeProject, "exp_wrong", explorer.ManagementRepository)); err != nil {
			f.fail("create repository-mode owner", err)
		}
		receipt2, revision2, release2 := seedLanesOnlyForExistingOwner(t, f, wrongModeProject, "exp_wrong")
		_, err2 := ps.PublishAuthoring(f.ctx, receipt2, revision2, release2, 0)
		if !errors.Is(err2, explorer.ErrDraftConflict) {
			t.Fatalf("mode mismatch: error = %v, want explorer.ErrDraftConflict", err2)
		}
		if err.Error() != err2.Error() {
			t.Fatalf("guard causes must be indistinguishable: %q vs %q", err, err2)
		}

		// The "default" Explorer must be REPOSITORY, never INTERACTIVE.
		defaultProject := "proj_pubauth_default"
		if _, err := store.Create(f.ctx, pubauthOwner(defaultProject, "default", explorer.ManagementInteractive)); err != nil {
			f.fail("create interactive default owner", err)
		}
		receipt3, revision3, release3 := seedLanesOnlyForExistingOwner(t, f, defaultProject, "default")
		_, err3 := ps.PublishAuthoring(f.ctx, receipt3, revision3, release3, 0)
		if !errors.Is(err3, explorer.ErrDraftConflict) {
			t.Fatalf("default Explorer with INTERACTIVE mode: error = %v, want explorer.ErrDraftConflict", err3)
		}

		// And the REPOSITORY spelling must then succeed.
		f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET management_mode = 'REPOSITORY' WHERE project = $1", defaultProject)
		f.publishOK(t, ps, receipt3, revision3, release3, 0)
	})

	t.Run("superseded-or-failed-candidate-is-a-guard-miss", func(t *testing.T) {
		f := newPGPubAuthFixture(t)
		f.migrate(t)
		store := f.newStore(t)
		ps := f.newPublishStore(t)
		project, explorerID := "proj_pubauth_guard2", "exp_guard2"
		receipt1, revision1, release1 := f.seedPublishedWorld(t, store, project, explorerID, "g2a")
		f.publishOK(t, ps, receipt1, revision1, release1, 0)
		receipt2, revision2, release2 := secondPublishInputsSameExplorer(t, f, project, explorerID, "g2b")
		f.publishOK(t, ps, receipt2, revision2, release2, 1) // rev1 now SUPERSEDED

		// Republish the superseded revision: guard miss (status is neither
		// READY nor ACTIVE-under-owner), zero writes.
		before := f.snapshotAll(t, false)
		_, err := ps.PublishAuthoring(f.ctx, receipt1, revision1, release1, 2)
		if !errors.Is(err, explorer.ErrDraftConflict) {
			t.Fatalf("superseded candidate: error = %v, want explorer.ErrDraftConflict", err)
		}
		if post := f.snapshotAll(t, false); post != before {
			t.Fatal("superseded candidate republish touched the stored world")
		}

		// A FAILED candidate (same id/content) is refused the same way.
		if _, err := store.FailRevision(f.ctx, revision2.ID, []explorer.Diagnostic{{Severity: "ERROR", Code: "X"}}); err != nil {
			f.fail("fail revision2", err)
		}
		before = f.snapshotAll(t, false)
		_, err = ps.PublishAuthoring(f.ctx, receipt2, revision2, release2, 2)
		if !errors.Is(err, explorer.ErrDraftConflict) {
			t.Fatalf("failed candidate: error = %v, want explorer.ErrDraftConflict", err)
		}
		if post := f.snapshotAll(t, false); post != before {
			t.Fatal("failed candidate republish touched the stored world")
		}
	})

	t.Run("fail-closed-owner-pointer", func(t *testing.T) {
		f := newPGPubAuthFixture(t)
		f.migrate(t)
		store := f.newStore(t)
		ps := f.newPublishStore(t)
		project, explorerID := "proj_pubauth_prior", "exp_prior"
		receipt1, revision1, release1 := f.seedPublishedWorld(t, store, project, explorerID, "priorA")
		f.publishOK(t, ps, receipt1, revision1, release1, 0)

		// Dangling pointer: owner.active_revision_id names nothing.
		f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET active_revision_id = 'rev_ghost' WHERE project = $1", project)
		receipt2, revision2, release2 := secondPublishInputsSameExplorer(t, f, project, explorerID, "priorB")
		before := f.snapshotAll(t, false)
		_, err := ps.PublishAuthoring(f.ctx, receipt2, revision2, release2, 1)
		if !errors.Is(err, explorer.ErrDraftConflict) {
			t.Fatalf("dangling prior: error = %v, want explorer.ErrDraftConflict", err)
		}
		requireErrContainsPubAuth(t, err, "not a stored revision", "dangling prior")
		if post := f.snapshotAll(t, false); post != before {
			t.Fatal("dangling prior refusal touched the stored world")
		}
		// Non-vacuity: restoring the pointer to the real ACTIVE revision
		// lets the same publish succeed -- and its supersede leg freezes
		// revision1 SUPERSEDED for the next probe.
		f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET active_revision_id = $2 WHERE project = $1",
			project, revision1.ID)
		f.publishOK(t, ps, receipt2, revision2, release2, 1)

		// Prior pointing at a non-ACTIVE revision is refused fail-closed.
		f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+" SET active_revision_id = $2 WHERE project = $1",
			project, revision1.ID) // revision1 is SUPERSEDED
		receipt3, revision3, release3 := secondPublishInputsSameExplorer(t, f, project, explorerID, "priorC")
		_, err = ps.PublishAuthoring(f.ctx, receipt3, revision3, release3, 2)
		if !errors.Is(err, explorer.ErrDraftConflict) {
			t.Fatalf("superseded prior: error = %v, want explorer.ErrDraftConflict", err)
		}
		requireErrContainsPubAuth(t, err, "not ACTIVE", "superseded prior")

		// Foreign-scope prior (another Explorer's revision) is refused.
		otherProject := "proj_pubauth_prior_other"
		if _, err := store.Create(f.ctx, pubauthOwner(otherProject, "exp_other", explorer.ManagementInteractive)); err != nil {
			f.fail("create other owner", err)
		}
		f.execRaw(t, "UPDATE "+f.ctlTable(OwnerTable)+
			" SET active_revision_id = $2 WHERE project = $1", otherProject, revision1.ID)
		receipt4, revision4, release4 := seedLanesOnlyForExistingOwner(t, f, otherProject, "exp_other")
		_, err = ps.PublishAuthoring(f.ctx, receipt4, revision4, release4, 0)
		if !errors.Is(err, explorer.ErrDraftConflict) {
			t.Fatalf("foreign prior: error = %v, want explorer.ErrDraftConflict", err)
		}
		requireErrContainsPubAuth(t, err, "foreign to", "foreign prior")
	})
}

func requireErrContainsPubAuth(t *testing.T, err error, want, step string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: error = %v, want it to mention %q", step, err, want)
	}
}
