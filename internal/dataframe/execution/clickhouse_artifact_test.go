package execution

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/store/clickhouse"
)

func TestMaterializeClickHouseArtifactUsesCompleteScopedAQLStream(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	stream := OutputStream{
		Name: "patients", Columns: []string{"name"}, batchSize: 10,
		recipeDigest: strings.Repeat("a", 64), planFingerprint: strings.Repeat("b", 64),
		stageID: "source_projection",
		outputSchema: []lower.CompiledOutputColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: string(expression.RequiredOne), Internal: true, Identity: true},
			{ID: "name-id", Name: "name", Kind: "string", Cardinality: string(expression.RequiredOne)},
		},
		bindings: recipe.RuntimeBindings{
			Project: "project-a", DatasetGeneration: "generation-2", AuthScopeMode: authscope.ReadScopeRestricted,
			AuthResourcePaths: []string{"/programs/a"}, IncludeAuthResourcePath: true,
		},
		stream: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"__loom_row_id": "stable-1", "name": "Ada", "auth_resource_path": "/programs/a"})
		},
	}
	artifact, err := stream.MaterializeClickHouseArtifact(context.Background(), manager, "execution-9", "source_projection", []chartifact.Column{{ID: "name-id", Name: "name", LogicalType: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := artifact.Manifest()
	if manifest.Identity.ExecutionID != "execution-9" || manifest.Identity.RecipeDigest != stream.recipeDigest || manifest.Identity.PlanDigest != stream.planFingerprint || manifest.RowCount != 1 {
		t.Fatalf("private artifact lost its exact source identity: %#v", manifest)
	}
	if got := ch.rows[manifest.PhysicalTable][0]["project_id"]; got != "project-a" {
		t.Fatalf("project identity = %#v, want trusted project", got)
	}
	if err := artifact.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMaterializeClickHouseArtifactRejectsPreviewAndMissingRowScope(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	stream := OutputStream{
		Name: "patients", Columns: []string{"name"}, queryLimit: 10,
		recipeDigest: strings.Repeat("a", 64), planFingerprint: strings.Repeat("b", 64),
		stageID: "source_projection",
		outputSchema: []lower.CompiledOutputColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: string(expression.RequiredOne), Internal: true, Identity: true},
			{ID: "name-id", Name: "name", Kind: "string", Cardinality: string(expression.RequiredOne)},
		},
		bindings: recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-2", AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/a"}, IncludeAuthResourcePath: true},
		stream:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	}
	columns := []chartifact.Column{{ID: "name-id", Name: "name", LogicalType: "string"}}
	if _, err := stream.MaterializeClickHouseArtifact(context.Background(), manager, "execution-9", "source_projection", columns); err == nil {
		t.Fatal("private artifact accepted a bounded preview")
	}
	stream.queryLimit = 0
	stream.bindings.IncludeAuthResourcePath = false
	if _, err := stream.MaterializeClickHouseArtifact(context.Background(), manager, "execution-9", "source_projection", columns); err == nil {
		t.Fatal("private artifact accepted restricted scope without row-level paths")
	}
	if len(catalog.items) != 0 {
		t.Fatalf("rejected source stream created %d artifacts", len(catalog.items))
	}
}

