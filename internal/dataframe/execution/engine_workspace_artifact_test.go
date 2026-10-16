package execution

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/store/clickhouse"
)

func TestEngineStreamsNestedWorkspaceCombinesWithOnlyTerminalLimit(t *testing.T) {
	catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
	ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	leafA := workspaceArtifactTestOutput("leaf-a", []ir.PhysicalCombineInputRef{
		{TableID: "table_a", RevisionID: "revision_a", OutputID: "out_a"},
		{TableID: "table_b", RevisionID: "revision_b", OutputID: "out_b"},
	})
	leafB := workspaceArtifactTestOutput("leaf-b", []ir.PhysicalCombineInputRef{
		{TableID: "table_c", RevisionID: "revision_c", OutputID: "out_c"},
		{TableID: "table_d", RevisionID: "revision_d", OutputID: "out_d"},
	})
	inner := workspaceArtifactTestOutput("inner", []ir.PhysicalCombineInputRef{
		{WorkspaceOutputID: "leaf-a"}, {WorkspaceOutputID: "leaf-b"},
	})
	terminal := workspaceArtifactTestOutput("terminal", []ir.PhysicalCombineInputRef{
		{WorkspaceOutputID: "inner"}, {WorkspaceOutputID: "leaf-b"},
	})
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	var pinCalls, resolveCalls int
	var privateQueryCalls int
	var queries []string
	engine := &Engine{
		privateClickHouseArtifacts: manager,
		withExecutionReadPins: func(_ context.Context, revisions []string, visit func(context.Context) error) error {
			if !reflect.DeepEqual(revisions, []string{"revision_a", "revision_b"}) &&
				!reflect.DeepEqual(revisions, []string{"revision_c", "revision_d"}) {
				return fmt.Errorf("pinned revisions = %#v", revisions)
			}
			pinCalls++
			return visit(context.Background())
		},
		resolveClickHouseInputs: func(_ context.Context, plan ir.PhysicalClickHouseCombine, gotBindings recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			resolveCalls++
			if gotBindings.Project != bindings.Project || gotBindings.DatasetGeneration != bindings.DatasetGeneration {
				return nil, fmt.Errorf("published input resolution lost exact project/generation")
			}
			resolved := make([]ir.ResolvedClickHouseTable, 0, len(plan.Inputs))
			for index, input := range plan.Inputs {
				if input.WorkspaceOutputID != "" {
					return nil, fmt.Errorf("published resolver received workspace reference %q", input.WorkspaceOutputID)
				}
				resolved = append(resolved, ir.ResolvedClickHouseTable{
					TableID: input.TableID, RevisionID: input.RevisionID, OutputID: input.OutputID,
					Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
					ReceiptID: "receipt-" + input.RevisionID, SchemaDigest: "schema-" + input.RevisionID,
					ScopeDigest: "scope-unrestricted", PhysicalTable: fmt.Sprintf("published_%d", index), Unrestricted: true,
					Columns: []ir.ResolvedClickHouseColumn{
						{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
						{ID: "value-id", Name: "value", LogicalType: "string", ClickHouseType: "String"},
					},
				})
			}
			return resolved, nil
		},
		clickHouseQueryRows: func(ctx context.Context, query string, columns []string, visit func(map[string]any) error, _ ...any) error {
			queries = append(queries, query)
			if !reflect.DeepEqual(columns, []string{"__loom_row_id", "value", "auth_resource_path"}) {
				return fmt.Errorf("ClickHouse result columns = %#v", columns)
			}
			if strings.Contains(query, "loom_private_") {
				privateQueryCalls++
				want := 3 // the nested output's writer is live while its two inputs are queried.
				if privateQueryCalls%2 == 0 {
					want = 2 // the terminal query sees its two completed source captures.
				}
				if len(catalog.items) != want {
					return fmt.Errorf("private query %d sees %d live captures, want %d", privateQueryCalls, len(catalog.items), want)
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return visit(map[string]any{"__loom_row_id": fmt.Sprintf("query-row-%d", len(queries)), "value": "result", "auth_resource_path": ""})
		},
	}
	resolved := Resolved{
		StoredRecipeDigest: strings.Repeat("a", 64),
		Semantic:           semantic.ResolvedRecipePlan{SemanticPlan: semantic.RecipePlan{Bindings: bindings}},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{terminal}, WorkspaceDependencies: []lower.CompiledRecipeOutput{inner, leafA, leafB}},
	}
	stream, _, err := engine.streamForOutput(resolved, "terminal", 3)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	result, err := stream.Stream(context.Background(), func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("nested workspace output stream: %v", err)
	}
	if result.RowCount != 1 || len(rows) != 1 || rows[0]["value"] != "result" {
		t.Fatalf("terminal output rows = %#v, result=%#v", rows, result)
	}
	if pinCalls != 3 || resolveCalls != 3 {
		t.Fatalf("nested published pin/resolution calls = %d/%d, want 3/3", pinCalls, resolveCalls)
	}
	if len(queries) != 5 {
		t.Fatalf("nested execution issued %d ClickHouse queries, want five: %#v", len(queries), queries)
	}
	for index, query := range queries[:len(queries)-1] {
		if strings.Contains(query, " LIMIT ") {
			t.Fatalf("dependency query %d was preview-limited: %s", index, query)
		}
	}
	if !strings.Contains(queries[len(queries)-1], " LIMIT 3") {
		t.Fatalf("terminal query did not receive the sole row limit: %s", queries[len(queries)-1])
	}
	if len(catalog.items) != 0 {
		t.Fatalf("nested execution leaked %d private artifact manifests", len(catalog.items))
	}

	stream, _, err = engine.streamForOutput(resolved, "terminal", 3)
	if err != nil {
		t.Fatal(err)
	}
	consumerErr := errors.New("consumer stopped")
	_, err = stream.Stream(context.Background(), func(map[string]any) error { return consumerErr })
	if !errors.Is(err, consumerErr) {
		t.Fatalf("terminal consumer error = %v, want %v", err, consumerErr)
	}
	if len(catalog.items) != 0 {
		t.Fatalf("nested execution leaked %d private artifact manifests after consumer cancellation", len(catalog.items))
	}

	stream, _, err = engine.streamForOutput(resolved, "terminal", 3)
	if err != nil {
		t.Fatal(err)
	}
	stream.bindings.Project = "substituted-project"
	queriesBeforeScopeFailure := len(queries)
	_, err = stream.Stream(context.Background(), func(map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("late mismatched-scope proof error = %v, want a scope rejection", err)
	}
	if len(queries) <= queriesBeforeScopeFailure {
		t.Fatal("late scope rejection occurred before its child workspace captures were exercised")
	}
	if len(catalog.items) != 0 {
		t.Fatalf("nested execution leaked %d private artifact manifests after scope-proof rejection", len(catalog.items))
	}
}

func TestEngineCapturesCompilerGroupAndPivotWorkspaceOutputs(t *testing.T) {
	catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
	ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	grouped := recipe.Output{
		Name: "grouped", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "sex", ColumnID: "sex-id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "sex-id", Name: "sex", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group-sex", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group-sex",
					Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "sex-id", OutputColumnID: "group-sex-id"}},
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "patient-count-id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "group-sex-id", Name: "sex", Type: "string"}, {ID: "patient-count-id", Name: "patient_count", Type: "integer"}},
			}},
		},
	}
	pivoted := recipe.Output{
		Name: "pivoted", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "sex", ColumnID: "pivot-sex-id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "category", ColumnID: "category-id", Expr: recipe.Expression{Select: "root.active"}},
			{Name: "amount", ColumnID: "amount-id", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "pivot-sex-id", Name: "sex", Type: "string"},
				{ID: "category-id", Name: "category", Type: "boolean"},
				{ID: "amount-id", Name: "amount", Type: "integer"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot-category", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "pivot-category", GroupKeyIDs: []string{"pivot-sex-id"}, CategoryColumnID: "category-id", ValueColumnID: "amount-id",
					Categories:             []recipe.ConstructionPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarBoolean, Boolean: boolPointer(true)}, OutputColumnID: "active-amount-id"}},
					DuplicatePolicy:        recipe.PivotDuplicateSum,
					MissingCellPolicy:      recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{{ID: "pivot-sex-id", Name: "sex", Type: "string"}, {ID: "active-amount-id", Name: "active_amount", Type: "integer", Nullable: true}},
			}},
		},
	}
	joined := recipe.Output{
		Name: "joined", RootResourceType: "Patient", RowGrain: "patient",
		Construction: &recipe.Construction{
			Version: 1,
			Steps: []recipe.ConstructionStep{{
				ID: "append-group-and-pivot",
				Inputs: []recipe.ConstructionInputRef{
					{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "grouped"},
					{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "pivoted"},
				},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCombineOp, Combine: &recipe.ConstructionCombine{
					Kind: recipe.ConstructionCombineAppend,
					Projections: []recipe.ConstructionCombineProjection{
						{OutputColumnID: "value-id", InputIndex: 0, InputColumnID: "patient-count-id"},
						{OutputColumnID: "value-id", InputIndex: 1, InputColumnID: "active-amount-id"},
					},
				}},
				Outputs: []recipe.StageColumn{{ID: "value-id", Name: "value", Type: "integer", Nullable: true}},
			}},
		},
	}

	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group-pivot-workspace", TranslationVersion: "test",
		Outputs: []recipe.Output{joined, grouped, pivoted},
	}
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	var aqlCalls int
	var failSourceCleanup bool
	var cancelTerminalQuery bool
	var cancelTerminalExecution context.CancelFunc
	var terminalQueryFailure error
	var failedSourceTable string
	cleanupFailure := errors.New("injected source artifact cleanup failure")
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{}, PrivateClickHouseArtifacts: manager,
		QueryRows: func(_ context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			if strings.Contains(query, " LIMIT ") {
				return fmt.Errorf("group or Pivot dependency received a preview limit")
			}
			aqlCalls++
			if aqlCalls == 1 {
				return visit(map[string]any{"sex": "F", "patient_count": int64(2), "__loom_row_id": "group-row"})
			}
			return visit(map[string]any{"sex": "F", "active_amount": int64(3), "__loom_row_id": "pivot-row"})
		},
		ClickHouseQueryRows: func(queryCtx context.Context, query string, columns []string, visit func(map[string]any) error, _ ...any) error {
			if cancelTerminalQuery || terminalQueryFailure != nil {
				catalog.mu.Lock()
				outputs := make(map[string]bool, len(catalog.items))
				for _, manifest := range catalog.items {
					outputs[manifest.Identity.OutputID] = true
				}
				catalog.mu.Unlock()
				if len(outputs) != 3 || !outputs["grouped"] || !outputs["pivoted"] || !outputs["joined"] {
					return fmt.Errorf("terminal query failure ran without terminal and both dependency artifacts: %#v", outputs)
				}
				if cancelTerminalQuery {
					cancelTerminalQuery = false
					if cancelTerminalExecution == nil {
						return fmt.Errorf("terminal query cancellation has no owning context")
					}
					cancelTerminalExecution()
					return queryCtx.Err()
				}
				return terminalQueryFailure
			}
			if failSourceCleanup {
				if strings.Contains(query, " LIMIT ") {
					return fmt.Errorf("terminal capture unexpectedly received a preview limit")
				}
				catalog.mu.Lock()
				for _, manifest := range catalog.items {
					if manifest.Identity.OutputID == "grouped" {
						failedSourceTable = manifest.PhysicalTable
						break
					}
				}
				catalog.mu.Unlock()
				if failedSourceTable == "" {
					return fmt.Errorf("grouped source artifact was not live during terminal capture")
				}
				ch.mu.Lock()
				ch.failDropTable, ch.failDropTableWith = failedSourceTable, cleanupFailure
				ch.mu.Unlock()
			} else if !strings.Contains(query, " LIMIT 2") {
				return fmt.Errorf("terminal Combine did not receive its requested preview limit")
			}
			if !reflect.DeepEqual(columns, []string{"__loom_row_id", "value", "auth_resource_path"}) {
				return fmt.Errorf("terminal Combine columns = %#v", columns)
			}
			wantLiveCaptures := 2
			if failSourceCleanup {
				wantLiveCaptures++ // the terminal writer is already live while its rows stream.
			}
			if len(catalog.items) != wantLiveCaptures {
				return fmt.Errorf("terminal Combine sees %d private captures, want %d", len(catalog.items), wantLiveCaptures)
			}
			seen := map[string]bool{}
			for _, manifest := range catalog.items {
				identity := manifest.Identity
				if identity.OutputID == "joined" {
					continue // the terminal writer is still filling its table in this callback.
				}
				if identity.Project != bindings.Project || identity.DatasetGeneration != bindings.DatasetGeneration || identity.AuthScopeMode != authscope.ReadScopeUnrestricted || identity.ScopeMode != chartifact.ScopeModeWhole || identity.ScopeEvidenceDigest == "" {
					return fmt.Errorf("group/Pivot artifact lost whole-scope identity: %#v", identity)
				}
				rows := ch.rows[manifest.PhysicalTable]
				if len(rows) != 1 || rows[0]["__loom_row_id"] == "" || rows[0]["auth_resource_path"] != nil {
					return fmt.Errorf("group/Pivot artifact row = %#v", rows)
				}
				seen[identity.OutputID] = true
			}
			if !seen["grouped"] || !seen["pivoted"] {
				return fmt.Errorf("private whole-scope captures = %#v", seen)
			}
			if err := visit(map[string]any{"__loom_row_id": "joined-group-row", "value": int64(2), "auth_resource_path": nil}); err != nil {
				return err
			}
			return visit(map[string]any{"__loom_row_id": "joined-pivot-row", "value": int64(3), "auth_resource_path": nil})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatalf("compile Group/Pivot workspace fixture: %v", err)
	}
	stream, _, err := engine.streamForOutput(resolved, "joined", 2)
	if err != nil {
		t.Fatalf("build Group/Pivot workspace stream: %v", err)
	}
	if len(stream.workspaceArtifactSources) != 2 {
		t.Fatalf("Group/Pivot capture dependencies = %d, want two", len(stream.workspaceArtifactSources))
	}
	for _, source := range stream.workspaceArtifactSources {
		if source.Stream.artifactScopeMode != chartifact.ScopeModeWhole || source.Stream.scopeEvidence == nil || source.Stream.scopeEvidenceIdentity == nil {
			t.Fatalf("workspace source %q lacks compiler-issued whole-relation evidence", source.OutputID)
		}
	}
	var rows []map[string]any
	result, err := stream.Stream(context.Background(), func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("capture and combine Group/Pivot sources: %v", err)
	}
	if result.RowCount != 2 || len(rows) != 2 || rows[0]["value"] != int64(2) || rows[1]["value"] != int64(3) {
		t.Fatalf("combined Group/Pivot result = %#v (%#v)", rows, result)
	}
	if aqlCalls != 2 || len(catalog.items) != 0 || len(ch.rows) != 0 {
		t.Fatalf("Group/Pivot source calls or cleanup = %d AQL calls, %d manifests, %d tables", aqlCalls, len(catalog.items), len(ch.rows))
	}

	stream, _, err = engine.streamForOutput(resolved, "joined", 0)
	if err != nil {
		t.Fatalf("build Group/Pivot cancellation capture: %v", err)
	}
	columns, err := chartifact.ColumnsFromCompiledOutput(stream.outputSchema)
	if err != nil {
		t.Fatalf("resolve Group/Pivot cancellation schema: %v", err)
	}
	aqlCalls = 0
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelTerminalQuery, cancelTerminalExecution = true, cancel
	_, err = stream.materializePreparedClickHouse(cancelCtx, manager, "execution-terminal-cancel", stream.stageID, columns)
	cancel()
	cancelTerminalExecution = nil
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("terminal query cancellation error = %v, want context.Canceled", err)
	}
	if len(catalog.items) != 0 || len(ch.rows) != 0 {
		t.Fatalf("terminal query cancellation leaked %d manifests and %d ClickHouse tables", len(catalog.items), len(ch.rows))
	}

	stream, _, err = engine.streamForOutput(resolved, "joined", 0)
	if err != nil {
		t.Fatalf("build Group/Pivot terminal failure capture: %v", err)
	}
	aqlCalls = 0
	terminalQueryFailure = errors.New("injected terminal ClickHouse query failure")
	_, err = stream.materializePreparedClickHouse(context.Background(), manager, "execution-terminal-failure", stream.stageID, columns)
	terminalQueryFailure = nil
	if err == nil || !strings.Contains(err.Error(), "injected terminal ClickHouse query failure") {
		t.Fatalf("terminal query failure = %v, want injected query error", err)
	}
	if len(catalog.items) != 0 || len(ch.rows) != 0 {
		t.Fatalf("terminal query failure leaked %d manifests and %d ClickHouse tables", len(catalog.items), len(ch.rows))
	}

	stream, _, err = engine.streamForOutput(resolved, "joined", 0)
	if err != nil {
		t.Fatalf("build unbounded Group/Pivot capture stream: %v", err)
	}
	columns, err = chartifact.ColumnsFromCompiledOutput(stream.outputSchema)
	if err != nil {
		t.Fatalf("resolve terminal capture schema: %v", err)
	}
	aqlCalls = 0
	failSourceCleanup = true
	_, err = stream.materializePreparedClickHouse(context.Background(), manager, "execution-cleanup-failure", stream.stageID, columns)
	if !errors.Is(err, cleanupFailure) {
		t.Fatalf("terminal capture did not preserve post-consume source cleanup error: %v", err)
	}
	catalog.mu.Lock()
	terminalWasFinalized := catalog.readyOutputIDs["joined"]
	remaining := make([]chartifact.Manifest, 0, len(catalog.items))
	for _, manifest := range catalog.items {
		remaining = append(remaining, manifest)
	}
	catalog.mu.Unlock()
	if !terminalWasFinalized {
		t.Fatal("injected dependency cleanup failure occurred before the terminal artifact was finalized")
	}
	if len(remaining) != 1 || remaining[0].Identity.OutputID != "grouped" || remaining[0].State != chartifact.StateCleanupPending {
		t.Fatalf("post-consume cleanup retained unexpected manifests: %#v", remaining)
	}
	ch.mu.Lock()
	remainingTables := len(ch.rows)
	_, sourceTableRemains := ch.rows[failedSourceTable]
	ch.mu.Unlock()
	if remainingTables != 1 || !sourceTableRemains {
		t.Fatalf("failed dependency cleanup tables = %d, source table remains=%t", remainingTables, sourceTableRemains)
	}
}

