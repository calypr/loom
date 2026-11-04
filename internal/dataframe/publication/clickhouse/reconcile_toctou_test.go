package clickhouse

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/store/clickhouse"
)

// Adversarial verification for reconcileExecution (bundle_store.go:1056-1102).
// Hypothesis: the claim (:1076) and DropTable loop (:1084) never revalidate
// the bundle-key lease or the pointer, and the cleanup-lease renewal tick
// (cleanupLeaseRenewal = 30s, bundle_store.go:238) can never fire inside the
// 10s boundedBundleCleanupContext region. If the reconciler's lease dies
// before the region arms (stall between :1058 and :1066 counts against the
// 2-min lease but not the 10-s context), a runner retrying a retryable FAILED
// execution (:1092) can publish between the fresh GetPointer (:1068) and the
// drop, and the reconciler deletes the just-published tables. The fenced
// SaveExecution (:1097) then fails AFTER the deletion.
//
// leaseTTL = -1s stages the identical DB state a >=leaseTTL stall produces
// (lease doc expired at the moment the reconciler enters its region) without
// modifying production code.

type tocFakeCatalog struct {
	mu         sync.Mutex
	leases     map[string]tocLease
	pointers   map[string]publication.BundlePointer
	executions map[string]publication.BundleExecution
	states     map[string]*tocExecState
	race       func()
	raceRan    bool
	dropped    []string
	savedFence []string
}

type tocLease struct {
	owner   string
	expires time.Time
}

type tocPin struct {
	owner   string
	expires time.Time
}

type tocExecState struct {
	pins    []tocPin
	cleanup *tocLease
}

func newTOCCatalog() *tocFakeCatalog {
	return &tocFakeCatalog{
		leases:     map[string]tocLease{},
		pointers:   map[string]publication.BundlePointer{},
		executions: map[string]publication.BundleExecution{},
		states:     map[string]*tocExecState{},
	}
}

func (f *tocFakeCatalog) state(execID string) *tocExecState {
	if s := f.states[execID]; s != nil {
		return s
	}
	s := &tocExecState{}
	f.states[execID] = s
	return s
}

// AcquireBundleLease mirrors bundle_registry.go:315.
func (f *tocFakeCatalog) AcquireBundleLease(_ context.Context, key, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	cur, ok := f.leases[key]
	if !ok || cur.expires.Before(now) || cur.owner == owner {
		f.leases[key] = tocLease{owner: owner, expires: expires}
		return true, nil
	}
	return false, nil
}

func (f *tocFakeCatalog) RenewBundleLease(_ context.Context, key, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.leases[key]
	if ok && cur.owner == owner && !cur.expires.Before(time.Now().UTC()) {
		f.leases[key] = tocLease{owner: owner, expires: expires}
		return true, nil
	}
	return false, nil
}

func (f *tocFakeCatalog) ReleaseBundleLease(_ context.Context, key, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.leases[key]; ok && cur.owner == owner {
		delete(f.leases, key)
	}
	return nil
}

func (f *tocFakeCatalog) GetPointer(_ context.Context, name string) (publication.BundlePointer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pointers[name]; ok {
		return p, nil
	}
	return publication.BundlePointer{}, publication.ErrBundleNotFound
}

func (f *tocFakeCatalog) CompareAndSwapPointer(_ context.Context, name, expected, next string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pointers[name]; ok && p.ExecutionID != expected {
		return publication.ErrBundlePointerConflict
	}
	f.pointers[name] = publication.BundlePointer{Name: name, ExecutionID: next, UpdatedAt: time.Now().UTC()}
	return nil
}

// PublishExecution mirrors the single-AQL guard set bundle_registry.go:287-291.
func (f *tocFakeCatalog) PublishExecution(_ context.Context, name, expected string, execution publication.BundleExecution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	stored, ok := f.executions[execution.ID]
	if !ok {
		return publication.ErrBundleNotFound
	}
	lease, ok := f.leases[stored.Key]
	if !ok || lease.owner != execution.OwnerID || lease.expires.Before(now) {
		return publication.ErrBundlePointerConflict
	}
	if p, ok := f.pointers[name]; ok && p.ExecutionID != expected {
		return publication.ErrBundlePointerConflict
	}
	f.executions[execution.ID] = execution
	f.pointers[name] = publication.BundlePointer{Name: name, ExecutionID: execution.ID, UpdatedAt: now}
	return nil
}

// SaveExecution mirrors the fenced save bundle_registry.go:26-47.
func (f *tocFakeCatalog) SaveExecution(_ context.Context, execution publication.BundleExecution, fenceOwner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	lease, ok := f.leases[execution.Key]
	if !ok || lease.owner != fenceOwner || lease.expires.Before(now) {
		return publication.ErrBundleLeaseLost
	}
	f.savedFence = append(f.savedFence, fenceOwner)
	f.executions[execution.ID] = execution
	return nil
}

