package postgres

// Real-PostgreSQL 18 acceptance tests for the page stale execution
// reconciliation sweep (pg-publication-read-pins-reconcile-v1; part of
// check-3: go test -count=1 ./internal/dataframe/publication/postgres/).
//
// The promise under test is the reconcile.go contract:
//
//	A. The stable bounded keyset scan drives the whole sweep: every stale
//	   execution in the swept states is visited exactly once in
//	   (updated_at, id) order across fixed pages, and each reclaimed
//	   execution gets its durable terminal record written literally.
//	B. Pin arbitration through PostgreSQL: a live reader pin refuses the
//	   sweep with the exact named publication.ErrExecutionReadPinActive
//	   (first-error-wins, baseline parity), with the claim never minted, the
//	   lease released, and the execution byte-identical; a later sweep after
//	   the reader releases converges.
//	C. Active release/pointer bindings gate the ClickHouse deletion: a
//	   pointer that names the execution (scan-time or post-claim) and an
//	   active-release binding both skip without dropping, the PUBLISHED
//	   janitor arm exists only with a wired release reader, and the
//	   in-transaction pointer veto is non-vacuous (a pointer committed
//	   between the pre-claim read and the claim transaction stops the
//	   claim).
//	D. Two-real-connection racing surfaces (distinct pg_backend_pid,
//	   >=10 rounds each): sweep-vs-publisher on the same execution lease
//	   converges to exactly one actor per round; the claim transaction
//	   serializes against a concurrent pointer move witnessed from a second
//	   backend.
//	E. Crash injection at each durability seam with post-abort convergence:
//	   a failure inside the claim transaction leaves ZERO retention state
//	   (including the anchor) with the bundle lease still held; a failure
//	   between the drop phase and the terminal write leaves the claim
//	   active, the execution pre-sweep, and the drops already applied, from
//	   which a re-sweep converges -- same sweeper immediately, foreign
//	   sweeper once the abandoned claim expires.
//	F. Heartbeat loss stops the deletion: losing the cleanup claim mid-drop
//	   (owner-conditional renewal refused) cancels the drop context, the
//	   lost tables stay listed in remaining outputs, and the final error
//	   joins ErrExecutionReadPinLost.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

var (
	rprStaleStamp = time.Date(2026, 1, 5, 6, 7, 8, 900000, time.UTC)
)

func setReconcileGates(claim func(reconcileClaimAttempt) error, finalize func(reconcileFinalizeAttempt) error) {
	if claim == nil {
		reconcileClaimGate.Store(nil)
	} else {
		reconcileClaimGate.Store(&claim)
	}
	if finalize == nil {
		reconcileFinalizeGate.Store(nil)
	} else {
		reconcileFinalizeGate.Store(&finalize)
	}
}

// rprReleases is a scripted dataset.ActiveReleaseResolver.
type rprReleases struct {
	mu    sync.Mutex
	bound map[string][]string
	err   error
}

func newRPRReleases() *rprReleases {
	return &rprReleases{bound: map[string][]string{}}
}

func (f *rprReleases) bind(project string, executionIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bound[project] = executionIDs
}

func (f *rprReleases) failWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *rprReleases) ReadActiveRelease(_ context.Context, project string) (dataset.ActiveRelease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return dataset.ActiveRelease{}, f.err
	}
	ids := f.bound[project]
	if len(ids) == 0 {
		return dataset.ActiveRelease{}, dataset.ErrNoActiveRelease
	}
	release := dataset.ProjectRelease{}
	for _, id := range ids {
		release.Publications = append(release.Publications, dataset.ReleasePublication{ExecutionID: id})
	}
	return dataset.ActiveRelease{Release: release}, nil
}

// rprDrops records the physical drop calls the sweep made; the hook is the
// only ClickHouse-shaped surface in the sweep and must always run outside
// any PostgreSQL transaction.
type rprDrops struct {
	mu       sync.Mutex
	calls    []string
	perTable map[string]error
	// waitFor, when non-nil, is observed by the hook before each drop; the
	// hook returns its context error when the drop context is canceled
	// first, mirroring how a real ClickHouse call aborts.
	waitFor <-chan struct{}
	hookErr func(table string, ctx context.Context) error
}

func newRPRDrops() *rprDrops {
	return &rprDrops{perTable: map[string]error{}}
}

// liveTables models which physical tables exist, shared across every
// rprDrops instance: a legitimate re-sweep of a FAILED execution may ask to
// drop an already-dead table (DROP ... IF EXISTS is a no-op), and the
// witness must not recount it as a deletion.
var (
	liveTablesMu sync.Mutex
	liveTables   = map[string]bool{}
)

func markLiveTables(tables ...string) {
	liveTablesMu.Lock()
	defer liveTablesMu.Unlock()
	for _, tbl := range tables {
		liveTables[tbl] = true
	}
}