func TestEngineCapturesRestrictedWorkspaceRowsWithExactAuthPaths(t *testing.T) {
	const allowedPath = "/programs/allowed"
	for _, test := range []struct {
		name          string
		returnedPath  string
		wantPathError bool
	}{
		{name: "preserves scoped rows through Combine", returnedPath: allowedPath},
		{name: "rejects a row outside the pinned scope", returnedPath: "/programs/excluded", wantPathError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
			ch := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
			manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
			if err != nil {
				t.Fatal(err)
			}
			bindings := recipe.RuntimeBindings{
				Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeRestricted,
				AuthResourcePaths: []string{allowedPath},
			}
			var sourceCalls, combineCalls int
			engine, err := New(Config{
				Registry: invalidRecipeRegistry{}, PrivateClickHouseArtifacts: manager,
				QueryRows: func(_ context.Context, query string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
					if !strings.Contains(query, "auth_resource_path IN @auth_resource_paths") || strings.Contains(query, " LIMIT ") {
						return fmt.Errorf("restricted source query omitted its exact scope filter or received a limit: %s", query)
					}
					projectedAuthPath := false
					for _, value := range bindVars {
						if name, ok := value.(string); ok && name == "auth_resource_path" {
							projectedAuthPath = true
							break
						}
					}
					if !projectedAuthPath {
						return fmt.Errorf("restricted source query did not project the private row auth path: %#v", bindVars)
					}
					if bindVars["auth_resource_paths_unrestricted"] != false || !reflect.DeepEqual(bindVars["auth_resource_paths"], []string{allowedPath}) {
						return fmt.Errorf("restricted source query changed its exact auth scope: %#v", bindVars)
					}
					value := "left"
					if sourceCalls == 1 {
						value = "right"
					}
					sourceCalls++
					return visit(map[string]any{
						"__loom_row_id": "row-" + value, "value": value, "auth_resource_path": test.returnedPath,
					})
				},
				ClickHouseQueryRows: func(_ context.Context, query string, columns []string, visit func(map[string]any) error, args ...any) error {
					combineCalls++
					if !strings.Contains(query, " LIMIT 1") || strings.Count(query, "auth_resource_path` IN ?") != 2 {
						return fmt.Errorf("terminal Combine query lost its limit or exact per-input path filters: %s", query)
					}
					if !reflect.DeepEqual(columns, []string{"__loom_row_id", "value", "auth_resource_path"}) {
						return fmt.Errorf("terminal Combine columns = %#v", columns)
					}
					wantArgs := []any{[]string{allowedPath}, []string{allowedPath}}
					if !reflect.DeepEqual(args, wantArgs) {
						return fmt.Errorf("terminal Combine path args = %#v, want %#v", args, wantArgs)
					}
					catalog.mu.Lock()
					manifests := make([]chartifact.Manifest, 0, len(catalog.items))
					for _, manifest := range catalog.items {
						manifests = append(manifests, manifest)
					}
					catalog.mu.Unlock()
					if len(manifests) != 2 {
						return fmt.Errorf("terminal Combine saw %d captured source artifacts, want two", len(manifests))
					}
					wantValues := map[string]string{"left": "left", "right": "right"}
					for _, manifest := range manifests {
						identity := manifest.Identity
						if identity.Project != bindings.Project || identity.DatasetGeneration != bindings.DatasetGeneration ||
							identity.AuthScopeMode != authscope.ReadScopeRestricted || !reflect.DeepEqual(identity.AuthResourcePaths, bindings.AuthResourcePaths) ||
							identity.ScopeMode != chartifact.ScopeModeRows {
							return fmt.Errorf("captured source identity lost exact restricted scope: %#v", identity)
						}
						ch.mu.Lock()
						rows := append([]map[string]any(nil), ch.rows[manifest.PhysicalTable]...)
						ch.mu.Unlock()
						if len(rows) != 1 || rows[0]["value"] != wantValues[identity.OutputID] || rows[0]["auth_resource_path"] != allowedPath {
							return fmt.Errorf("captured source %q did not retain its exact row path: %#v", identity.OutputID, rows)
						}
					}
					return visit(map[string]any{
						"__loom_row_id": "combined-left", "value": "left", "auth_resource_path": allowedPath,
					})
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			bundle := recipe.Bundle{
				RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "restricted-workspace-capture", TranslationVersion: "test",
				Outputs: []recipe.Output{
					{Name: "left", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "value", ColumnID: "value-id", Expr: recipe.Expression{Select: "root.id"}}}},
					{Name: "right", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "value", ColumnID: "value-id", Expr: recipe.Expression{Select: "root.id"}}}},
				},
			}
			resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, bindings)
			if err != nil {
				t.Fatalf("compile restricted source outputs: %v", err)
			}
			dependencies := append([]lower.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
			joined := workspaceArtifactTestOutput("joined", []ir.PhysicalCombineInputRef{
				{WorkspaceOutputID: "left"}, {WorkspaceOutputID: "right"},
			})
			joined.Plan.ClickHouseCombine.Outputs[0].ClickHouseType = "Nullable(String)"
			joined.Plan.ClickHouseCombine.Outputs[0].Nullable = true
			joined.OutputSchema[1].Nullable = true
			resolved.Compiled.Outputs = []lower.CompiledRecipeOutput{joined}
			resolved.Compiled.WorkspaceDependencies = dependencies

			stream, _, err := engine.streamForOutput(resolved, "joined", 1)
			if err != nil {
				t.Fatalf("build restricted workspace Combine: %v", err)
			}
			if resolved.Semantic.SemanticPlan.Bindings.IncludeAuthResourcePath || stream.bindings.IncludeAuthResourcePath {
				t.Fatal("private source projection mutated the original or selected Combine bindings")
			}
			if !reflect.DeepEqual(stream.outputSchema, resolved.Compiled.Outputs[0].OutputSchema) {
				t.Fatal("private source projection changed the selected Combine output schema")
			}
			if len(stream.workspaceArtifactSources) != 2 {
				t.Fatalf("workspace capture sources = %d, want two", len(stream.workspaceArtifactSources))
			}
			for index, source := range stream.workspaceArtifactSources {
				if !source.Stream.bindings.IncludeAuthResourcePath {
					t.Fatalf("restricted row-scoped source %q was not compiled with its private auth-path projection", source.OutputID)
				}
				if strings.Contains(strings.Join(source.Stream.Columns, ","), "auth_resource_path") {
					t.Fatalf("private auth-path projection leaked into source %q public columns", source.OutputID)
				}
				if !reflect.DeepEqual(source.Stream.outputSchema, dependencies[index].OutputSchema) {
					t.Fatalf("private auth-path projection changed source %q output schema", source.OutputID)
				}
			}

			var rows []map[string]any
			result, err := stream.Stream(context.Background(), func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			})
			if test.wantPathError {
				if err == nil || !strings.Contains(err.Error(), "outside the exact restricted scope") {
					t.Fatalf("out-of-scope captured row error = %v, want exact-scope rejection", err)
				}
				if combineCalls != 0 {
					t.Fatalf("terminal Combine ran %d times after an out-of-scope source row", combineCalls)
				}
			} else {
				if err != nil {
					t.Fatalf("restricted workspace Combine: %v", err)
				}
				if sourceCalls != 2 || combineCalls != 1 || result.RowCount != 1 || len(rows) != 1 ||
					rows[0]["value"] != "left" || rows[0]["auth_resource_path"] != allowedPath {
					t.Fatalf("restricted workspace Combine calls/rows = %d/%d/%#v (%#v)", sourceCalls, combineCalls, rows, result)
				}
			}
			if len(catalog.items) != 0 || len(ch.rows) != 0 {
				t.Fatalf("restricted workspace capture leaked %d manifests and %d tables", len(catalog.items), len(ch.rows))
			}
		})
	}
}