func TestWithPrivateClickHouseArtifactHoldsPublishedPinsThroughCombineAndCleanup(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	stream := OutputStream{
		Name: "patients", Columns: []string{"name"},
		recipeDigest: strings.Repeat("a", 64), planFingerprint: strings.Repeat("b", 64), stageID: "source_projection",
		outputSchema: []lower.CompiledOutputColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: string(expression.RequiredOne), Internal: true, Identity: true},
			{ID: "name-id", Name: "name", Kind: "string", Cardinality: string(expression.RequiredOne)},
		},
		bindings: recipe.RuntimeBindings{
			Project: "project-a", DatasetGeneration: "generation-2", AuthScopeMode: authscope.ReadScopeRestricted,
			AuthResourcePaths: []string{"/programs/a"}, IncludeAuthResourcePath: true,
		},
		stream: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			if !pinned {
				return fmt.Errorf("AQL stream ran outside published input pins")
			}
			return visit(map[string]any{"__loom_row_id": "stable-1", "name": "Ada", "auth_resource_path": "/programs/a"})
		},
	}
	withPins := func(ctx context.Context, revisions []string, visit func(context.Context) error) error {
		if !reflect.DeepEqual(revisions, []string{"published-1", "published-2"}) {
			return fmt.Errorf("wrong exact revisions pinned: %#v", revisions)
		}
		pinned = true
		defer func() { pinned = false }()
		if err := visit(ctx); err != nil {
			return err
		}
		if len(catalog.items) != 0 {
			return fmt.Errorf("private artifact remained when pinned execution callback returned")
		}
		return nil
	}
	consumed := false
	err = stream.WithPrivateClickHouseArtifact(context.Background(), manager, "execution-9", "source_projection", []chartifact.Column{{ID: "name-id", Name: "name", LogicalType: "string"}}, []string{"published-1", "published-2"}, withPins, func(ctx context.Context, artifact *chartifact.Artifact) error {
		if !pinned {
			return fmt.Errorf("ClickHouse combine ran outside published input pins")
		}
		if err := artifact.CheckLease(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		consumed = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !consumed || pinned {
		t.Fatalf("consume/pin lifecycle = consumed %t, pinned %t", consumed, pinned)
	}
}

type testArtifactCatalog struct {
	mu    sync.Mutex
	items map[string]chartifact.Manifest
}

func (c *testArtifactCatalog) Create(_ context.Context, value chartifact.Manifest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[value.ArtifactID] = value
	return nil
}

func (c *testArtifactCatalog) Update(_ context.Context, id, owner string, progress chartifact.Progress) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return chartifact.ErrLeaseLost
	}
	value.State, value.RowCount, value.ByteCount, value.UpdatedAt = progress.State, progress.RowCount, progress.ByteCount, progress.UpdatedAt
	if progress.LeaseUntil.After(value.LeaseUntil) || progress.State == chartifact.StateCleanupPending {
		value.LeaseUntil = progress.LeaseUntil
	}
	c.items[id] = value
	return nil
}

func (c *testArtifactCatalog) Renew(_ context.Context, id, owner string, until time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return false, nil
	}
	if until.After(value.LeaseUntil) {
		value.LeaseUntil = until
		c.items[id] = value
	}
	return true, nil
}

func (c *testArtifactCatalog) ListExpired(_ context.Context, before time.Time, limit int) ([]chartifact.Manifest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]chartifact.Manifest, 0)
	for _, item := range c.items {
		if !item.LeaseUntil.After(before) {
			result = append(result, item)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (c *testArtifactCatalog) ClaimCleanup(_ context.Context, id, owner string, expiredBefore, until time.Time) (chartifact.Manifest, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseUntil.After(expiredBefore) {
		return chartifact.Manifest{}, false, nil
	}
	value.LeaseOwner, value.LeaseUntil, value.State = owner, until, chartifact.StateCleanupPending
	c.items[id] = value
	return value, true, nil
}

func (c *testArtifactCatalog) Delete(_ context.Context, id, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return chartifact.ErrLeaseLost
	}
	delete(c.items, id)
	return nil
}

func (c *testArtifactCatalog) ReleaseCleanup(_ context.Context, id, owner string, until time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return chartifact.ErrLeaseLost
	}
	value.LeaseOwner, value.LeaseUntil = "", until
	c.items[id] = value
	return nil
}

type testArtifactClickHouse struct {
	mu      sync.Mutex
	columns map[string][]clickhouse.Column
	rows    map[string][]map[string]any
}

func (c *testArtifactClickHouse) CreateTable(_ context.Context, table string, columns []clickhouse.Column) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.columns[table] = append([]clickhouse.Column(nil), columns...)
	c.rows[table] = nil
	return nil
}

func (c *testArtifactClickHouse) InsertRows(_ context.Context, table string, columns []clickhouse.Column, rows []map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.columns[table], columns) {
		return fmt.Errorf("unexpected insert schema")
	}
	for _, row := range rows {
		copy := make(map[string]any, len(row))
		for key, value := range row {
			copy[key] = value
		}
		c.rows[table] = append(c.rows[table], copy)
	}
	return nil
}

func (c *testArtifactClickHouse) VerifyOutput(_ context.Context, table string, columns []clickhouse.Column, expected int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.columns[table], columns) || int64(len(c.rows[table])) != expected {
		return fmt.Errorf("artifact verification mismatch")
	}
	return nil
}

func (c *testArtifactClickHouse) DropTable(_ context.Context, table string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.columns, table)
	delete(c.rows, table)
	return nil
}
