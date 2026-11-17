package postgres

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
	"github.com/jackc/pgx/v5/pgconn"

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

type rprDrops struct {
	mu       sync.Mutex
	calls    []string
	perTable map[string]error
	waitFor  <-chan struct{}
	hookErr  func(table string, ctx context.Context) error
}

func newRPRDrops() *rprDrops {
	return &rprDrops{perTable: map[string]error{}}
}

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
		return nil
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
	rc.fixIdentity("reconciler-test")
	rc.heartbeatTTL = 2 * time.Minute
	rc.renewalEvery = 0
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

func TestReconcileSweepReclaimsAbandoned(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

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

func TestReconcileDropFailureConvergesIdempotently(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)

	const id = "exec-dropconv"
	tbl0 := "tbl-" + id + "-0"
	tbl1 := "tbl-" + id + "-1"
	s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-dropconv", publication.BundleRunning, 2))

	failing := newRPRDrops()
	failing.hookErr = func(table string, _ context.Context) error {
		if table == tbl0 {
			return errors.New("drop callback exploded")
		}
		return nil
	}
	rc := s.newReconciler(t, nil)
	err := rc.SweepStale(ctx, time.Now().UTC(), failing.hook)
	if err == nil || !strings.Contains(err.Error(), "drop callback exploded") {
		t.Fatalf("sweep 1 error = %v, want the drop callback failure", err)
	}
	if got := strings.Join(failing.snapshot(), ","); got != tbl0+"!error,"+tbl1 {
		t.Fatalf("sweep 1 drop witnesses = %v, want [%s %s] in page order", failing.snapshot(), tbl0+"!error", tbl1)
	}
	state, payload := s.executionRow(t, id)
	if state != string(publication.BundleFailed) {
		t.Fatalf("state after failed drop = %q, want FAILED", state)
	}
	if !strings.Contains(payload, tbl0) || strings.Contains(payload, tbl1) {
		t.Fatalf("remaining after failed drop wrong (want %s kept, %s gone): %s", tbl0, tbl1, payload)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("failed-drop sweep stranded the cleanup claim: %q", claim)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("failed-drop sweep stranded the bundle lease")
	}
	if got := s.pinsRows(t, s.w, id); len(got) != 0 {
		t.Fatalf("failed-drop sweep left pin rows: %v", got)
	}

	rc2, err := NewReconciler(s.regA, s.pinsA, nil)
	if err != nil {
		t.Fatalf("new unfixed reconciler: %v", err)
	}
	converged := newRPRDrops()
	if err := rc2.SweepStale(ctx, time.Now().UTC(), converged.hook); err != nil {
		t.Fatalf("converging sweep: %v", err)
	}
	if got := converged.snapshot(); len(got) != 1 || got[0] != tbl0 {
		t.Fatalf("converging drop witnesses = %v, want exactly [%s]: the already-dead %s must be a no-op re-drop, not a recounted deletion", got, tbl0, tbl1)
	}
	state, payload = s.executionRow(t, id)
	if state != string(publication.BundleFailed) {
		t.Fatalf("state after convergence = %q, want FAILED", state)
	}
	if strings.Contains(payload, tbl0) || strings.Contains(payload, tbl1) {
		t.Fatalf("convergence left outputs in the record: %s", payload)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("converging sweep stranded the cleanup claim: %q", claim)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("converging sweep stranded the bundle lease")
	}
}

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
	if state, payload := s.executionRow(t, id); state != string(publication.BundleRunning) || !strings.Contains(payload, `"updatedAt":"2026-01-05T06:07:08.0009Z"`) {
		t.Fatalf("pinned execution changed: state %q payload %s", state, payload)
	}
	_ = before

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

func TestReconcilePointerSkips(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	setReconcileGates(nil, nil)
	defer setReconcileGates(nil, nil)

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

	casDone := make(chan error, 1)
	casBlocked := make(chan bool, 1)
	setReconcileGates(func(attempt reconcileClaimAttempt) error {
		if attempt.ExecutionID != id {
			return nil
		}
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
		rc.fixIdentity(fmt.Sprintf("reconciler-race-%02d", round))
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
			if !published {
				t.Fatalf("round %d: neither outcome: %v / %v", round, sweepErr, pubErr)
			}
			if got := drops.snapshot(); len(got) != 0 {
				t.Fatalf("round %d: sweep dropped tables under the publisher's lease: %v", round, got)
			}
		}
	}
}