func (f *tocFakeCatalog) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.executions[id]; ok {
		return e, nil
	}
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (f *tocFakeCatalog) FindExecutionByKey(_ context.Context, key string) (publication.BundleExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.executions {
		if e.Key == key {
			return e, nil
		}
	}
	return publication.BundleExecution{}, publication.ErrBundleNotFound
}

func (f *tocFakeCatalog) ListExecutions(_ context.Context, _ publication.BundleState, _ time.Time) ([]publication.BundleExecution, error) {
	return nil, nil
}

// VisitExecutionPages feeds the stale page for the seeded state only.
func (f *tocFakeCatalog) VisitExecutionPages(_ context.Context, state publication.BundleState, _ time.Time, _ int, visit publication.BundleExecutionPageFunc) error {
	f.mu.Lock()
	page := make([]publication.BundleExecution, 0, 1)
	for _, e := range f.executions {
		if e.State == state {
			page = append(page, e)
		}
	}
	f.mu.Unlock()
	if len(page) == 0 {
		return nil
	}
	return visit(page)
}

func (f *tocFakeCatalog) AcquireExecutionReadPin(_ context.Context, execID, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	now := time.Now().UTC()
	if s.cleanup != nil && !s.cleanup.expires.Before(now) {
		return false, nil
	}
	s.pins = append(s.pins, tocPin{owner: owner, expires: expires})
	return true, nil
}

func (f *tocFakeCatalog) RenewExecutionReadPin(_ context.Context, execID, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	for i, p := range s.pins {
		if p.owner == owner {
			s.pins[i].expires = expires
			return true, nil
		}
	}
	return false, nil
}

func (f *tocFakeCatalog) ReleaseExecutionReadPin(_ context.Context, execID, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	kept := s.pins[:0]
	for _, p := range s.pins {
		if p.owner != owner {
			kept = append(kept, p)
		}
	}
	s.pins = kept
	return nil
}

// ClaimExecutionCleanup mirrors bundle_registry.go:414-422 exactly: no lease,
// state, or pointer check; pins must all be expired; slot free/expired/self.
// The injected race commits before the claim is evaluated, modelling Arango
// ordering where the publish lands after GetPointer (:1068) but before the
// claim query executes.
func (f *tocFakeCatalog) ClaimExecutionCleanup(_ context.Context, execID, owner string) (bool, error) {
	if f.race != nil && !f.raceRan {
		f.raceRan = true
		f.race()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	s := f.state(execID)
	for _, p := range s.pins {
		if !p.expires.Before(now) {
			return false, nil
		}
	}
	if s.cleanup != nil && !s.cleanup.expires.Before(now) && s.cleanup.owner != owner {
		return false, nil
	}
	s.pins = nil
	s.cleanup = &tocLease{owner: owner, expires: now.Add(time.Minute)}
	return true, nil
}

func (f *tocFakeCatalog) RenewExecutionCleanup(_ context.Context, execID, owner string, expires time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.state(execID)
	now := time.Now().UTC()
	if s.cleanup == nil || s.cleanup.owner != owner || s.cleanup.expires.Before(now) {
		return false, nil
	}
	for _, p := range s.pins {
		if !p.expires.Before(now) {
			return false, nil
		}
	}
	s.cleanup.expires = expires
	return true, nil
}

func (f *tocFakeCatalog) ReleaseExecutionCleanup(_ context.Context, execID, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.states[execID]; s != nil && s.cleanup != nil && s.cleanup.owner == owner {
		s.cleanup = nil
	}
	return nil
}

type tocFakeClickHouse struct {
	mu    sync.Mutex
	table string
	drop  func(ctx context.Context) error
}

// DropTable mirrors driver context honoring: a dead ctx aborts the drop.
func (c *tocFakeClickHouse) DropTable(ctx context.Context, table string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.table == table {
		c.table = ""
	}
	if c.drop != nil {
		return c.drop(ctx)
	}
	return nil
}

func (c *tocFakeClickHouse) CreateTable(context.Context, string, []clickhouse.Column) error {
	return nil
}
func (c *tocFakeClickHouse) InsertRows(context.Context, string, []clickhouse.Column, []map[string]any) error {
	return nil
}
func (c *tocFakeClickHouse) VerifyOutput(context.Context, string, []clickhouse.Column, int64) error {
	return nil
}
func (c *tocFakeClickHouse) DropColumns(context.Context, string, []string) error { return nil }

const tocExecID = "exec-toc-1"
const tocTable = "loom_bundle_toc_output"

func tocExecution(state publication.BundleState) publication.BundleExecution {
	now := time.Now().UTC()
	return publication.BundleExecution{
		ID:    tocExecID,
		Key:   "bundle-key-toc",
		State: state,
		Outputs: []publication.BundleOutputRecord{
			{Name: "output", PhysicalTable: tocTable, RowCount: 10},
		},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-time.Hour),
	}
}