func (d *rprDrops) hook(ctx context.Context, table string) error {
	if d.waitFor != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.waitFor:
		}
	}
	liveTablesMu.Lock()
	existed := liveTables[table]
	if existed {
		delete(liveTables, table)
	}
	liveTablesMu.Unlock()
	if !existed {
		return nil // DROP IF EXISTS on an already-dead table: no witness
	}
	if d.hookErr != nil {
		if err := d.hookErr(table, ctx); err != nil {
			liveTablesMu.Lock()
			liveTables[table] = true
			liveTablesMu.Unlock()
			d.record(table, err)
			return err
		}
	}
	d.record(table, nil)
	return nil
}

func (d *rprDrops) record(table string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err == nil {
		d.calls = append(d.calls, table)
	} else {
		d.calls = append(d.calls, table+"!error")
	}
}

func (d *rprDrops) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

// ---------------------------------------------------------------------------
// Seeding helpers built on the production Registry surface.
// ---------------------------------------------------------------------------

func rprExecution(id, project, name string, state publication.BundleState, outputs int) publication.BundleExecution {
	execution := publication.BundleExecution{
		ID:  id,
		Key: "key-" + id,
		BundleIdentity: publication.BundleIdentity{
			Name: name, Project: project, DatasetGeneration: "gen-1",
			RecipeDigest: "r-" + id, SchemaDigest: "s-" + id, ScopeDigest: "sc-" + id, EngineVersion: "v1",
		},
		State:     state,
		CreatedAt: rprStaleStamp,
		UpdatedAt: rprStaleStamp,
	}
	for i := 0; i < outputs; i++ {
		execution.Outputs = append(execution.Outputs, publication.BundleOutputRecord{
			Name: fmt.Sprintf("out-%d", i), PhysicalTable: fmt.Sprintf("tbl-%s-%d", id, i), State: state,
		})
	}
	return execution
}

// seedStaleExecution writes an execution through the real fenced
// SaveExecution path and leaves the bundle lease free.
func (s *rprService) seedStaleExecution(t *testing.T, execution publication.BundleExecution) {
	t.Helper()
	tables := make([]string, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		tables = append(tables, output.PhysicalTable)
	}
	markLiveTables(tables...)
	granted, err := s.regW.AcquireBundleLease(s.ctx, execution.Key, "seeder", time.Now().UTC().Add(10*time.Minute))
	if err != nil || !granted {
		t.Fatalf("seed lease %s: %v %v", execution.Key, granted, err)
	}
	if err := s.regW.SaveExecution(s.ctx, execution, "seeder"); err != nil {
		t.Fatalf("seed save %s: %v", execution.ID, err)
	}
	if err := s.regW.ReleaseBundleLease(s.ctx, execution.Key, "seeder"); err != nil {
		t.Fatalf("seed release %s: %v", execution.Key, err)
	}
}

func (s *rprService) newReconciler(t *testing.T, releases dataset.ActiveReleaseResolver) *Reconciler {
	t.Helper()
	rc, err := NewReconciler(s.regA, s.pinsA, releases)
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	rc.reconcilerID = "reconciler-test"
	rc.heartbeatTTL = 2 * time.Minute
	rc.renewalEvery = 0 // renewal exercised explicitly by its own test
	return rc
}

func (s *rprService) leaseRow(t *testing.T, key string) string {
	t.Helper()
	return strings.Join(s.queryRows(t, s.w,
		"SELECT owner_id, "+fmt.Sprintf(tsText, "expires_at")+
			" FROM "+pgx.Identifier{s.schema, BundleLeasesTable}.Sanitize()+" WHERE key = $1", key), "|")
}

func (s *rprService) executionRow(t *testing.T, id string) (state, payload string) {
	t.Helper()
	rows := s.queryRows(t, s.w,
		"SELECT state, payload::text FROM "+pgx.Identifier{s.schema, BundleExecutionsTable}.Sanitize()+" WHERE id = $1", id)
	if len(rows) != 1 {
		t.Fatalf("execution rows for %s = %v, want exactly one", id, rows)
	}
	parts := strings.SplitN(rows[0], "|", 2)
	return parts[0], strings.ReplaceAll(parts[1], `": `, `":`)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition never held: %s", what)
}

// ---------------------------------------------------------------------------
// A. Full sweep: keyset coverage + literal terminal records.
// ---------------------------------------------------------------------------

