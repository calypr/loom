package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type recordingClickHouse struct {
	mu           sync.Mutex
	creates      []string
	inserts      int
	verified     []string
	drops        []string
	dropFailures map[string]int
}

func (f *recordingClickHouse) CreateTable(_ context.Context, table string, _ []clickhouse.Column) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, table)
	return nil
}

func (f *recordingClickHouse) InsertRows(_ context.Context, _ string, _ []clickhouse.Column, rows []map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserts += len(rows)
	return nil
}

func (f *recordingClickHouse) VerifyOutput(_ context.Context, table string, _ []clickhouse.Column, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verified = append(f.verified, table)
	return nil
}

func (f *recordingClickHouse) DropTable(_ context.Context, table string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if remaining := f.dropFailures[table]; remaining > 0 {
		f.dropFailures[table] = remaining - 1
		return errors.New("fake ClickHouse: injected drop failure")
	}
	f.drops = append(f.drops, table)
	return nil
}

func (f *recordingClickHouse) failDrops(table string, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dropFailures == nil {
		f.dropFailures = map[string]int{}
	}
	f.dropFailures[table] = times
}

func (f *recordingClickHouse) dropCount(table string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, dropped := range f.drops {
		if dropped == table {
			count++
		}
	}
	return count
}

func (f *recordingClickHouse) totalDrops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.drops)
}

func managerIdentity() chartifact.Identity {
	return chartifact.Identity{
		ExecutionID: "execution-mgr", OutputID: "output-mgr", StageID: "stage-mgr",
		Project: "project-mgr", DatasetGeneration: "generation-mgr",
		RecipeDigest: "recipe-digest", PlanDigest: "plan-digest",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
		ScopeMode:     chartifact.ScopeModeRows,
	}
}

func managerColumns(t *testing.T) []chartifact.Column {
	t.Helper()
	columns, err := chartifact.NormalizeSchema([]chartifact.Column{
		{ID: "col-name", Name: "name", SemanticPath: "Patient.name", LogicalType: "string", ClickHouseType: "String"},
	})
	if err != nil {
		t.Fatalf("NormalizeSchema = %v", err)
	}
	return columns
}