func TestReconcileCrashWindows(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)

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

		drops2 := newRPRDrops()
		rc2 := s.newReconciler(t, nil)
		if err := rc2.SweepStale(ctx, time.Now().UTC(), drops2.hook); err != nil {
			t.Fatalf("fixed-identity refresh convergence: %v", err)
		}
		if state, _ := s.executionRow(t, id); state != string(publication.BundleFailed) {
			t.Fatalf("fixed-identity refresh convergence state = %q", state)
		}

		const id3 = "exec-crash-finalize-a2"
		s.seedStaleExecution(t, rprExecution(id3, "proj-a", "recipe-crash-a2", publication.BundleRunning, 1))
		setReconcileGates(nil, func(reconcileFinalizeAttempt) error { return injected })
		rcA2, err := NewReconciler(s.regA, s.pinsA, nil)
		if err != nil {
			t.Fatalf("new unfixed reconciler: %v", err)
		}
		if err := rcA2.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); !errors.Is(err, injected) {
			t.Fatalf("crash A2: %v", err)
		}
		setReconcileGates(nil, nil)
		if claim := s.claimRow(t, s.w, id3); claim == "" || strings.HasPrefix(claim, "reconciler-test|") {
			t.Fatalf("crashed A2 claim must carry a minted identity: %q", claim)
		}
		var a2Decisions []ReconcileDecision
		rcA2.OnDecision = func(d ReconcileDecision) { a2Decisions = append(a2Decisions, d) }
		if err := rcA2.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); err != nil {
			t.Fatalf("same-instance sweep after same-instance crash: %v", err)
		}
		sawLeased := false
		for _, d := range a2Decisions {
			if d.ExecutionID == id3 && d.Outcome == ReconcileSkippedLeased {
				sawLeased = true
			}
		}
		if !sawLeased {
			t.Fatalf("the next sweep of the same instance must be skipped-leased by the abandoned lease: %v", a2Decisions)
		}
		if state, _ := s.executionRow(t, id3); state != string(publication.BundleRunning) {
			t.Fatalf("abandoned record mutated by a blocked sweep: %q", state)
		}
		s.queryRows(t, s.w,
			"UPDATE "+pgx.Identifier{s.schema, BundleLeasesTable}.Sanitize()+
				" SET expires_at = $1 WHERE key = $2", rprPast, "key-"+id3)
		s.queryRows(t, s.w,
			"UPDATE "+pgx.Identifier{s.schema, ExecutionCleanupClaimsTable}.Sanitize()+
				" SET expires_at = $1 WHERE execution_id = $2", rprPast, id3)
		dropsA2 := newRPRDrops()
		if err := rcA2.SweepStale(ctx, time.Now().UTC(), dropsA2.hook); err != nil {
			t.Fatalf("same-instance post-expiry convergence: %v", err)
		}
		if state, payload := s.executionRow(t, id3); state != string(publication.BundleFailed) ||
			!strings.Contains(payload, `"error":"stale execution reconciled"`) {
			t.Fatalf("same-instance post-expiry convergence record = %q %s", state, payload)
		}
		if claim := s.claimRow(t, s.w, id3); claim != "" {
			t.Fatalf("convergence did not consume the claim: %q", claim)
		}
		if got := dropsA2.snapshot(); len(got) != 0 {
			t.Fatalf("post-expiry convergence drops = %v, want none (table died in the crashed sweep)", got)
		}

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
		time.Sleep(120 * time.Millisecond)
		foreign := s.newReconciler(t, nil)
		foreign.fixIdentity("reconciler-foreign")
		if err := foreign.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); !errors.Is(err, publication.ErrExecutionReadPinActive) {
			t.Fatalf("foreign sweep over the live abandoned claim = %v, want named refusal", err)
		}
		s.queryRows(t, s.w,
			"UPDATE "+pgx.Identifier{s.schema, ExecutionCleanupClaimsTable}.Sanitize()+
				" SET expires_at = $1 WHERE execution_id = $2", rprPast, id2)
		drops4 := newRPRDrops()
		foreign2 := s.newReconciler(t, nil)
		foreign2.fixIdentity("reconciler-foreign-2")
		if err := foreign2.SweepStale(ctx, time.Now().UTC(), drops4.hook); err != nil {
			t.Fatalf("foreign post-expiry convergence: %v", err)
		}
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
	_, payload := s.executionRow(t, id)
	if !strings.Contains(payload, `"physicalTable":"tbl-`+id+`-0"`) {
		t.Fatalf("remaining outputs lost the un-dropped table: %s", payload)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("claim should be gone after the sweep finished its releases: %q", claim)
	}
}

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