func TestOrdinaryRestrictedPreviewDoesNotRequirePrivateCaptureProof(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/programs/a"},
	}
	var queryCalls int
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
			queryCalls++
			if bindVars["auth_resource_paths_unrestricted"] != false || !reflect.DeepEqual(bindVars["auth_resource_paths"], []string{"/programs/a"}) {
				return fmt.Errorf("ordinary preview lost its exact restricted source scope: %#v", bindVars)
			}
			return visit(map[string]any{"id": "patient-1", "__loom_row_id": "patient-row-1"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "restricted-preview", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}}}}},
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	// Model a valid typed source plan for which the stronger private-capture
	// issuer declines a whole-source proof. Public preview must stay available.
	resolved.Compiled.Outputs[0].ScopeEvidence = nil
	resolved.Compiled.Outputs[0].ScopeEvidenceIdentity = nil
	rows := []map[string]any{}
	_, err = engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "patients", Limit: 2}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ordinary restricted preview required private-capture metadata: %v", err)
	}
	if queryCalls != 1 || !reflect.DeepEqual(rows, []map[string]any{{"id": "patient-1"}}) {
		t.Fatalf("ordinary restricted preview calls/rows = %d/%#v", queryCalls, rows)
	}
}

func TestPublishedOnlyKeyJoinStreamsRepeatedOutput(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/programs/a"},
	}
	plan := ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineKeyJoin,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "table-left", RevisionID: "revision-left", OutputID: "left"},
			{TableID: "table-right", RevisionID: "revision-right", OutputID: "right"},
		},
		Keys:     []ir.PhysicalCombineKey{{LeftColumnID: "left-key", RightColumnID: "right-key"}},
		JoinType: "INNER", RightMatchPolicy: "PRESERVE_ALL",
		Projections: []ir.PhysicalCombineProjection{{OutputColumnID: "tags-id", InputIndex: 0, InputColumnID: "tags-left"}},
		Outputs:     []ir.PhysicalCombineOutputColumn{{ID: "tags-id", Name: "tags", LogicalType: "string", ClickHouseType: "Array(String)", Repeated: true}},
	}}
	output := lower.CompiledRecipeOutput{
		Name: "joined", Columns: []string{"tags"}, Stages: []lower.CompiledStageDescriptor{{ID: "combine-final"}}, Plan: plan,
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
			{ID: "tags-id", Name: "tags", Kind: "string", Cardinality: "many"},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"__loom_row_id"}},
	}
	published := func(tableID, revisionID, outputID, keyID, physical string, includeTags bool) ir.ResolvedClickHouseTable {
		columns := []ir.ResolvedClickHouseColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
			{ID: keyID, Name: "key", LogicalType: "string", ClickHouseType: "String"},
			{ID: "auth-path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "String"},
		}
		if includeTags {
			columns = append(columns, ir.ResolvedClickHouseColumn{ID: "tags-left", Name: "tags", LogicalType: "string", ClickHouseType: "Array(String)", Repeated: true})
		}
		return ir.ResolvedClickHouseTable{
			TableID: tableID, RevisionID: revisionID, OutputID: outputID,
			Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
			ReceiptID: "receipt-" + revisionID, SchemaDigest: "schema-" + revisionID,
			ScopeDigest: "scope-" + revisionID, PhysicalTable: physical,
			Unrestricted: false, AuthResourcePaths: []string{"/programs/a"}, ScopeMode: chartifact.ScopeModeRows,
			Columns: columns,
		}
	}
	left, right := published("table-left", "revision-left", "left", "left-key", "left_private", true),
		published("table-right", "revision-right", "right", "right-key", "right_private", false)
	var pinned []string
	engine, err := New(Config{
		Registry:  invalidRecipeRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		WithExecutionReadPins: func(_ context.Context, revisions []string, visit func(context.Context) error) error {
			pinned = append([]string(nil), revisions...)
			return visit(context.Background())
		},
		ResolveClickHouseInputs: func(_ context.Context, got ir.PhysicalClickHouseCombine, gotBindings recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			if gotBindings.Project != bindings.Project || gotBindings.DatasetGeneration != bindings.DatasetGeneration || !reflect.DeepEqual(got.Inputs, plan.ClickHouseCombine.Inputs) {
				return nil, fmt.Errorf("published-only repeated KeyJoin lost its exact inputs or project scope")
			}
			return []ir.ResolvedClickHouseTable{left, right}, nil
		},
		ClickHouseQueryRows: func(_ context.Context, _ string, columns []string, visit func(map[string]any) error, _ ...any) error {
			if !reflect.DeepEqual(columns, []string{"__loom_row_id", "tags", "auth_resource_path"}) {
				return fmt.Errorf("repeated KeyJoin query columns = %#v", columns)
			}
			return visit(map[string]any{"__loom_row_id": "joined-row", "tags": []any{"a", "b"}, "auth_resource_path": "/programs/a"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := Resolved{
		Semantic: semantic.ResolvedRecipePlan{SemanticPlan: semantic.RecipePlan{Bindings: bindings}},
		Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
	}
	stream, _, err := engine.streamForOutput(resolved, "joined", 0)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if _, err := stream.Stream(context.Background(), func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("stream published-only repeated KeyJoin: %v", err)
	}
	if !reflect.DeepEqual(pinned, []string{"revision-left", "revision-right"}) || !reflect.DeepEqual(rows, []map[string]any{{"__loom_row_id": "joined-row", "tags": []any{"a", "b"}, "auth_resource_path": "/programs/a"}}) {
		t.Fatalf("published repeated KeyJoin pins/rows = %#v/%#v", pinned, rows)
	}
}

func workspaceArtifactTestOutput(name string, inputs []ir.PhysicalCombineInputRef) lower.CompiledRecipeOutput {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend, Inputs: append([]ir.PhysicalCombineInputRef(nil), inputs...),
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value-id", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	for index := range inputs {
		plan.Projections = append(plan.Projections, ir.PhysicalCombineProjection{OutputColumnID: "value-id", InputIndex: index, InputColumnID: "value-id"})
	}
	return lower.CompiledRecipeOutput{
		Name: name, Columns: []string{"value"},
		Stages: []lower.CompiledStageDescriptor{{ID: "combine-final"}},
		Plan:   ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &plan},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
			{ID: "value-id", Name: "value", Kind: "string", Cardinality: "required_one"},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"__loom_row_id"}},
	}
}

func boolPointer(value bool) *bool { return &value }
