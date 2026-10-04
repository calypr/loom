package chartifact

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/store/clickhouse"
)

func TestArtifactStreamsBoundedRowsWithExactSchemaAndScope(t *testing.T) {
	catalog, ch := newMemoryCatalog(), newMemoryClickHouse()
	manager, err := New(Config{Catalog: catalog, ClickHouse: ch, BatchRows: 2, BatchBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	columns, err := ColumnsFromCompiledOutput([]lower.CompiledOutputColumn{
		{ID: "row", Name: rowIDColumn, Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true, Identity: true},
		{ID: "name-id", Name: "name", SemanticPath: "Patient.name", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne)},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity()
	writer, err := manager.Begin(context.Background(), identity, columns)
	if err != nil {
		t.Fatal(err)
	}
	first := map[string]any{"__loom_row_id": "row-1", "name": "Ada", "auth_resource_path": "/programs/a", "project_id": "spoofed"}
	if err := writer.Write(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	first["name"] = "mutated-after-write"
	if len(ch.batches) != 0 {
		t.Fatalf("batch count before threshold = %d, want 0", len(ch.batches))
	}
	if err := writer.Write(context.Background(), map[string]any{"__loom_row_id": "row-2", "name": "Grace", "auth_resource_path": "/programs/b"}); err != nil {
		t.Fatal(err)
	}
	if len(ch.batches) != 1 || len(ch.batches[0]) != 2 {
		t.Fatalf("bounded batches = %#v, want one two-row batch", ch.batches)
	}
	if got := ch.rows[writer.manifest.PhysicalTable][0]["name"]; got != "Ada" {
		t.Fatalf("buffer retained caller mutation: name = %#v", got)
	}
	if got := ch.rows[writer.manifest.PhysicalTable][0][projectIDColumn]; got != identity.Project {
		t.Fatalf("project metadata = %#v, want trusted project %q", got, identity.Project)
	}
	artifact, err := writer.Finalize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := artifact.Manifest()
	if manifest.State != StateReady || manifest.RowCount != 2 || manifest.Identity.Project != identity.Project || manifest.Identity.DatasetGeneration != identity.DatasetGeneration {
		t.Fatalf("artifact manifest identity/state = %#v", manifest)
	}
	if !strings.HasPrefix(manifest.PhysicalTable, "loom_private_stage_") || !reflect.DeepEqual(manifest.Identity.AuthResourcePaths, identity.AuthResourcePaths) {
		t.Fatalf("artifact table/scope = %q %#v", manifest.PhysicalTable, manifest.Identity.AuthResourcePaths)
	}
	if manifest.Identity.SchemaDigest == "" || manifest.Identity.ScopeDigest != scopeDigest(manifest.Identity) {
		t.Fatalf("artifact digest identity = %#v", manifest.Identity)
	}
	if err := artifact.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, exists := ch.rows[manifest.PhysicalTable]; exists {
		t.Fatalf("private artifact table %q survived release", manifest.PhysicalTable)
	}
	if _, exists := catalog.items[manifest.ArtifactID]; exists {
		t.Fatalf("private artifact manifest %q survived release", manifest.ArtifactID)
	}
}

func TestArtifactRejectsScopeSchemaAndPreviewMismatches(t *testing.T) {
	catalog, ch := newMemoryCatalog(), newMemoryClickHouse()
	manager, err := New(Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	columns := []Column{{ID: "name-id", Name: "name", LogicalType: "string"}}
	identity := testIdentity()
	identity.ScopeDigest = strings.Repeat("0", 64)
	if _, err := manager.Begin(context.Background(), identity, columns); err == nil {
		t.Fatal("Begin accepted an incorrect scope digest")
	}
	identity = testIdentity()
	identity.OutputID = ""
	if _, err := manager.Begin(context.Background(), identity, columns); err == nil {
		t.Fatal("Begin accepted an artifact without an exact output ID")
	}
	identity = testIdentity()
	identity.SchemaDigest = strings.Repeat("0", 64)
	if _, err := manager.Begin(context.Background(), identity, columns); err == nil {
		t.Fatal("Begin accepted an incorrect schema digest")
	}
	writer, err := manager.Begin(context.Background(), testIdentity(), columns)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []map[string]any{
		{"__loom_row_id": "row-1", "name": "Ada"},
		{"__loom_row_id": "row-2", "name": "Grace", "auth_resource_path": "/outside"},
		{"__loom_row_id": "row-3", "name": "Lin", "auth_resource_path": "/programs/a", "extra": true},
		{"__loom_row_id": "row-4", "name": map[string]any{"unexpected": true}, "auth_resource_path": "/programs/a"},
	} {
		if err := writer.Write(context.Background(), row); err == nil {
			t.Fatalf("Write accepted invalid row %#v", row)
		}
	}
	if err := writer.Abort(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ch.batches) != 0 {
		t.Fatalf("invalid rows wrote %d batches", len(ch.batches))
	}
	if _, err := NormalizeSchema([]Column{{ID: "x", Name: "payload", LogicalType: "object"}}); err == nil {
		t.Fatal("NormalizeSchema accepted an unsupported object type")
	}
	if _, err := ColumnsFromCompiledOutput([]lower.CompiledOutputColumn{{Name: rowIDColumn, Kind: string(expression.KindObject), Identity: true}}); err == nil {
		t.Fatal("ColumnsFromCompiledOutput accepted a structured row identity that this boundary cannot encode")
	}
	arraySchema, err := NormalizeSchema([]Column{{ID: "tags-id", Name: "tags", LogicalType: "string", Repeated: true, Nullable: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := arraySchema[1].ClickHouseType; got != "Array(String)" {
		t.Fatalf("nullable repeated logical field type = %q, want publication-compatible Array(String)", got)
	}
}

func TestWriterFlushesBeforeByteLimitWouldBeExceeded(t *testing.T) {
	catalog, ch := newMemoryCatalog(), newMemoryClickHouse()
	manager, err := New(Config{Catalog: catalog, ClickHouse: ch, BatchRows: 100, BatchBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := manager.Begin(context.Background(), testIdentity(), []Column{{ID: "value-id", Name: "value", LogicalType: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"__loom_row_id": "row-1", "value": strings.Repeat("a", 80), "auth_resource_path": "/programs/a"},
		{"__loom_row_id": "row-2", "value": strings.Repeat("b", 80), "auth_resource_path": "/programs/b"},
	}
	_, firstSize, err := writer.validateRow(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	_, secondSize, err := writer.validateRow(rows[1])
	if err != nil {
		t.Fatal(err)
	}
	manager.batchBytes = firstSize + secondSize - 1
	if err := writer.Write(context.Background(), rows[0]); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(context.Background(), rows[1]); err != nil {
		t.Fatal(err)
	}
	if len(ch.batches) != 1 || len(ch.batches[0]) != 1 {
		t.Fatalf("byte bound did not flush before appending the second row: %#v", ch.batches)
	}
	artifact, err := writer.Finalize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.batches) != 2 || len(ch.batches[1]) != 1 {
		t.Fatalf("final bounded batches = %#v, want two single-row batches", ch.batches)
	}
	if err := artifact.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileCleansExpiredArtifactsAndRetriesFailedDrop(t *testing.T) {
	catalog, ch := newMemoryCatalog(), newMemoryClickHouse()
	manager, err := New(Config{Catalog: catalog, ClickHouse: ch, LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := manager.Begin(context.Background(), testIdentity(), []Column{{ID: "name-id", Name: "name", LogicalType: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := writer.Finalize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manifest := artifact.Manifest()
	ch.dropErr = errors.New("temporary drop failure")
	if err := artifact.Release(context.Background()); err == nil {
		t.Fatal("Release succeeded despite ClickHouse drop failure")
	}
	ch.dropErr = nil
	if err := manager.Reconcile(context.Background(), time.Now().UTC().Add(time.Second), 20); err != nil {
		t.Fatal(err)
	}
	if _, exists := ch.rows[manifest.PhysicalTable]; exists {
		t.Fatalf("reconciler left private table %q", manifest.PhysicalTable)
	}
	if _, exists := catalog.items[manifest.ArtifactID]; exists {
		t.Fatalf("reconciler left manifest %q", manifest.ArtifactID)
	}
}

func TestReconcileClaimsExpiredCreatingArtifact(t *testing.T) {
	catalog, ch := newMemoryCatalog(), newMemoryClickHouse()
	manager, err := New(Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := manager.Begin(context.Background(), testIdentity(), []Column{{ID: "name-id", Name: "name", LogicalType: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := writer.manifest
	writer.lease.stop() // simulate a crashed process; the durable lease expires below
	catalog.expire(manifest.ArtifactID, time.Now().UTC().Add(-time.Minute))
	if err := manager.Reconcile(context.Background(), time.Now().UTC(), 20); err != nil {
		t.Fatal(err)
	}
	if _, exists := ch.rows[manifest.PhysicalTable]; exists {
		t.Fatalf("reconciler left expired creating table %q", manifest.PhysicalTable)
	}
	if _, exists := catalog.items[manifest.ArtifactID]; exists {
		t.Fatalf("reconciler left expired creating manifest %q", manifest.ArtifactID)
	}
}

func testIdentity() Identity {
	return Identity{
		ExecutionID: "execution-1", OutputID: "patients", StageID: "source_projection", Project: "project-a", DatasetGeneration: "generation-4",
		RecipeDigest: strings.Repeat("a", 64), PlanDigest: strings.Repeat("b", 64),
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/a", "/programs/b"},
	}
}

type memoryCatalog struct {
	mu    sync.Mutex
	items map[string]Manifest
}

func newMemoryCatalog() *memoryCatalog { return &memoryCatalog{items: map[string]Manifest{}} }

func (c *memoryCatalog) Create(_ context.Context, manifest Manifest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[manifest.ArtifactID]; exists {
		return fmt.Errorf("artifact already exists")
	}
	c.items[manifest.ArtifactID] = cloneManifest(manifest)
	return nil
}

func (c *memoryCatalog) Update(_ context.Context, id, owner string, progress Progress) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest, ok := c.items[id]
	if !ok || manifest.LeaseOwner != owner {
		return ErrLeaseLost
	}
	manifest.State = progress.State
	manifest.RowCount, manifest.ByteCount = progress.RowCount, progress.ByteCount
	manifest.UpdatedAt = progress.UpdatedAt
	if progress.State == StateCleanupPending || progress.LeaseUntil.After(manifest.LeaseUntil) {
		manifest.LeaseUntil = progress.LeaseUntil
	}
	c.items[id] = manifest
	return nil
}

func (c *memoryCatalog) Renew(_ context.Context, id, owner string, until time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest, ok := c.items[id]
	if !ok || manifest.LeaseOwner != owner || time.Now().UTC().After(manifest.LeaseUntil) {
		return false, nil
	}
	if until.After(manifest.LeaseUntil) {
		manifest.LeaseUntil = until
		manifest.UpdatedAt = time.Now().UTC()
		c.items[id] = manifest
	}
	return true, nil
}

func (c *memoryCatalog) ListExpired(_ context.Context, before time.Time, limit int) ([]Manifest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]Manifest, 0)
	for _, manifest := range c.items {
		if !manifest.LeaseUntil.After(before) {
			result = append(result, cloneManifest(manifest))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ArtifactID < result[j].ArtifactID })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (c *memoryCatalog) ClaimCleanup(_ context.Context, id, owner string, expiredBefore, until time.Time) (Manifest, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest, ok := c.items[id]
	if !ok || manifest.LeaseUntil.After(expiredBefore) {
		return Manifest{}, false, nil
	}
	manifest.LeaseOwner, manifest.LeaseUntil = owner, until
	manifest.State, manifest.UpdatedAt = StateCleanupPending, time.Now().UTC()
	c.items[id] = manifest
	return cloneManifest(manifest), true, nil
}

func (c *memoryCatalog) Delete(_ context.Context, id, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest, ok := c.items[id]
	if !ok || manifest.LeaseOwner != owner {
		return ErrLeaseLost
	}
	delete(c.items, id)
	return nil
}

func (c *memoryCatalog) ReleaseCleanup(_ context.Context, id, owner string, until time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest, ok := c.items[id]
	if !ok || manifest.LeaseOwner != owner {
		return ErrLeaseLost
	}
	manifest.LeaseOwner, manifest.LeaseUntil, manifest.UpdatedAt = "", until, time.Now().UTC()
	manifest.State = StateCleanupPending
	c.items[id] = manifest
	return nil
}

func (c *memoryCatalog) expire(id string, until time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	manifest := c.items[id]
	manifest.LeaseUntil = until
	c.items[id] = manifest
}

type memoryClickHouse struct {
	mu      sync.Mutex
	columns map[string][]clickhouse.Column
	rows    map[string][]map[string]any
	batches [][]map[string]any
	dropErr error
}

func newMemoryClickHouse() *memoryClickHouse {
	return &memoryClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
}

func (c *memoryClickHouse) CreateTable(_ context.Context, table string, columns []clickhouse.Column) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.columns[table]; exists {
		return fmt.Errorf("table already exists")
	}
	c.columns[table] = append([]clickhouse.Column(nil), columns...)
	c.rows[table] = nil
	return nil
}

func (c *memoryClickHouse) InsertRows(_ context.Context, table string, columns []clickhouse.Column, rows []map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.columns[table], columns) {
		return fmt.Errorf("insert schema mismatch")
	}
	batch := make([]map[string]any, len(rows))
	for index, row := range rows {
		batch[index] = cloneValue(row).(map[string]any)
		c.rows[table] = append(c.rows[table], batch[index])
	}
	c.batches = append(c.batches, batch)
	return nil
}

func (c *memoryClickHouse) VerifyOutput(_ context.Context, table string, columns []clickhouse.Column, expectedRows int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.columns[table], columns) || int64(len(c.rows[table])) != expectedRows {
		return fmt.Errorf("private artifact verification mismatch")
	}
	return nil
}

func (c *memoryClickHouse) DropTable(_ context.Context, table string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dropErr != nil {
		return c.dropErr
	}
	delete(c.columns, table)
	delete(c.rows, table)
	return nil
}