func eqStatementLists(t *testing.T, label string, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: statement count = %d, want %d\ngot:  %#v\nwant: %#v", label, len(got), len(want), got, want)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("%s: statement[%d] mismatch\ngot:  %s\nwant: %s", label, i, got[i], want[i])
		}
	}
}

func TestReconcileGoldenClaimStatements(t *testing.T) {
	locks := func(s *rprService) (pointer, ensure, anchor, active, read, gc, grant, touch string) {
		q := func(table string) string { return pgx.Identifier{s.schema, table}.Sanitize() }
		pointer = "SELECT execution_id FROM " + q(BundlePointersTable) + " WHERE pointer_key = $1 FOR UPDATE"
		ensure = "INSERT INTO " + q(ExecutionRetentionTable) + " (execution_id, updated_at) VALUES ($1, $2) ON CONFLICT (execution_id) DO NOTHING"
		anchor = "SELECT true FROM " + q(ExecutionRetentionTable) + " WHERE execution_id = $1 FOR UPDATE"
		active = "SELECT EXISTS (SELECT 1 FROM " + q(ExecutionReadPinsTable) + " WHERE execution_id = $1 AND expires_at >= $2)"
		read = "SELECT owner_id, expires_at FROM " + q(ExecutionCleanupClaimsTable) + " WHERE execution_id = $1"
		gc = "DELETE FROM " + q(ExecutionReadPinsTable) + " WHERE execution_id = $1 AND expires_at < $2"
		grant = "INSERT INTO " + q(ExecutionCleanupClaimsTable) + " (execution_id, owner_id, expires_at, updated_at)" +
			" VALUES ($1, $2, $3, $4) ON CONFLICT (execution_id) DO UPDATE SET owner_id = $2, expires_at = $3, updated_at = $4"
		touch = "UPDATE " + q(ExecutionRetentionTable) + " SET updated_at = $2 WHERE execution_id = $1"
		return
	}

	runCapture := func(t *testing.T, prepare func(s *rprService, id string), wantOutcome ReconcileOutcome, wantErr bool) {
		t.Helper()
		s := newRPRService(t)
		ctx := s.ctx
		defer setReconcileGates(nil, nil)
		pointer, ensure, anchor, active, read, gc, grant, touch := locks(s)
		var attempts []reconcileClaimAttempt
		setReconcileGates(func(a reconcileClaimAttempt) error {
			attempts = append(attempts, a)
			return nil
		}, nil)
		const id = "exec-golden-claim"
		s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-golden", publication.BundleRunning, 1))
		if prepare != nil {
			prepare(s, id)
		}
		rc := s.newReconciler(t, nil)
		err := rc.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook)
		if wantErr && !errors.Is(err, publication.ErrExecutionReadPinActive) {
			t.Fatalf("sweep error = %v, want named refusal", err)
		}
		if !wantErr && err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if len(attempts) != 1 {
			t.Fatalf("claim attempts = %d, want exactly one: %#v", len(attempts), attempts)
		}
		a := attempts[0]
		if a.ExecutionID != id || a.Outcome != wantOutcome {
			t.Fatalf("attempt = %s/%v, want %s/%v", a.ExecutionID, a.Outcome, id, wantOutcome)
		}
		if a.ReconcilerID != "reconciler-test" {
			t.Fatalf("attempt identity = %q, want reconciler-test", a.ReconcilerID)
		}
		if a.BackendPID == 0 {
			t.Fatal("claim attempt carries no backend witness")
		}
		switch wantOutcome {
		case reconcileClaimGranted:
			eqStatementLists(t, "granted", []string{pointer, ensure, anchor, active, read, gc, grant, touch}, a.Statements)
		case ReconcileRefusedPinActive:
			eqStatementLists(t, "refused-pin", []string{pointer, ensure, anchor, active}, a.Statements)
		case ReconcileRefusedCleanupOwned:
			eqStatementLists(t, "refused-claim", []string{pointer, ensure, anchor, active, read}, a.Statements)
		case ReconcileSkippedPointer:
			eqStatementLists(t, "vetoed-pointer", []string{pointer, ensure, anchor}, a.Statements)
		}
	}

	t.Run("granted", func(t *testing.T) { runCapture(t, nil, reconcileClaimGranted, false) })
	t.Run("refused_by_active_pin", func(t *testing.T) {
		runCapture(t, func(s *rprService, id string) {
			if granted, err := s.pinsW.AcquireExecutionReadPin(s.ctx, id, "reader-1", rprFarFuture); err != nil || !granted {
				t.Fatalf("seed pin: %v %v", granted, err)
			}
		}, ReconcileRefusedPinActive, true)
	})
	t.Run("refused_by_foreign_claim", func(t *testing.T) {
		runCapture(t, func(s *rprService, id string) {
			if granted, err := s.pinsW.ClaimExecutionCleanup(s.ctx, id, "worker-foreign"); err != nil || !granted {
				t.Fatalf("seed claim: %v %v", granted, err)
			}
		}, ReconcileRefusedCleanupOwned, true)
	})
	t.Run("vetoed_by_in_transaction_pointer", func(t *testing.T) {
		runCapture(t, func(s *rprService, id string) {
			execution := rprExecution(id, "proj-a", "recipe-golden", publication.BundleRunning, 1)
			if err := s.regW.CompareAndSwapPointer(s.ctx, execution.PointerName(), "", "exec-golden-other"); err != nil {
				t.Fatalf("seed pointer: %v", err)
			}
			setBeforeClaim(func(executionID string) {
				if executionID != id {
					return
				}
				if err := s.regB.CompareAndSwapPointer(s.ctx, execution.PointerName(), "exec-golden-other", id); err != nil {
					t.Errorf("pre-claim pointer move: %v", err)
				}
			})
		}, ReconcileSkippedPointer, false)
		defer setBeforeClaim(nil)
	})
}

