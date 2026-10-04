package execution

import (
	"context"
	"errors"
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
	if manifest.Identity.ExecutionID != "execution-9" || manifest.Identity.OutputID != stream.Name || manifest.Identity.RecipeDigest != stream.recipeDigest || manifest.Identity.PlanDigest != stream.planFingerprint || manifest.RowCount != 1 {
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

func TestWithPrivateClickHouseArtifactsCaptureOrderedWorkspaceOutputsWithoutPins(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	pinsCalled := false
	err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", []PrivateClickHouseArtifactSource{
		privateCaptureSource("patients", "Ada", "b"), privateCaptureSource("observations", "registered", "b"),
	}, nil, func(context.Context, []string, func(context.Context) error) error {
		pinsCalled = true
		return errors.New("all-workspace inputs must not request published pins")
	}, func(ctx context.Context, inputs []PrivateClickHouseArtifactInput) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if got := []string{inputs[0].OutputID, inputs[1].OutputID}; !reflect.DeepEqual(got, []string{"patients", "observations"}) {
			return fmt.Errorf("captured source order = %#v", got)
		}
		if len(catalog.items) != 2 {
			return fmt.Errorf("consumer sees %d live artifacts, want two", len(catalog.items))
		}
		for index, input := range inputs {
			if err := input.Artifact.CheckLease(); err != nil {
				return fmt.Errorf("input %d lease: %w", index, err)
			}
			manifest := input.Artifact.Manifest()
			if got := manifest.Identity.ExecutionID; got != "execution-9" {
				return fmt.Errorf("input %d execution identity = %q", index, got)
			}
			if manifest.Identity.OutputID != input.OutputID || manifest.Identity.StageID != "source_projection" || manifest.Identity.PlanDigest != strings.Repeat("b", 64) {
				return fmt.Errorf("input %d exact artifact identity = %#v", index, manifest.Identity)
			}
			if manifest.Identity.SchemaDigest != inputs[0].Artifact.Manifest().Identity.SchemaDigest {
				return fmt.Errorf("same-stage outputs unexpectedly changed schema identity: %q != %q", manifest.Identity.SchemaDigest, inputs[0].Artifact.Manifest().Identity.SchemaDigest)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pinsCalled || len(catalog.items) != 0 {
		t.Fatalf("all-workspace pin/cleanup state: pinsCalled=%t artifacts=%d", pinsCalled, len(catalog.items))
	}
}

func TestWithPrivateClickHouseArtifactsPinPublishedInputsThroughAllCaptures(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	withPins := func(ctx context.Context, revisions []string, visit func(context.Context) error) error {
		if !reflect.DeepEqual(revisions, []string{"published-a", "published-b"}) {
			return fmt.Errorf("wrong exact revisions pinned: %#v", revisions)
		}
		pinned = true
		defer func() { pinned = false }()
		err := visit(ctx)
		if len(catalog.items) != 0 {
			return errors.Join(err, fmt.Errorf("private captures remained before published pins released: %d", len(catalog.items)))
		}
		return err
	}
	sources := []PrivateClickHouseArtifactSource{privateCaptureSource("patients", "Ada", "b"), privateCaptureSource("observations", "registered", "c")}
	for index := range sources {
		outputID := sources[index].OutputID
		sources[index].Stream.stream = func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			if !pinned {
				return fmt.Errorf("workspace output %q streamed outside published pins", outputID)
			}
			return visit(privateCaptureRow(outputID))
		}
	}
	consumeCalled := false
	err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", sources,
		[]string{"published-a", "published-b"}, withPins,
		func(ctx context.Context, inputs []PrivateClickHouseArtifactInput) error {
			if !pinned || ctx.Err() != nil || len(inputs) != 2 || len(catalog.items) != 2 {
				return fmt.Errorf("consumer ran without pins/leases: pinned=%t inputs=%d live=%d", pinned, len(inputs), len(catalog.items))
			}
			consumeCalled = true
			return nil
		})
	if err != nil || !consumeCalled || pinned || len(catalog.items) != 0 {
		t.Fatalf("published pin lifecycle: error=%v consume=%t pinned=%t artifacts=%d", err, consumeCalled, pinned, len(catalog.items))
	}
}

func TestWithPrivateClickHouseArtifactsCleansEarlierCaptureAfterSecondFailure(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	sources := []PrivateClickHouseArtifactSource{privateCaptureSource("patients", "Ada", "b"), privateCaptureSource("observations", "registered", "c")}
	sources[1].Stream.stream = func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		return errors.New("second source failed")
	}
	consumeCalled := false
	err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", sources, nil, nil,
		func(context.Context, []PrivateClickHouseArtifactInput) error {
			consumeCalled = true
			return nil
		})
	if err == nil || !strings.Contains(err.Error(), "second source failed") || consumeCalled || len(catalog.items) != 0 {
		t.Fatalf("second-source failure cleanup = %v consume=%t artifacts=%d", err, consumeCalled, len(catalog.items))
	}
}