func TestReconcileSweepReclaimsAbandoned(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	// Seven stale RUNNING executions with two outputs each; page size 3
	// forces multi-page keyset continuation, including a VALIDATING and a
	// FAILED state so every swept state is exercised.
	for i := 0; i < 7; i++ {
		state := publication.BundleRunning
		if i == 5 {
			state = publication.BundleValidating
		}
		if i == 6 {
			state = publication.BundleFailed
		}
		s.seedStaleExecution(t, rprExecution(fmt.Sprintf("exec-sweep-%02d", i), "proj-a", "recipe-sweep", state, 2))
	}
	// A fresh (not stale) execution and a QUEUED one must never be touched.
	fresh := rprExecution("exec-sweep-fresh", "proj-a", "recipe-sweep", publication.BundleRunning, 1)
	fresh.UpdatedAt = time.Now().UTC().Add(time.Hour)
	s.seedStaleExecution(t, fresh)
	s.seedStaleExecution(t, rprExecution("exec-sweep-queued", "proj-a", "recipe-sweep", publication.BundleQueued, 1))

	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	rc.pageSize = 3
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }

	if err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	wantDrops := []string{}
	wantDecisions := []ReconcileOutcome{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("exec-sweep-%02d", i)
		wantDrops = append(wantDrops, fmt.Sprintf("tbl-%s-0", id), fmt.Sprintf("tbl-%s-1", id))
		wantDecisions = append(wantDecisions, ReconcileReconciled)
	}
	if got := drops.snapshot(); strings.Join(got, ",") != strings.Join(wantDrops, ",") {
		t.Fatalf("drop calls = %v\nwant exactly %v (keyset order, once per table, none for fresh/queued)", got, wantDrops)
	}
	if len(decisions) != len(wantDecisions) {
		t.Fatalf("decisions = %v, want %d entries", decisions, len(wantDecisions))
	}
	for i, want := range wantDecisions {
		if decisions[i].Outcome != want {
			t.Fatalf("decision[%d] = %v, want %v", i, decisions[i].Outcome, want)
		}
	}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("exec-sweep-%02d", i)
		state, payload := s.executionRow(t, id)
		if state != string(publication.BundleFailed) {
			t.Fatalf("%s state = %q, want FAILED", id, state)
		}
		for _, want := range []string{
			`"error":"stale execution reconciled"`,
			`"failureCode":"PUBLICATION_LEASE_LOST"`,
			`"failureRetryable":true`,
		} {
			if !strings.Contains(payload, want) {
				t.Fatalf("%s payload missing literal %s: %s", id, want, payload)
			}
		}
		if strings.Contains(payload, `"outputs":[{`) {
			t.Fatalf("%s payload still lists outputs after full drop: %s", id, payload)
		}
		if strings.Contains(payload, `"ownerId":"seeder"`) || strings.Contains(payload, `"ownerId":"reconciler-test"`) {
			t.Fatalf("%s payload still names an owner: %s", id, payload)
		}
		if s.leaseRow(t, "key-"+id) != "" {
			t.Fatalf("%s bundle lease still present after reconcile", id)
		}
		if claim := s.claimRow(t, s.w, id); claim != "" {
			t.Fatalf("%s cleanup claim still present after reconcile: %q", id, claim)
		}
		if rows := s.pinsRows(t, s.w, id); len(rows) != 0 {
			t.Fatalf("%s pins still present after reconcile: %v", id, rows)
		}
	}
	if state, _ := s.executionRow(t, "exec-sweep-fresh"); state != string(publication.BundleRunning) {
		t.Fatalf("fresh execution was swept: state %q", state)
	}
	if state, _ := s.executionRow(t, "exec-sweep-queued"); state != string(publication.BundleQueued) {
		t.Fatalf("queued execution was swept: state %q", state)
	}
}

// ---------------------------------------------------------------------------
// B. Pin arbitration.
// ---------------------------------------------------------------------------

func TestReconcileRefusedByLivePin(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	const id = "exec-pin-refuse"
	s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-pin", publication.BundleRunning, 1))
	must(s.pinsA.AcquireExecutionReadPin(ctx, id, "streaming-reader", rprFarFuture))(t)
	before := s.stateDigest(t, s.w)

	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }

	err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
	if !errors.Is(err, publication.ErrExecutionReadPinActive) {
		t.Fatalf("sweep error = %v, want errors.Is publication.ErrExecutionReadPinActive", err)
	}
	if len(decisions) != 1 || decisions[0].Outcome != ReconcileRefusedPinActive {
		t.Fatalf("decisions = %v, want exactly one refused-pin-active", decisions)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("drop hook ran despite the live pin: %v", got)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("bundle lease was not released after the refusal")
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("cleanup claim was minted despite the pin: %q", claim)
	}
	// Execution untouched: same state, same payload bytes.
	if state, payload := s.executionRow(t, id); state != string(publication.BundleRunning) || !strings.Contains(payload, `"updatedAt":"2026-01-05T06:07:08.0009Z"`) {
		t.Fatalf("pinned execution changed: state %q payload %s", state, payload)
	}
	_ = before

	// Convergence: once the reader releases, the next sweep reclaims.
	if err := s.pinsA.ReleaseExecutionReadPin(ctx, id, "streaming-reader"); err != nil {
		t.Fatalf("reader release: %v", err)
	}
	rc2 := s.newReconciler(t, nil)
	rc2.renewalEvery = 0
	if err := rc2.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("convergence sweep: %v", err)
	}
	if got := drops.snapshot(); len(got) != 1 || got[0] != "tbl-"+id+"-0" {
		t.Fatalf("convergence drops = %v, want the single table", got)
	}
}