func TestReconcileTerminalReleaseSurvivesSlowDrop(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)
	const id = "exec-slowdrop"
	s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-slowdrop", publication.BundleRunning, 1))
	rc := s.newReconciler(t, nil)
	rc.callTimeout = 250 * time.Millisecond
	drops := newRPRDrops()
	slowDrop := func(ctx context.Context, table string) error {
		time.Sleep(450 * time.Millisecond)
		return drops.hook(ctx, table)
	}
	if err := rc.SweepStale(ctx, time.Now().UTC(), slowDrop); err != nil {
		t.Fatalf("sweep with slow drop: %v", err)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("cleanup claim stranded live after a slow drop: %q", claim)
	}
	state, payload := s.executionRow(t, id)
	if state != string(publication.BundleFailed) || !strings.Contains(payload, `"error":"stale execution reconciled"`) {
		t.Fatalf("terminal record not written through the slow drop: %q %s", state, payload)
	}
	if got := drops.snapshot(); len(got) != 1 || got[0] != "tbl-"+id+"-0" {
		t.Fatalf("drops = %v", got)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("bundle lease stranded after a slow drop")
	}
}

func TestReconcileVetoCleanupReleaseBounded(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)
	defer setReadPinGate(nil)

	const id = "exec-veto-bound"
	execution := rprExecution(id, "proj-a", "recipe-vetobound", publication.BundleRunning, 1)
	s.seedStaleExecution(t, execution)
	releases := newRPRReleases()
	releases.bind("proj-a", id)

	rc := s.newReconciler(t, releases)
	rc.callTimeout = 900 * time.Millisecond
	setReconcileGates(func(attempt reconcileClaimAttempt) error {
		if attempt.ExecutionID == id && attempt.Outcome == reconcileClaimGranted {
			time.Sleep(650 * time.Millisecond)
		}
		return nil
	}, nil)
	setReadPinGate(func(attempt readPinAttempt) error {
		if attempt.Op == "release-cleanup" && attempt.ExecutionID == id {
			time.Sleep(500 * time.Millisecond)
		}
		return nil
	})

	started := time.Now()
	drops := newRPRDrops()
	var decisions []ReconcileDecision
	rc.OnDecision = func(d ReconcileDecision) { decisions = append(decisions, d) }
	if err := rc.SweepStale(ctx, time.Now().UTC(), drops.hook); err != nil {
		t.Fatalf("veto sweep with a delayed claim release: %v", err)
	}
	if got := time.Since(started); got > 8*time.Second {
		t.Fatalf("veto sweep blocked %v, want bounded", got)
	}
	sawBound := false
	for _, d := range decisions {
		if d.ExecutionID == id && d.Outcome == ReconcileSkippedBound {
			sawBound = true
		}
	}
	if !sawBound {
		t.Fatalf("active-release veto expected on the post-claim gate: %v", decisions)
	}
	if got := drops.snapshot(); len(got) != 0 {
		t.Fatalf("veto ran physical drops: %v", got)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("veto path stranded the cleanup claim (release died past the pre-claim budget): %q", claim)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("veto path stranded the bundle lease")
	}
}