func TestWithPrivateClickHouseArtifactsAcceptsOneAllDraftSource(t *testing.T) {
	catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
	if err != nil {
		t.Fatal(err)
	}
	consumed := false
	err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", []PrivateClickHouseArtifactSource{
		privateCaptureSource("patients", "Ada", "b"),
	}, nil, nil, func(_ context.Context, inputs []PrivateClickHouseArtifactInput) error {
		consumed = true
		if len(inputs) != 1 || inputs[0].OutputID != "patients" || inputs[0].Artifact.Manifest().Identity.OutputID != "patients" {
			return fmt.Errorf("single-source capture identity/order = %#v", inputs)
		}
		return nil
	})
	if err != nil || !consumed || len(catalog.items) != 0 {
		t.Fatalf("single all-draft source: error=%v consumed=%t liveArtifacts=%d", err, consumed, len(catalog.items))
	}
}

func TestWithPrivateClickHouseArtifactsRejectsDuplicateIDsAndContextMismatch(t *testing.T) {
	t.Run("duplicate output IDs", func(t *testing.T) {
		catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
		manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
		if err != nil {
			t.Fatal(err)
		}
		source := privateCaptureSource("patients", "Ada", "b")
		err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", []PrivateClickHouseArtifactSource{source, source}, nil, nil,
			func(context.Context, []PrivateClickHouseArtifactInput) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "unique exact output ID") || len(catalog.items) != 0 {
			t.Fatalf("duplicate source identity accepted: error=%v artifacts=%d", err, len(catalog.items))
		}
	})
	t.Run("recipe, project, generation, and authorization scope must match", func(t *testing.T) {
		mutations := map[string]func(*OutputStream){
			"recipe":     func(stream *OutputStream) { stream.recipeDigest = strings.Repeat("d", 64) },
			"project":    func(stream *OutputStream) { stream.bindings.Project = "project-b" },
			"generation": func(stream *OutputStream) { stream.bindings.DatasetGeneration = "generation-other" },
			"scope":      func(stream *OutputStream) { stream.bindings.AuthResourcePaths = []string{"/programs/other"} },
		}
		for name, mutate := range mutations {
			t.Run(name, func(t *testing.T) {
				catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
				manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
				if err != nil {
					t.Fatal(err)
				}
				second := privateCaptureSource("observations", "registered", "b")
				mutate(&second.Stream)
				err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", []PrivateClickHouseArtifactSource{
					privateCaptureSource("patients", "Ada", "b"), second,
				}, nil, nil, func(context.Context, []PrivateClickHouseArtifactInput) error { return nil })
				if err == nil || !strings.Contains(err.Error(), "differs from the first source") || len(catalog.items) != 0 {
					t.Fatalf("mismatched %s context accepted: error=%v artifacts=%d", name, err, len(catalog.items))
				}
			})
		}
	})
}

func TestWithPrivateClickHouseArtifactsCleansCapturesAfterCancellationAndConsumerError(t *testing.T) {
	t.Run("cancellation while capturing second source", func(t *testing.T) {
		catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
		manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sources := []PrivateClickHouseArtifactSource{privateCaptureSource("patients", "Ada", "b"), privateCaptureSource("observations", "registered", "c")}
		sources[1].Stream.stream = func(ctx context.Context, _ string, _ int, _ map[string]any, _ func(map[string]any) error) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		}
		consumeCalled := false
		err = WithPrivateClickHouseArtifacts(ctx, manager, "execution-9", sources, nil, nil,
			func(context.Context, []PrivateClickHouseArtifactInput) error { consumeCalled = true; return nil })
		if !errors.Is(err, context.Canceled) || consumeCalled || len(catalog.items) != 0 {
			t.Fatalf("cancellation cleanup = %v consume=%t artifacts=%d", err, consumeCalled, len(catalog.items))
		}
	})

	t.Run("consumer failure", func(t *testing.T) {
		catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
		manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
		if err != nil {
			t.Fatal(err)
		}
		sentinel := errors.New("combine query failed")
		err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", []PrivateClickHouseArtifactSource{
			privateCaptureSource("patients", "Ada", "b"), privateCaptureSource("observations", "registered", "c"),
		}, nil, nil, func(_ context.Context, inputs []PrivateClickHouseArtifactInput) error {
			if len(inputs) != 2 || len(catalog.items) != 2 {
				t.Fatalf("consumer sees %d inputs and %d artifacts", len(inputs), len(catalog.items))
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || len(catalog.items) != 0 {
			t.Fatalf("consumer failure cleanup = %v artifacts=%d", err, len(catalog.items))
		}
	})
}