func TestReconcileForeignClaimRefusesSweep(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	const id = "exec-foreign-claim"
	s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-claim", publication.BundleRunning, 1))
	s.seedClaimDirect(t, id, "other-sweeper", rprFarFuture)

	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
	if !errors.Is(err, publication.ErrExecutionReadPinActive) {
		t.Fatalf("sweep error = %v, want named ErrExecutionReadPinActive", err)
	}
	if len(decisions) != 1 || decisions[0].Outcome != ReconcileRefusedCleanupOwned {
		t.Fatalf("decisions = %v, want refused-cleanup-owned", decisions)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("drops ran under a foreign claim: %v", got)
	}
	if claim := s.claimRow(t, s.w, id); !strings.HasPrefix(claim, "other-sweeper|") {
		t.Fatalf("foreign claim changed: %q", claim)
	}
}

// ---------------------------------------------------------------------------
// C. Pointer and active-release binding gates.
// ---------------------------------------------------------------------------

func TestReconcilePointerSkips(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	// Scan-time pointer: the pointer already names the execution.
	const id = "exec-pointer-live"
	execution := rprExecution(id, "proj-a", "recipe-pointer", publication.BundleRunning, 1)
	s.seedStaleExecution(t, execution)
	if err := s.regW.CompareAndSwapPointer(ctx, execution.PointerName(), "", id); err != nil {
		t.Fatalf("seed pointer: %v", err)
	}
	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	if err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Outcome != ReconcileSkippedPointer {
		t.Fatalf("decisions = %v, want skipped-pointer", decisions)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("drops ran for a pointer-bound execution: %v", got)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("lease not released after pointer skip")
	}
}

func TestReconcileInClaimPointerVeto(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)

	// The scan-time pointer check and the claim transaction's locked
	// pointer read are separated by the pre-claim window -- exactly where a
	// publisher can move the pointer onto the execution under the scan's
	// nose. A scripted publisher commits the pointer there; the claim
	// transaction's in-transaction veto must catch it, minting no claim and
	// dropping nothing.
	const id = "exec-inclaim"
	execution := rprExecution(id, "proj-a", "recipe-inclaim", publication.BundleRunning, 1)
	s.seedStaleExecution(t, execution)
	if err := s.regW.CompareAndSwapPointer(ctx, execution.PointerName(), "", "exec-inclaim-other"); err != nil {
		t.Fatalf("seed pointer: %v", err)
	}
	setBeforeClaim(func(executionID string) {
		if executionID != id {
			return
		}
		if err := s.regB.CompareAndSwapPointer(ctx, execution.PointerName(), "exec-inclaim-other", id); err != nil {
			t.Errorf("pre-claim pointer move: %v", err)
		}
	})

	// Witness 2: with the claim transaction about to lock the pointer row,
	// a concurrent publisher from a DIFFERENT backend must queue behind it.
	casDone := make(chan error, 1)
	casBlocked := make(chan bool, 1)
	setReconcileGates(func(attempt reconcileClaimAttempt) error {
		if attempt.ExecutionID != id {
			return nil
		}
		// The in-transaction veto must stop the claim BEFORE any grant:
		// a granted attempt here means the veto was bypassed and only the
		// later post-claim gate would have (racy) cover.
		if attempt.Outcome == reconcileClaimGranted {
			t.Errorf("claim granted for %s despite the pointer naming it: in-transaction veto bypassed", id)
		}
		return nil
	}, nil)

	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	if err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	setBeforeClaim(nil)
	if len(decisions) != 1 || decisions[0].Outcome != ReconcileSkippedPointer {
		t.Fatalf("decisions = %v, want skipped-pointer (in-transaction pointer veto)", decisions)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("physical drop ran despite the pointer that names the execution: %v", got)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("lease not released after the pointer veto")
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("claim minted despite the pointer veto: %q", claim)
	}
	if state, _ := s.executionRow(t, id); state != string(publication.BundleRunning) {
		t.Fatalf("bound execution was mutated: %q", state)
	}
	_, _ = casDone, casBlocked

	// Witness: the claim transaction really locks the pointer row. Re-run
	// the claim on a second abandoned execution whose pointer names
	// someone else (claim proceeds), and show a concurrent CAS queues
	// behind the claim decision.
	const id2 = "exec-inclaim-2"
	execution2 := rprExecution(id2, "proj-a", "recipe-inclaim-2", publication.BundleRunning, 1)
	s.seedStaleExecution(t, execution2)
	if err := s.regW.CompareAndSwapPointer(ctx, execution2.PointerName(), "", "exec-inclaim-other"); err != nil {
		t.Fatalf("seed pointer 2: %v", err)
	}
	setReconcileGates(func(attempt reconcileClaimAttempt) error {
		if attempt.Outcome != reconcileClaimGranted || attempt.ExecutionID != id2 {
			return nil
		}
		go func() {
			casDone <- s.regB.CompareAndSwapPointer(ctx, execution2.PointerName(), "exec-inclaim-other", id2)
		}()
		select {
		case err := <-casDone:
			casBlocked <- false
			t.Errorf("pointer CAS completed while the claim transaction held its lock: %v", err)
		case <-time.After(50 * time.Millisecond):
			casBlocked <- true
		}
		return nil
	}, nil)
	rc2 := s.newReconciler(t, nil)
	if err := rc2.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if !<-casBlocked {
		t.Fatal("the claim transaction did not hold a row lock a concurrent publisher queued behind")
	}
	if err := <-casDone; err != nil {
		t.Fatalf("concurrent pointer CAS: %v", err)
	}
}