func TestReconcileSweepsAreMutuallyExclusive(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)
	const id = "exec-concur"
	s.seedStaleExecution(t, rprExecution(id, "proj-a", "recipe-concur", publication.BundleRunning, 1))
	rc, err := NewReconciler(s.regA, s.pinsA, nil)
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	rc.heartbeatTTL = 30 * time.Second
	rc.renewalEvery = 20 * time.Millisecond
	delayedDrop := func(d *rprDrops) PhysicalDropTable {
		return func(ctx context.Context, table string) error {
			time.Sleep(300 * time.Millisecond)
			return d.hook(ctx, table)
		}
	}
	dropsA, dropsB := newRPRDrops(), newRPRDrops()
	start := make(chan struct{})
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errA = rc.SweepStale(ctx, time.Now().UTC(), delayedDrop(dropsA))
	}()
	go func() {
		defer wg.Done()
		<-start
		errB = rc.SweepStale(ctx, time.Now().UTC(), delayedDrop(dropsB))
	}()
	close(start)
	wg.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("concurrent sweeps of one instance must exclude through the lease, not abort each other: A=%v B=%v", errA, errB)
	}
	total := append(dropsA.snapshot(), dropsB.snapshot()...)
	if len(total) != 1 || total[0] != "tbl-"+id+"-0" {
		t.Fatalf("drops = %v, want exactly one physical drop across both sweeps", total)
	}
	if state, _ := s.executionRow(t, id); state != string(publication.BundleFailed) {
		t.Fatalf("execution state = %q, want FAILED", state)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("claim not consumed: %q", claim)
	}
	if s.leaseRow(t, "key-"+id) != "" {
		t.Fatal("lease not consumed")
	}
}

func TestReconcileMintsFreshIdentityPerSweep(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	defer setReconcileGates(nil, nil)
	var attempts []reconcileClaimAttempt
	setReconcileGates(func(a reconcileClaimAttempt) error {
		attempts = append(attempts, a)
		return nil
	}, nil)
	rc, err := NewReconciler(s.regA, s.pinsA, nil)
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	s.seedStaleExecution(t, rprExecution("exec-id-a", "proj-a", "recipe-ida", publication.BundleRunning, 1))
	if err := rc.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); err != nil {
		t.Fatalf("sweep 1: %v", err)
	}
	s.seedStaleExecution(t, rprExecution("exec-id-b", "proj-a", "recipe-idb", publication.BundleRunning, 1))
	if err := rc.SweepStale(ctx, time.Now().UTC(), newRPRDrops().hook); err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	var first, second string
	for _, a := range attempts {
		switch a.ExecutionID {
		case "exec-id-a":
			if first == "" {
				first = a.ReconcilerID
			}
		case "exec-id-b":
			second = a.ReconcilerID
		}
	}
	if first == "" || second == "" {
		t.Fatalf("claim attempts missing a sweep target: %v", attempts)
	}
	if first == second || !strings.HasPrefix(first, "reconciler-") || !strings.HasPrefix(second, "reconciler-") {
		t.Fatalf("sweep identities must be freshly minted per call: %q vs %q", first, second)
	}
}

type unsafeDB struct {
	inner DB
	junk  map[string]string
}

func (u unsafeDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return u.inner.Exec(ctx, sql, args...)
}

func (u unsafeDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return u.inner.Query(ctx, sql, args...)
}

func (u unsafeDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return u.inner.QueryRow(ctx, sql, args...)
}

func (u unsafeDB) Begin(ctx context.Context) (pgx.Tx, error) {
	return u.inner.Begin(ctx)
}