func newManager(t *testing.T, s *catalogService, ch *recordingClickHouse) *chartifact.Manager {
	t.Helper()
	manager, err := chartifact.New(chartifact.Config{
		Catalog: s.catalog, ClickHouse: ch,
		BatchRows: 1000, BatchBytes: 1 << 20, LeaseTTL: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("chartifact.New = %v", err)
	}
	return manager
}

func writeOneRow(t *testing.T, writer *chartifact.Writer) {
	t.Helper()
	if err := writer.Write(context.Background(), map[string]any{
		"__loom_row_id": "row-1", "name": "Ada",
	}); err != nil {
		t.Fatalf("Write = %v", err)
	}
}

func TestManagerLifecycleThroughPostgresCatalogDropsOnceOutsideTransactions(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	ch := &recordingClickHouse{}
	manager := newManager(t, s, ch)

	writer, err := manager.Begin(context.Background(), managerIdentity(), managerColumns(t))
	if err != nil {
		t.Fatalf("Begin = %v", err)
	}
	created := s.witnessSingleRow(t)
	if created.State != "WRITING" {
		t.Fatalf("post-Begin row = %+v, want WRITING", created)
	}
	table, artifactID := created.PhysicalTable, created.ArtifactID
	writeOneRow(t, writer)
	artifact, err := writer.Finalize(context.Background())
	if err != nil {
		t.Fatalf("Finalize = %v", err)
	}
	if artifact.Manifest().ArtifactID != artifactID || artifact.Manifest().PhysicalTable != table {
		t.Fatalf("finalized artifact %q/%q does not match the stored row %q/%q",
			artifact.Manifest().ArtifactID, artifact.Manifest().PhysicalTable, artifactID, table)
	}
	if row := s.witnessRow(t, artifactID); row.State != "READY" || row.RowCount != 1 || row.ByteCount <= 0 {
		t.Fatalf("post-Finalize row = %+v, want READY with one row", row)
	}
	if ch.inserts != 1 {
		t.Fatalf("ClickHouse inserts = %d, want 1", ch.inserts)
	}
	if len(ch.verified) != 1 || ch.verified[0] != table {
		t.Fatalf("VerifyOutput calls = %v, want [%s]", ch.verified, table)
	}

	if err := artifact.Release(context.Background()); err != nil {
		t.Fatalf("Release = %v", err)
	}
	if err := artifact.Release(context.Background()); err != nil {
		t.Fatalf("repeat Release = %v, want idempotent", err)
	}
	if got := ch.dropCount(table); got != 1 {
		t.Fatalf("ClickHouse drops of %s = %d, want exactly 1 across repeat Release calls", table, got)
	}
	s.requireNoRow(t, artifactID)
}

func TestDropFailureLeavesReclaimableManifestForReconcile(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	failingCH := &recordingClickHouse{}
	manager := newManager(t, s, failingCH)

	writer, err := manager.Begin(context.Background(), managerIdentity(), managerColumns(t))
	if err != nil {
		t.Fatalf("Begin = %v", err)
	}
	created := s.witnessSingleRow(t)
	table, artifactID := created.PhysicalTable, created.ArtifactID
	failingCH.failDrops(table, 1)
	if err := writer.Abort(context.Background()); err == nil {
		t.Fatal("Abort with an injected drop failure reported success")
	}
	row := s.witnessRow(t, artifactID)
	if row.State != "CLEANUP_PENDING" || row.PhysicalTable != table {
		t.Fatalf("failed drop did not leave a cleanup-pending manifest: %+v", row)
	}
	if _, ok, err := s.catalog.ClaimCleanup(s.ctx, artifactID, "cleanup-early",
		time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)); err != nil || !ok {
		t.Fatalf("reconciler could not claim the failed-drop manifest = (%t, %v)", ok, err)
	}
	if err := s.catalog.Delete(s.ctx, artifactID, "stage-not-owner"); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("non-owner Delete = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.Delete(s.ctx, artifactID, "cleanup-early"); err != nil {
		t.Fatalf("cleanup-owner Delete = %v", err)
	}
	s.requireNoRow(t, artifactID)
	if failingCH.dropCount(table) != 0 {
		t.Fatalf("failed drop was recorded as successful: %d", failingCH.dropCount(table))
	}

	reconcileCH := &recordingClickHouse{}
	reconcileManager := newManager(t, s, reconcileCH)
	writer, err = reconcileManager.Begin(context.Background(), managerIdentity(), managerColumns(t))
	if err != nil {
		t.Fatalf("second Begin = %v", err)
	}
	created = s.witnessSingleRow(t)
	table, artifactID = created.PhysicalTable, created.ArtifactID
	if _, err := s.iso.Exec(s.ctx, "UPDATE "+s.qualified+
		" SET lease_until = $2::timestamptz WHERE artifact_id = $1",
		artifactID, time.Now().UTC().Add(-time.Minute)); err != nil {
		s.fail("expire fixture lease", err)
	}
	if err := reconcileManager.Reconcile(context.Background(), time.Now().UTC().Add(time.Minute), 10); err != nil {
		t.Fatalf("Reconcile = %v", err)
	}
	if got := reconcileCH.dropCount(table); got != 1 {
		t.Fatalf("Reconcile drops of %s = %d, want 1", table, got)
	}
	s.requireNoRow(t, artifactID)
}

func TestReconcileRaceDropsEachExpiredArtifactExactlyOnce(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	now := time.Now().UTC()
	var tables []string
	for _, id := range []string{"race-1", "race-2"} {
		manifest := sampleManifestWithLease(id, now.Add(-time.Minute))
		if err := s.catalog.Create(s.ctx, manifest); err != nil {
			t.Fatalf("Create %s = %v", id, err)
		}
		tables = append(tables, manifest.PhysicalTable)
	}
	leftCH, rightCH := &recordingClickHouse{}, &recordingClickHouse{}
	left, right := newManager(t, s, leftCH), newManager(t, s, rightCH)

	var wg sync.WaitGroup
	start := make(chan struct{})
	reconcile := func(manager *chartifact.Manager) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := manager.Reconcile(context.Background(), now.Add(time.Minute), 10); err != nil {
				s.fail("concurrent reconcile", err)
			}
		}()
	}
	reconcile(left)
	reconcile(right)
	close(start)
	wg.Wait()

	for _, table := range tables {
		if got := leftCH.dropCount(table) + rightCH.dropCount(table); got != 1 {
			t.Fatalf("table %s dropped %d times across replicas, want exactly 1", table, got)
		}
	}
	if leftCH.totalDrops()+rightCH.totalDrops() != len(tables) {
		t.Fatalf("total drops = %d, want %d", leftCH.totalDrops()+rightCH.totalDrops(), len(tables))
	}
	for _, id := range []string{"race-1", "race-2"} {
		s.requireNoRow(t, id)
	}
}