func setBeforeClaim(fn func(executionID string)) {
	if fn == nil {
		reconcileBeforeClaim.Store(nil)
		return
	}
	reconcileBeforeClaim.Store(&fn)
}

func TestReconcileJanitorArmAndBindings(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	// No release reader: the PUBLISHED janitor arm is never enumerated --
	// even a fully orphaned PUBLISHED execution survives the sweep.
	const orphan = "exec-janitor-off"
	s.seedStaleExecution(t, rprExecution(orphan, "proj-off", "recipe-jan", publication.BundlePublished, 1))
	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	if err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("sweep without releases: %v", err)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("PUBLISHED arm ran without an active-release reader: %v", got)
	}
	if state, _ := s.executionRow(t, orphan); state != string(publication.BundlePublished) {
		t.Fatalf("orphaned PUBLISHED execution was swept with releases nil: %q", state)
	}

	// Wired reader: bound execution skips, unbound is reclaimed as
	// superseded with the literal superseded error.
	releases := newRPRReleases()
	const bound = "exec-janitor-bound"
	const free = "exec-janitor-free"
	s.seedStaleExecution(t, rprExecution(bound, "proj-a", "recipe-jan", publication.BundlePublished, 1))
	s.seedStaleExecution(t, rprExecution(free, "proj-a", "recipe-jan", publication.BundlePublished, 1))
	releases.bind("proj-a", bound)

	drops2 := newRPRDrops()
	rc2 := s.newReconciler(t, releases)
	var decisions []ReconcileDecision
	rc2.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	if err := rc2.SweepStale(ctx, time.Now().UTC(), drops2.hook); err != nil {
		t.Fatalf("sweep with releases: %v", err)
	}
	wantOutcomes := []ReconcileOutcome{ReconcileSkippedBound, ReconcileReconciledSuperseded, ReconcileReconciledSuperseded}
	if len(decisions) != len(wantOutcomes) {
		t.Fatalf("decisions = %v, want %d", decisions, len(wantOutcomes))
	}
	for i, want := range wantOutcomes {
		if decisions[i].Outcome != want {
			t.Fatalf("decision[%d] = %v, want %v", i, decisions[i].Outcome, want)
		}
	}
	if decisions[0].ExecutionID != bound || decisions[1].ExecutionID != free || decisions[2].ExecutionID != orphan {
		t.Fatalf("decision order = %v, want [bound free orphan]", decisions)
	}
	if got := drops2.snapshot(); len(got) != 2 || got[0] != "tbl-"+free+"-0" || got[1] != "tbl-"+orphan+"-0" {
		t.Fatalf("drops = %v, want the unbound executions' tables", got)
	}
	if state, payload := s.executionRow(t, bound); state != string(publication.BundlePublished) {
		t.Fatalf("active-release-bound execution swept: %q %s", state, payload)
	}
	if _, payload := s.executionRow(t, free); !strings.Contains(payload, `"error":"superseded execution reconciled (no pointer or active-release reference)"`) {
		t.Fatalf("superseded terminal error missing literal: %s", payload)
	}

	// Fail-closed: an unrelated release-reader failure vetoes the sweep
	// instead of deleting under unknown bindings.
	releases.failWith(errors.New("release store unavailable"))
	const failExec = "exec-janitor-fail"
	s.seedStaleExecution(t, rprExecution(failExec, "proj-a", "recipe-jan", publication.BundlePublished, 1))
	rc3 := s.newReconciler(t, releases)
	err := rc3.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook)
	if err == nil || !strings.Contains(err.Error(), "release store unavailable") {
		t.Fatalf("release-reader failure did not veto fail-closed: %v", err)
	}
	if state, _ := s.executionRow(t, failExec); state != string(publication.BundlePublished) {
		t.Fatalf("execution swept despite release-reader failure: %q", state)
	}
}

// ---------------------------------------------------------------------------
// Lease discipline.
// ---------------------------------------------------------------------------

func TestReconcileSkipsForeignLease(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	const id = "exec-leased"
	execution := rprExecution(id, "proj-a", "recipe-lease", publication.BundleRunning, 1)
	s.seedStaleExecution(t, execution)
	granted, err := s.regW.AcquireBundleLease(ctx, execution.Key, "worker-1", time.Now().UTC().Add(10*time.Minute))
	if err != nil || !granted {
		t.Fatalf("seed worker lease: %v %v", granted, err)
	}
	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	if err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Outcome != ReconcileSkippedLeased {
		t.Fatalf("decisions = %v, want skipped-leased", decisions)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("sweep deleted tables under a foreign live lease: %v", got)
	}
	if s.leaseRow(t, execution.Key) == "" {
		t.Fatal("foreign lease was removed by the sweep")
	}
}