func TestWithPrivateClickHouseArtifactsLeaseLossCancelsNextSourceStream(t *testing.T) {
	catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
	ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, LeaseTTL: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	sources := []PrivateClickHouseArtifactSource{privateCaptureSource("patients", "Ada", "b"), privateCaptureSource("observations", "registered", "c")}
	sources[1].Stream.stream = func(ctx context.Context, _ string, _ int, _ map[string]any, _ func(map[string]any) error) error {
		catalog.mu.Lock()
		if len(catalog.createdIDs) == 0 {
			catalog.mu.Unlock()
			return errors.New("first capture was not created")
		}
		catalog.failRenewID = catalog.createdIDs[0]
		catalog.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return errors.New("first capture lease loss did not cancel the next source stream")
		}
	}
	err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", sources, nil, nil,
		func(context.Context, []PrivateClickHouseArtifactInput) error {
			return errors.New("consumer ran after capture lease loss")
		})
	if !errors.Is(err, chartifact.ErrLeaseLost) || len(catalog.items) != 0 {
		t.Fatalf("lease-loss cleanup = %v artifacts=%d", err, len(catalog.items))
	}
}

func TestWithPrivateClickHouseArtifactsRechecksLeaseAndContextAfterConsumer(t *testing.T) {
	t.Run("lease loss", func(t *testing.T) {
		catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
		ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
		manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, LeaseTTL: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		err = WithPrivateClickHouseArtifacts(context.Background(), manager, "execution-9", []PrivateClickHouseArtifactSource{
			privateCaptureSource("patients", "Ada", "b"),
		}, nil, nil, func(ctx context.Context, inputs []PrivateClickHouseArtifactInput) error {
			catalog.mu.Lock()
			catalog.failRenewID = catalog.createdIDs[0]
			catalog.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
				return errors.New("lease loss did not cancel consumer context")
			}
		})
		if !errors.Is(err, chartifact.ErrLeaseLost) || len(catalog.items) != 0 {
			t.Fatalf("consumer returned nil after lease loss: error=%v liveArtifacts=%d", err, len(catalog.items))
		}
	})
	t.Run("consumer cancels parent but returns nil", func(t *testing.T) {
		catalog, ch := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}, &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
		manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err = WithPrivateClickHouseArtifacts(ctx, manager, "execution-9", []PrivateClickHouseArtifactSource{
			privateCaptureSource("patients", "Ada", "b"),
		}, nil, nil, func(context.Context, []PrivateClickHouseArtifactInput) error {
			cancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || len(catalog.items) != 0 {
			t.Fatalf("consumer hid parent cancellation: error=%v liveArtifacts=%d", err, len(catalog.items))
		}
	})
}

func privateCaptureSource(outputID, value, fingerprint string) PrivateClickHouseArtifactSource {
	columnID := "shared-value-id"
	return PrivateClickHouseArtifactSource{
		OutputID: outputID,
		Columns:  []chartifact.Column{{ID: columnID, Name: "value", LogicalType: "string"}},
		Stream: OutputStream{
			Name: outputID, Columns: []string{"value"}, recipeDigest: strings.Repeat("a", 64),
			planFingerprint: strings.Repeat(fingerprint, 64), stageID: "source_projection",
			outputSchema: []lower.CompiledOutputColumn{
				{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: string(expression.RequiredOne), Internal: true, Identity: true},
				{ID: columnID, Name: "value", Kind: "string", Cardinality: string(expression.RequiredOne)},
			},
			bindings: recipe.RuntimeBindings{
				Project: "project-a", DatasetGeneration: "generation-2", AuthScopeMode: authscope.ReadScopeRestricted,
				AuthResourcePaths: []string{"/programs/a"}, IncludeAuthResourcePath: true,
			},
			stream: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				return visit(privateCaptureRow(value))
			},
		},
	}
}

func privateCaptureRow(value string) map[string]any {
	return map[string]any{"__loom_row_id": "stable-" + value, "value": value, "auth_resource_path": "/programs/a"}
}

type testArtifactCatalog struct {
	mu          sync.Mutex
	items       map[string]chartifact.Manifest
	createdIDs  []string
	failRenewID string
}

func (c *testArtifactCatalog) Create(_ context.Context, value chartifact.Manifest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[value.ArtifactID] = value
	c.createdIDs = append(c.createdIDs, value.ArtifactID)
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
	if id == c.failRenewID {
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