func TestNewReconcilerRejectsUnsafeOrMismatchedBindings(t *testing.T) {
	s := newRPRService(t)

	junk := map[string]string{"canary": "value"}
	regU, err := New(s.w, s.schema)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	pinsU, err := NewReadPins(s.w, s.schema)
	if err != nil {
		t.Fatalf("new pins: %v", err)
	}
	regU.db = unsafeDB{inner: s.w, junk: junk}
	pinsU.db = unsafeDB{inner: s.w, junk: junk}
	if _, err := NewReconciler(regU, pinsU, nil); err == nil {
		t.Fatal("non-comparable database bindings accepted")
	} else if !strings.Contains(err.Error(), "not comparable") {
		t.Fatalf("non-comparable binding error = %v, want the comparable refusal", err)
	}

	regM, err := New(s.w, s.schema)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	pinsM, err := NewReadPins(s.w, s.schema)
	if err != nil {
		t.Fatalf("new pins: %v", err)
	}
	regM.db = s.w
	pinsM.db = s.b
	if _, err := NewReconciler(regM, pinsM, nil); err == nil {
		t.Fatal("mismatched comparable pools accepted")
	}

	regC, err := New(s.w, s.schema)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	pinsC, err := NewReadPins(s.w, s.schema)
	if err != nil {
		t.Fatalf("new pins: %v", err)
	}
	if _, err := NewReconciler(regC, pinsC, nil); err != nil {
		t.Fatalf("matched bindings rejected: %v", err)
	}
}

func TestBundleCatalogDelegatesPinArbitration(t *testing.T) {
	s := newRPRService(t)
	ctx := s.ctx
	cat, err := NewBundleCatalog(s.w, s.schema)
	if err != nil {
		t.Fatalf("new bundle catalog: %v", err)
	}
	if _, ok := any(cat).(publication.ExecutionReadPinCatalog); !ok {
		t.Fatal("consumers cannot discover read pins on the bundle catalog value")
	}
	const id = "exec-catalog-delegation"
	granted, err := cat.AcquireExecutionReadPin(ctx, id, "reader-1", rprFarFuture)
	if err != nil || !granted {
		t.Fatalf("catalog acquire pin = %v, %v", granted, err)
	}
	if rows := s.pinsRows(t, s.w, id); len(rows) != 1 || !strings.HasPrefix(rows[0], "reader-1|") {
		t.Fatalf("acquire not delegated to the arbiter: %v", rows)
	}
	if _, err := cat.RenewExecutionReadPin(ctx, id, "reader-1", rprFarFuture.Add(time.Hour)); err != nil {
		t.Fatalf("catalog renew pin: %v", err)
	}
	if claimed, err := cat.ClaimExecutionCleanup(ctx, id, "cleanup-1"); err != nil || claimed {
		t.Fatalf("catalog claim over a live pin = %v, %v, want (false, nil) baseline refusal", claimed, err)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("refused catalog claim minted a claim row: %q", claim)
	}
	if err := cat.ReleaseExecutionReadPin(ctx, id, "reader-1"); err != nil {
		t.Fatalf("catalog release pin: %v", err)
	}
	if rows := s.pinsRows(t, s.w, id); len(rows) != 0 {
		t.Fatalf("release not delegated: %v", rows)
	}
	granted, err = cat.ClaimExecutionCleanup(ctx, id, "cleanup-1")
	if err != nil || !granted {
		t.Fatalf("catalog claim after release = %v, %v", granted, err)
	}
	if _, err := cat.RenewExecutionCleanup(ctx, id, "cleanup-1", rprFarFuture); err != nil {
		t.Fatalf("catalog renew cleanup: %v", err)
	}
	if err := cat.ReleaseExecutionCleanup(ctx, id, "cleanup-1"); err != nil {
		t.Fatalf("catalog release cleanup: %v", err)
	}
	if claim := s.claimRow(t, s.w, id); claim != "" {
		t.Fatalf("catalog release cleanup not delegated: %q", claim)
	}
	if cat.Pins() == nil {
		t.Fatal("catalog does not expose its bound arbiter for reconciler wiring")
	}
}

func TestZZZeroLeftoverReadPinsFixtures(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL")) == "" {
		t.Skip("no service configured; nothing could have been created")
	}
	if err := auditReadPinsLeftovers(); err != nil {
		t.Errorf("read-pins zero-leftover audit failed: %v", err)
	}
}