// ---------------------------------------------------------------------------
// D. Two-real-connection racing surface: sweep vs publisher.
// ---------------------------------------------------------------------------

func TestReconcilePublisherRace(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	if pidA, pidB := s.backendPID(t, s.a), s.backendPID(t, s.b); pidA == pidB {
		t.Fatalf("racing connections share backend %d", pidA)
	}
	const rounds = 12
	for round := 0; round < rounds; round++ {
		id := fmt.Sprintf("exec-race-pub-%02d", round)
		execution := rprExecution(id, "proj-a", fmt.Sprintf("recipe-race-%02d", round), publication.BundleRunning, 1)
		s.seedStaleExecution(t, execution)

		start := make(chan struct{})
		var sweepErr, pubErr error
		var wg sync.WaitGroup
		wg.Add(2)
		drops := newRPRDrops()
		rc := s.newReconciler(t, nil)
		rc.reconcilerID = fmt.Sprintf("reconciler-race-%02d", round)
		var decisionMu sync.Mutex
		var decisions []ReconcileDecision
		rc.OnDecision = func(d ReconcileDecision) {
			decisionMu.Lock()
			defer decisionMu.Unlock()
			decisions = append(decisions, d)
		}
		go func() {
			defer wg.Done()
			<-start
			sweepErr = rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
		}()
		go func() {
			defer wg.Done()
			<-start
			pub := s.regB
			granted, err := pub.AcquireBundleLease(ctx, execution.Key, "publisher", time.Now().UTC().Add(10*time.Minute))
			if err != nil {
				pubErr = err
				return
			}
			if !granted {
				pubErr = fmt.Errorf("publisher could not take the lease")
				return
			}
			published := execution
			published.State = publication.BundlePublished
			published.OwnerID = "publisher"
			now := time.Now().UTC()
			published.UpdatedAt = now
			published.PublishedAt = &now
			pubErr = pub.PublishExecution(ctx, execution.PointerName(), "", published)
		}()
		close(start)
		wg.Wait()

		_, payload := s.executionRow(t, id)
		pointer, pointerErr := s.regW.GetPointer(ctx, execution.PointerName())
		published := pointerErr == nil && pointer.ExecutionID == id && strings.Contains(payload, `"state":"PUBLISHED"`)
		decisionMu.Lock()
		reconciled := false
		for _, d := range decisions {
			if d.Outcome == ReconcileReconciled && d.ExecutionID == id {
				reconciled = true
			}
		}
		decisionMu.Unlock()
		if published && reconciled {
			t.Fatalf("round %d: BOTH publisher and sweeper completed (late-publisher over a reconciled row) -- lease discipline violated (sweepErr=%v pubErr=%v payload=%s)",
				round, sweepErr, pubErr, payload)
		}
		if !published && !reconciled {
			t.Fatalf("round %d: neither publisher nor sweeper completed (sweepErr=%v pubErr=%v payload=%s)", round, sweepErr, pubErr, payload)
		}
		if reconciled {
			// The publisher must have been refused (fence lost or CAS
			// conflict), never silently applied over the FAILED record.
			if pubErr == nil {
				t.Fatalf("round %d: publisher reported success while the sweeper won: %v", round, pubErr)
			}
			if got := drops.snapshot(); len(got) != 1 || got[0] != "tbl-"+id+"-0" {
				t.Fatalf("round %d: sweep drops = %v", round, got)
			}
			if pointerErr != nil && !errors.Is(pointerErr, publication.ErrBundleNotFound) {
				t.Fatalf("round %d: pointer read: %v", round, pointerErr)
			}
		} else {
			// Publisher won: sweep skipped-leased or its later steps found
			// the row already PUBLISHED and out of the swept states -- the
			// pointer must name the execution and no drops may have run.
			if !published {
				t.Fatalf("round %d: neither outcome: %v / %v", round, sweepErr, pubErr)
			}
			if got := drops.snapshot(); len(got) != 0 {
				t.Fatalf("round %d: sweep dropped tables under the publisher's lease: %v", round, got)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// E. Crash injection at each durability seam with post-abort convergence.
// ---------------------------------------------------------------------------

func TestReconcileCrashWindows(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)

	// Seam 1: injected failure inside the open claim transaction (after all
	// claim statements applied) -- zero retention state survives, including
	// the minted anchor, while the separately committed bundle lease is
	// still held. A clean re-sweep converges.
	{
		const id = "exec-crash-claim"
		s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-crash", publication.BundleRunning, 1))
		injected := errors.New("injected crash inside the claim transaction")
		setReconcileGates(func(reconcileClaimAttempt) error { return injected }, nil)
		drops := newRPRDrops()
		rc := s.newReconciler(t, nil)
		err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
		setReconcileGates(nil, nil)
		if !errors.Is(err, injected) {
			t.Fatalf("claim-seam crash error = %v, want the injected error", err)
		}
		if got := drops.snapshot(); len(got) != 0 {
			t.Fatalf("drops ran despite the claim crash: %v", got)
		}
		if claim := s.claimRow(t, s.w, id); claim != "" {
			t.Fatalf("claim survived the total rollback: %q", claim)
		}
		if anchor := s.anchorRow(t, s.w, id); anchor != "" {
			t.Fatalf("anchor survived the total rollback: %q", anchor)
		}
		if rows := s.pinsRows(t, s.w, id); len(rows) != 0 {
			t.Fatalf("pins present after claim rollback: %v", rows)
		}
		if s.leaseRow(t, "key-"+id) != "" {
			t.Fatal("baseline parity: a claim failure releases the just-taken bundle lease")
		}
		if state, _ := s.executionRow(t, id); state != string(publication.BundleRunning) {
			t.Fatalf("execution mutated by the crashed sweep: %q", state)
		}
		// Same sweeper converges immediately (same-owner claim refresh),
		// including over the still-held lease it left behind.
		drops2 := newRPRDrops()
		rc2 := s.newReconciler(t, nil)
		if err := rc2.SweepStale(ctx, time.Now().UTC(), drops2.hook); err != nil {
			t.Fatalf("post-abort convergence sweep: %v", err)
		}
		if got := drops2.snapshot(); len(got) != 1 || got[0] != "tbl-"+id+"-0" {
			t.Fatalf("post-abort drops = %v", got)
		}
		if state, _ := s.executionRow(t, id); state != string(publication.BundleFailed) {
			t.Fatalf("post-abort state = %q, want FAILED", state)
		}
	}

	// Seam 2: injected failure between the drop phase and the terminal
	// write -- drops already applied stay applied, the claim stays active,
	// the execution keeps its pre-sweep record, and NOTHING releases.
	{
		const id = "exec-crash-finalize"
		s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-crash2", publication.BundleRunning, 1))
		injected := errors.New("injected crash between drop and terminal write")
		var droppedWhileCrashed []string
		setReconcileGates(nil, func(a reconcileFinalizeAttempt) error {
			droppedWhileCrashed = append([]string(nil), a.DroppedTables...)
			return injected
		})
		drops := newRPRDrops()
		rc := s.newReconciler(t, nil)
		err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
		setReconcileGates(nil, nil)
		if !errors.Is(err, injected) {
			t.Fatalf("finalize-seam crash error = %v, want the injected error", err)
		}
		if got := drops.snapshot(); len(got) != 1 || got[0] != "tbl-"+id+"-0" {
			t.Fatalf("finalize-seam drops = %v, want the applied drop", got)
		}
		if strings.Join(droppedWhileCrashed, ",") != "tbl-"+id+"-0" {
			t.Fatalf("finalize witness drops = %v", droppedWhileCrashed)
		}
		if claim := s.claimRow(t, s.w, id); !strings.HasPrefix(claim, "reconciler-test|") {
			t.Fatalf("abandoned claim must stay active: %q", claim)
		}
		if state, _ := s.executionRow(t, id); state != string(publication.BundleRunning) {
			t.Fatalf("execution must keep its pre-sweep record: %q", state)
		}

		// Post-abort convergence A: the same sweeper re-claims immediately.
		drops2 := newRPRDrops()
		rc2 := s.newReconciler(t, nil)
		if err := rc2.SweepStale(ctx, time.Now().UTC(), drops2.hook); err != nil {
			t.Fatalf("same-sweeper convergence: %v", err)
		}
		if state, _ := s.executionRow(t, id); state != string(publication.BundleFailed) {
			t.Fatalf("same-sweeper convergence state = %q", state)
		}

		// Post-abort convergence B: a FOREIGN sweeper must wait for the
		// abandoned claim to expire, then converges.
		const id2 = "exec-crash-finalize-b"
		s.seedStaleExecution(t, rprExecution(id2, "proj-a", "recipe-crash2", publication.BundleRunning, 1))
		setReconcileGates(nil, func(reconcileFinalizeAttempt) error { return injected })
		rc3 := s.newReconciler(t, nil)
		rc3.heartbeatTTL = 5 * time.Minute
		rc3.leaseTTL = 80 * time.Millisecond
		if err := rc3.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); !errors.Is(err, injected) {
			t.Fatalf("crash B: %v", err)
		}
		setReconcileGates(nil, nil)
		// The abandoned claim stays live for minutes while the just-taken
		// bundle lease lapses in 80 ms: after the lease frees, the foreign
		// sweep reaches the claim guard and must name its refusal.
		time.Sleep(120 * time.Millisecond)
		foreign := s.newReconciler(t, nil)
		foreign.reconcilerID = "reconciler-foreign"
		if err := foreign.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); !errors.Is(err, publication.ErrExecutionReadPinActive) {
			t.Fatalf("foreign sweep over the live abandoned claim = %v, want named refusal", err)
		}
		// Force the abandoned claim past expiry rather than sleeping out a
		// 5-minute TTL; the re-sweep then converges.
		s.queryRows(t, s.w,
			"UPDATE "+pgx.Identifier{s.schema, ExecutionCleanupClaimsTable}.Sanitize()+
				" SET expires_at = $1 WHERE execution_id = $2", rprPast, id2)
		drops4 := newRPRDrops()
		foreign2 := s.newReconciler(t, nil)
		foreign2.reconcilerID = "reconciler-foreign-2"
		if err := foreign2.SweepStale(ctx, time.Now().UTC(), drops4.hook); err != nil {
			t.Fatalf("foreign post-expiry convergence: %v", err)
		}
		// The crashed sweep already deleted the physical table before the
		// finalize abort, so a correct convergence deletes nothing new --
		// its proof is the fenced terminal record and the consumed claim.
		if got := drops4.snapshot(); len(got) != 0 {
			t.Fatalf("foreign convergence drops = %v, want none (table died in the crashed sweep)", got)
		}
		if state, payload := s.executionRow(t, id2); state != string(publication.BundleFailed) ||
			!strings.Contains(payload, `"error":"stale execution reconciled"`) {
			t.Fatalf("foreign convergence record = %q %s", state, payload)
		}
		if claim := s.claimRow(t, s.w, id2); claim != "" {
			t.Fatalf("claim not consumed by convergence: %q", claim)
		}
	}
}

// ---------------------------------------------------------------------------
// F. Heartbeat loss stops the physical deletion mid-drop.
// ---------------------------------------------------------------------------

func TestReconcileHeartbeatLossStopsDrop(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	const id = "exec-heartbeat-loss"
	s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-hb", publication.BundleRunning, 1))

	drops := newRPRDrops()
	releaseNow := make(chan struct{})
	drops.waitFor = releaseNow

	rc := s.newReconciler(t, nil)
	rc.heartbeatTTL = 200 * time.Millisecond
	rc.renewalEvery = 15 * time.Millisecond
	errCh := make(chan error, 1)
	go func() {
		errCh <- rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
	}()
	// The claim is granted before any drop call; once it is visible, the
	// test loses it externally. The renewal tick must notice, cancel the
	// drop context, and the blocked hook returns context.Canceled -- the
	// table is never dropped while the claim is lost.
	waitUntil(t, "cleanup claim granted before the drop completed", func() bool {
		return s.claimRow(t, s.w, id) != ""
	})
	if err := s.pinsW.ReleaseExecutionCleanup(context.Background(), id, "reconciler-test"); err != nil {
		t.Fatalf("external claim release: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	close(releaseNow)
	err := <-errCh
	if !errors.Is(err, publication.ErrExecutionReadPinLost) {
		t.Fatalf("heartbeat-loss sweep error = %v, want errors.Is ErrExecutionReadPinLost", err)
	}
	// The lost table stays listed in remaining outputs of the terminal
	// record, so a retry re-attempts the physical drop.
	_, payload := s.executionRow(t, id)
	if !strings.Contains(payload, `"physicalTable":"tbl-`+id+`-0"`) {
		t.Fatalf("remaining outputs lost the un-dropped table: %s", payload)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("claim should be gone after the sweep finished its releases: %q", claim)
	}
}

// ---------------------------------------------------------------------------
// First-error-wins propagation (documented stranding parity).
// ---------------------------------------------------------------------------

func TestReconcileFirstErrorWins(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

	s.seedStaleExecution(t, rprExecution("exec-abort-a", "proj-a", "recipe-abort", publication.BundleRunning, 1))
	s.seedStaleExecution(t, rprExecution("exec-abort-b", "proj-a", "recipe-abort", publication.BundleRunning, 1))
	must(s.pinsA.AcquireExecutionReadPin(ctx, "exec-abort-a", "reader", rprFarFuture))(t)
	must(s.pinsA.AcquireExecutionReadPin(ctx, "exec-abort-b", "reader", rprFarFuture))(t)

	drops := newRPRDrops()
	rc := s.newReconciler(t, nil)
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook)
	if !errors.Is(err, publication.ErrExecutionReadPinActive) {
		t.Fatalf("sweep error = %v", err)
	}
	if len(decisions) != 1 || decisions[0].ExecutionID != "exec-abort-a" {
		t.Fatalf("decisions = %v, want exactly the first execution (baseline first-error-wins stranding)", decisions)
	}
	if state, _ := s.executionRow(t, "exec-abort-b"); state != string(publication.BundleRunning) {
		t.Fatalf("second execution was processed after the abort: %q", state)
	}
}

// TestZZZeroLeftoverReadPinsFixtures audits only the exact verify_rpr_it_*
// resources this suite creates. This test MUST stay the last test declared
// in these files: Go runs a package's tests in declaration order within each
// source file, and only this suite's files create verify_rpr_it_*
// resources. TestMain additionally invokes the same audit after the whole
// package (registry_test.go) so the required zero-leftover and
// pg_database==baseline checks still execute under filtered -run invocations.
func TestZZZeroLeftoverReadPinsFixtures(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL")) == "" {
		t.Skip("no service configured; nothing could have been created")
	}
	if err := auditReadPinsLeftovers(); err != nil {
		t.Errorf("read-pins zero-leftover audit failed: %v", err)
	}
}