func tocStore(t *testing.T, ch *tocFakeClickHouse, cat *tocFakeCatalog, leaseTTL time.Duration) *ClickHouseBundleStore {
	t.Helper()
	s, err := NewBundleStore(ch, cat)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}
	s.leaseTTL = leaseTTL
	return s
}

// TestReconcileDropsJustPublishedBundleStagesLeaseLoss is the converted F-B1
// TOCTOU reproduction: lease dead (stall-equivalent) + racing publish that
// lands after the pre-claim GetPointer and before the claim. Pre-Repair-1b
// the reconciler's one-shot guard let the drop through onto that live
// pointer (archived CONFIRMED-BUG red; guardless copy reproduces it verbatim:
// "deleted the physical tables of a bundle the pointer advertises as live").
// With the post-claim live pointer re-check the drop is denied, so the test
// now PASSES naturally and the former CONFIRMED branch is retained as the
// regression direction: if the table ever disappears under a live pointer,
// this fails again.
func TestReconcileDropsJustPublishedBundleStagesLeaseLoss(t *testing.T) {
	cat := newTOCCatalog()
	ch := &tocFakeClickHouse{table: tocTable}
	exec := tocExecution(publication.BundleRunning)
	cat.executions[tocExecID] = exec

	cat.race = func() {
		now := time.Now().UTC()
		acquired, err := cat.AcquireBundleLease(context.Background(), exec.Key, "runner", now.Add(2*time.Minute))
		if err != nil || !acquired {
			t.Errorf("racing runner could not take over the reconciler lease: acquired=%v err=%v", acquired, err)
			return
		}
		published := exec
		published.State = publication.BundlePublished
		published.OwnerID = "runner"
		published.PublishedAt = &now
		if err := cat.PublishExecution(context.Background(), exec.PointerName(), tocExecID, published); err != nil {
			t.Errorf("racing publish failed: %v", err)
		}
	}

	s := tocStore(t, ch, cat, -time.Second)
	err := s.Reconcile(context.Background(), time.Now().UTC().Add(-time.Minute))

	cat.mu.Lock()
	pointer, pointerLive := cat.pointers[exec.PointerName()]
	published := cat.executions[tocExecID].State == publication.BundlePublished
	cat.mu.Unlock()

	if !published {
		t.Fatalf("precondition: racing publish did not land")
	}
	if !pointerLive || pointer.ExecutionID != tocExecID {
		t.Fatalf("precondition: pointer does not name the published execution: %+v", pointer)
	}
	if ch.table == "" {
		t.Errorf("REGRESSION (F-B1 TOCTOU): reconcileExecution deleted the physical tables of a bundle the pointer advertises as live; pointer %q still names %q; tables gone. Reconcile error chain: %v (errors.Is ErrBundleLeaseLost=%v)",
			pointer.Name, pointer.ExecutionID, err, errors.Is(err, publication.ErrBundleLeaseLost))
	}
	if ch.table != tocTable {
		t.Fatalf("post-condition: table state unexpected, want the live table %q preserved, got %q", tocTable, ch.table)
	}
	if err != nil {
		t.Fatalf("post-condition: prevented drop must reconcile cleanly, got %v", err)
	}
}

// TestReconcileSkipsPointerNamedExecution is the control for the :1073 guard:
// publish lands BEFORE GetPointer, reconciler must leave tables alone.
func TestReconcileSkipsPointerNamedExecution(t *testing.T) {
	cat := newTOCCatalog()
	ch := &tocFakeClickHouse{table: tocTable}
	exec := tocExecution(publication.BundleRunning)
	cat.executions[tocExecID] = exec
	cat.pointers[exec.PointerName()] = publication.BundlePointer{Name: exec.PointerName(), ExecutionID: tocExecID}

	s := tocStore(t, ch, cat, -time.Second)
	if err := s.Reconcile(context.Background(), time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if ch.table != tocTable {
		t.Fatalf("regression: :1073 pointer skip dropped a pointer-named execution")
	}
}

// TestReconcileDropsLongStaleUnpublishedExecution is the ordinary-path control:
// with a live lease and no pointer the reconciler must delete and fence-save.
func TestReconcileDropsLongStaleUnpublishedExecution(t *testing.T) {
	cat := newTOCCatalog()
	ch := &tocFakeClickHouse{table: tocTable}
	exec := tocExecution(publication.BundleFailed)
	cat.executions[tocExecID] = exec

	s := tocStore(t, ch, cat, 2*time.Minute)
	if err := s.Reconcile(context.Background(), time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if ch.table != "" {
		t.Fatalf("ordinary stale cleanup did not drop the stale table")
	}
	if len(cat.savedFence) != 1 {
		t.Fatalf("expected exactly one fenced SaveExecution, got %v", cat.savedFence)
	}
}
