package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
)

type invalidRecipeRegistry struct{}

func (invalidRecipeRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{Bundle: recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "default",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "missing", Expr: recipe.Expression{Select: "root.missing"}}},
		}},
	}}, nil
}

func (r invalidRecipeRegistry) LoadRecipeVersion(ctx context.Context, name, _ string) (exec.Entry, error) {
	return r.LoadRecipe(ctx, name)
}

func TestMaterializeMarksResolutionFailures(t *testing.T) {
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Materialize(context.Background(), "default", recipe.RuntimeBindings{Project: "P1"}, nil)
	var resolution *ResolutionError
	if !errors.As(err, &resolution) {
		t.Fatalf("error = %v, want ResolutionError", err)
	}
}

func testResolvedBundle(dynamicColumns []string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "resolved",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			DynamicColumns: []recipe.DynamicColumn{{
				Name: "identifier", Source: recipe.Expression{Select: "root.identifier[].value"},
				Columns: dynamicColumns,
			}},
		}},
	}
}

func TestCohortPreviewDoesNotInventSingleSourceIdentityOrPageItsInput(t *testing.T) {
	for _, test := range []struct {
		name         string
		rootPageRows int
	}{
		{name: "grouped preview identity"},
		{name: "complete cohort input", rootPageRows: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, err := New(Config{
				Registry:     invalidRecipeRegistry{},
				ScopeDigest:  func(recipe.RuntimeBindings) string { return "test-scope" },
				QueryRows:    func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
				RootPageRows: test.rootPageRows,
			})
			if err != nil {
				t.Fatal(err)
			}
			bindings := recipe.RuntimeBindings{
				Project: "cohort-preview-project", SelectionProject: "cohort-preview-project",
				DatasetGeneration: "cohort-preview-generation", PreviewLimit: 25, IncludeSourceIdentity: true,
			}
			resolved, err := engine.CompileResolvedBundle(context.Background(), testCohortCompositionBundle(), bindings)
			if err != nil {
				t.Fatalf("compile cohort receipt bundle: %v", err)
			}
			if len(resolved.Compiled.Outputs) != 1 || resolved.Compiled.Outputs[0].Plan.StageSequence == nil {
				t.Fatal("cohort output has no typed stage sequence")
			}
			foundCohortStage := false
			for _, stage := range resolved.Compiled.Outputs[0].Plan.StageSequence.Stages {
				foundCohortStage = foundCohortStage || stage.Kind == ir.PhysicalStageCohortGroupOp
			}
			if !foundCohortStage {
				t.Fatal("cohort output has no typed COHORT_GROUP stage")
			}
			stream, query, err := engine.streamForOutput(resolved, "Cohort", 25)
			if err != nil {
				t.Fatalf("compile cohort preview plan: %v", err)
			}
			if stream.page != nil {
				t.Fatal("cohort preview paged roots before completing the group aggregation")
			}
			if strings.Contains(query.Query, "__loom_source_resource_id") {
				t.Fatal("cohort preview assigned one scalar source identity to a multi-member group")
			}
			if !strings.Contains(query.Query, "group_revision_id") {
				t.Fatal("cohort preview query omitted its grouped-row identity")
			}
		})
	}
}

func testCohortCompositionBundle() recipe.Bundle {
	value := "patient-a"
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "cohort preview",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Cohort", RootResourceType: "Patient", RowGrain: "groups",
			Fields: []recipe.Field{{Name: "id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "id", Type: "string"}},
				Steps: []recipe.ConstructionStep{{
					ID: "keep_patient", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "patient_id", Operator: recipe.FilterEquals,
						Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}},
					}},
					Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "id", Type: "string"}},
				}},
			},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_preview", AfterStepID: "keep_patient", UnassignedMemberPolicy: "EXCLUDE",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "patient_id", Policy: recipe.ConstructionRowValueAll}},
			},
		}},
	}
}

func testEngine(queryRows QueryRows) *Engine {
	engine, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows:   queryRows,
	})
	if err != nil {
		panic(err)
	}
	return engine
}

func TestPreviewOutputFiltersInternalColumnsAndReturnsSafePlanSummary(t *testing.T) {
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{
			"id":                          "p1",
			"_key":                        "internal-key",
			"__loom_row_id":               "internal-row-id",
			"auth_resource_path":          "/private/path",
			"__loom_dynamic_runtime_keys": map[string]any{"family": []string{"x"}},
		})
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	summary, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Output != "Patient" || summary.RowCount != 1 || summary.PlanMode != "physical" || summary.PlanProfile != "generic_fhir_graph_recipe" || summary.PlanFingerprint == "" {
		t.Fatalf("unsafe or incomplete preview summary: %#v", summary)
	}
	if len(rows) != 1 || rows[0]["id"] != "p1" || len(rows[0]) != 1 {
		t.Fatalf("preview row was not public-only: %#v", rows)
	}
}

func TestPreviewOutputCarriesFHIRSourceWithoutVisibleRootID(t *testing.T) {
	sourceIDProjected := false
	e, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(_ context.Context, _ string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
			if _, isSelectedRootQuery := bindVars[compiler.RootPageKeysBind]; isSelectedRootQuery {
				for _, value := range bindVars {
					if value == ir.PreviewSourceResourceIDColumn {
						sourceIDProjected = true
					}
				}
				return visit(map[string]any{
					"status": "final", "_key": "arangodb-key",
					ir.PreviewSourceResourceIDColumn: "fhir-researcher-id",
				})
			}
			if _, isRootKeyQuery := bindVars[compiler.RootPageAfterKeyBind]; isRootKeyQuery {
				return visit(map[string]any{"_key": "arangodb-key"})
			}
			return fmt.Errorf("unexpected preview query without root page bindings")
		},
		RootPageRows: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1, Name: "no-visible-root-id", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "Observation", RootResourceType: "Observation", RowGrain: "observation",
			Fields: []recipe.Field{{Name: "status", Expr: recipe.Expression{Select: "root.status"}}},
		}},
	}
	resolved, err := e.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{
		Project: "P1", IncludeSourceIdentity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	summary, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Observation", Limit: 1}, func(row map[string]any) error {
		got = row
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sourceIDProjected {
		t.Fatal("preview query omitted private source ID projection")
	}
	if summary.RowCount != 1 || len(summary.Columns) != 1 || summary.Columns[0] != "status" {
		t.Fatalf("preview summary exposed non-public identity: %#v", summary)
	}
	if got["status"] != "final" {
		t.Fatalf("public preview row = %#v", got)
	}
	if _, ok := got["_key"]; ok {
		t.Fatalf("Arango key leaked into preview row: %#v", got)
	}
	if _, ok := got[ir.PreviewSourceResourceIDColumn]; ok {
		t.Fatalf("private projection leaked into preview row: %#v", got)
	}
	source, ok := got[previewSourceMetadataKey].(map[string]any)
	if !ok || source["kind"] != previewSourceSingle || source["resourceType"] != "Observation" || source["id"] != "fhir-researcher-id" {
		t.Fatalf("source sidecar = %#v, want Observation/fhir-researcher-id independent of _key", got[previewSourceMetadataKey])
	}
}

func TestPreviewSourceClassificationDoesNotRecoverSingleAfterGroup(t *testing.T) {
	querySchema := []lower.CompiledOutputColumn{{Name: ir.PreviewSourceResourceIDColumn, Internal: true}}
	output := lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{
		{Operation: string(recipe.ConstructionGroupOp)},
		{Operation: string(recipe.ConstructionExpandOp)},
	}}
	if got := previewSourceIdentityMode(output, querySchema); got != previewSourceComposite {
		t.Fatalf("post-Group row-preserving stage source mode = %q, want COMPOSITE", got)
	}
	output = lower.CompiledRecipeOutput{Plan: ir.PhysicalPlan{Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalGroupRowsOp}}}}
	if got := previewSourceIdentityMode(output, querySchema); got != previewSourceComposite {
		t.Fatalf("GROUPS source mode = %q, want COMPOSITE", got)
	}
	output = lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{{Operation: string(recipe.ConstructionRelatedExpandOp)}}}
	if got := previewSourceIdentityMode(output, querySchema); got != previewSourceComposite {
		t.Fatalf("RELATED_EXPAND source mode = %q, want COMPOSITE", got)
	}
	output = lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{
		{Operation: string(ir.PhysicalStageCohortGroupOp)},
		{Operation: string(recipe.ConstructionFilterOp)},
	}}
	if got := previewSourceIdentityMode(output, querySchema); got != previewSourceComposite {
		t.Fatalf("COHORT_GROUP followed by FILTER source mode = %q, want COMPOSITE", got)
	}
	output = lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{{Operation: string(recipe.ConstructionCodedGroupOp)}}}
	if got := previewSourceIdentityMode(output, querySchema); got != previewSourceComposite {
		t.Fatalf("CODED_GROUP source mode = %q, want COMPOSITE", got)
	}
}

func TestCodedGroupPreviewRunsAgainstWholeInput(t *testing.T) {
	const sourceID = "body_structure_id"
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "body_structures",
		TranslationVersion:  "coded-group-test",
		Outputs: []recipe.Output{{
			Name: "body_structures", RootResourceType: "BodyStructure", RowGrain: "resource",
			Fields: []recipe.Field{{Name: sourceID, ColumnID: sourceID, Expr: recipe.Expression{Select: "root.id"}}},
			Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: sourceID, Name: sourceID}}, Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
					ConstructionID:   "group_codes",
					Source:           recipe.ConstructionCodedGroupSource{OccurrenceID: "base", ResourceType: "BodyStructure", CodingPath: "includedStructure[].structure.coding[]", FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{}},
					MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup, SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id", CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
				}}, Outputs: columns,
			}}},
		}},
	}
	engine, err := New(Config{Registry: invalidRecipeRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }, ScopeDigest: func(recipe.RuntimeBindings) string { return "coded-group" }, RootPageRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatal(err)
	}
	stream, query, err := engine.streamForOutput(resolved, "body_structures", 25)
	if err != nil {
		t.Fatal(err)
	}
	if stream.page != nil {
		t.Fatal("CODED_GROUP must scan the complete input before counting distinct source records")
	}
	groupAt := strings.Index(query.Query, "COLLECT __loom_construction_coded_group_system")
	limitAt := strings.Index(query.Query, "LIMIT @limit")
	if !strings.Contains(query.Query, "FOR root IN @@root_collection") || groupAt < 0 || limitAt <= groupAt {
		t.Fatalf("full-input CODED_GROUP query has an unexpected row scope:\n%s", query.Query)
	}
	if stream.sourceIdentityMode != previewSourceComposite {
		t.Fatalf("CODED_GROUP source mode = %q, want COMPOSITE", stream.sourceIdentityMode)
	}
}

func TestPreviewOutputUsesPreviewExecutorWithoutChangingOrdinaryStreams(t *testing.T) {
	var ordinaryCalls, previewCalls int
	ordinaryQuery := func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		ordinaryCalls++
		return visit(map[string]any{"id": "ordinary", "_key": "ordinary-key"})
	}
	previewQuery := func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		previewCalls++
		return visit(map[string]any{"id": "preview"})
	}
	e, err := New(Config{
		Registry:         invalidRecipeRegistry{},
		ScopeDigest:      func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows:        ordinaryQuery,
		PreviewQueryRows: previewQuery,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if previewCalls != 1 || ordinaryCalls != 0 {
		t.Fatalf("after preview: preview calls=%d ordinary calls=%d", previewCalls, ordinaryCalls)
	}
	stream, _, err := e.streamForOutput(resolved, "Patient", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Stream(context.Background(), func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if previewCalls != 1 || ordinaryCalls != 1 {
		t.Fatalf("after ordinary stream: preview calls=%d ordinary calls=%d", previewCalls, ordinaryCalls)
	}
}

func TestPreviewOutputDefersOnlyGroupIndexPreparation(t *testing.T) {
	groupOutput := executionGroupPreviewOutput()
	queryFinished := make(chan struct{})
	prepared := make(chan bool, 1)
	groupEngine, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		PreviewQueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			defer close(queryFinished)
			return visit(map[string]any{"status": "final", "rows": 1})
		},
		PreparePreviewIndex: func(_ context.Context, spec compiler.PreviewCoveringIndexSpec) error {
			select {
			case <-queryFinished:
				prepared <- spec.PrepareAfterPreview
			default:
				prepared <- false
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	groupResolved, err := groupEngine.CompileResolvedBundle(context.Background(), recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                groupOutput.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{groupOutput},
	}, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := groupEngine.PreviewOutput(context.Background(), groupResolved, PreviewRequest{Output: groupOutput.Name, Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	select {
	case afterQuery := <-prepared:
		if !afterQuery {
			t.Fatal("Group index preparation started before the preview query completed")
		}
	case <-time.After(time.Second):
		t.Fatal("successful Group preview did not schedule index preparation")
	}

	for _, test := range []struct {
		name              string
		rows              QueryRows
		cancelDuringQuery bool
	}{
		{name: "query failure", rows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return errors.New("query failed")
		}},
		{name: "canceled", cancelDuringQuery: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepareCalls := make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rows := test.rows
			if test.cancelDuringQuery {
				rows = func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
					cancel()
					return context.Canceled
				}
			}
			engine, err := New(Config{
				Registry:         invalidRecipeRegistry{},
				ScopeDigest:      func(recipe.RuntimeBindings) string { return "test-scope" },
				QueryRows:        func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
				PreviewQueryRows: rows,
				PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
					prepareCalls <- struct{}{}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := engine.CompileResolvedBundle(context.Background(), recipe.Bundle{
				RecipeSchemaVersion: recipe.CurrentSchemaVersion,
				Name:                groupOutput.Name,
				TranslationVersion:  "test",
				Outputs:             []recipe.Output{groupOutput},
			}, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := engine.PreviewOutput(ctx, resolved, PreviewRequest{Output: groupOutput.Name, Limit: 1}, func(map[string]any) error { return nil }); err == nil {
				t.Fatal("preview returned no error")
			}
			select {
			case <-prepareCalls:
				t.Fatal("failed or canceled Group preview scheduled index preparation")
			case <-time.After(25 * time.Millisecond):
			}
		})
	}

	pivotOutput := executionPivotPreviewOutput()
	preparedBeforeQuery := false
	pivotEngine, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows:   func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			preparedBeforeQuery = true
			return nil
		},
		PreviewQueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			if !preparedBeforeQuery {
				return errors.New("Pivot query started before its index preparation")
			}
			return visit(map[string]any{"resource_id": "r1", "female_amount": 1, "male_amount": nil})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pivotResolved, err := pivotEngine.CompileResolvedBundle(context.Background(), recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                pivotOutput.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{pivotOutput},
	}, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pivotEngine.PreviewOutput(context.Background(), pivotResolved, PreviewRequest{Output: pivotOutput.Name, Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("Pivot preview did not retain synchronous index preparation: %v", err)
	}
}

func executionGroupPreviewOutput() recipe.Output {
	return recipe.Output{
		Name: "group_preview", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status",
					Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "grouped_status_id"}},
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "grouped_status_id", Name: "status"}, {ID: "rows_id", Name: "rows", Type: "integer"}},
			}},
		},
	}
}

func executionPivotPreviewOutput() recipe.Output {
	female, male := "female", "male"
	return recipe.Output{
		Name: "pivot_preview", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "resource_id", ColumnID: "id_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "id_id", Name: "resource_id"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"}},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "pivot_rows", GroupKeyIDs: []string{"id_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []recipe.ConstructionPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &female}, OutputColumnID: "female_amount_id"},
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &male}, OutputColumnID: "male_amount_id"},
					},
					DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{{ID: "id_id", Name: "resource_id"}, {ID: "female_amount_id", Name: "female_amount"}, {ID: "male_amount_id", Name: "male_amount"}},
			}},
		},
	}
}

func TestPreviewOutputCanExposeStableRowIdentityForComparisonSinks(t *testing.T) {
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"id": "p1", "__loom_row_id": "stable-p1"})
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1", IncludeRowIdentity: true})
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if _, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 2}, func(value map[string]any) error {
		row = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if row["__loom_row_id"] != "stable-p1" {
		t.Fatalf("comparison preview row identity = %#v", row)
	}
}

func TestPreviewOutputExecutesOnlyRequestedOutputAndRejectsUnknownBeforeQuery(t *testing.T) {
	queries := 0
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		queries++
		return visit(map[string]any{"id": "p1"})
	})
	bundle := testResolvedBundle([]string{})
	bundle.Outputs = append(bundle.Outputs, recipe.Output{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Observation", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("query count = %d, want 1", queries)
	}
	_, err = e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Missing", Limit: 1}, func(map[string]any) error { return nil })
	if err == nil || queries != 1 {
		t.Fatalf("unknown output err=%v query count=%d, want error before query", err, queries)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("unknown output error = %v, want INVALID_REQUEST", err)
	}
}

func TestPreviewOutputEnforcesLimitAndCancellation(t *testing.T) {
	rowsSeen := 0
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for index := 0; index < 10; index++ {
			rowsSeen++
			if err := visit(map[string]any{"id": fmt.Sprintf("p%d", index)}); err != nil {
				return err
			}
		}
		return nil
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	summary, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 2}, func(map[string]any) error {
		count++
		return nil
	})
	if err != nil || count != 2 || summary.RowCount != 2 || rowsSeen != 2 {
		t.Fatalf("limit count=%d summary=%#v rowsSeen=%d err=%v", count, summary, rowsSeen, err)
	}
	if summary.Complete || !summary.Truncated {
		t.Fatalf("bounded preview completeness=%#v, want incomplete/truncated", summary)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.PreviewOutput(ctx, resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil })
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("canceled preview error = %v, want CLIENT_CANCELED", err)
	}

	cancelEngine := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		if err := visit(map[string]any{"id": "p1"}); err != nil {
			return err
		}
		return visit(map[string]any{"id": "p2"})
	})
	resolved, err = cancelEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	_, err = cancelEngine.PreviewOutput(ctx, resolved, PreviewRequest{Output: "Patient", Limit: 2}, func(map[string]any) error {
		cancel()
		return nil
	})
	userErr, ok = dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("mid-query canceled preview error = %v, want CLIENT_CANCELED", err)
	}
}

func TestPreviewOutputMarksNaturalExhaustionComplete(t *testing.T) {
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"id": "p1"})
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 2}, func(map[string]any) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Complete || summary.Truncated {
		t.Fatalf("natural preview completeness=%#v, want complete/not truncated", summary)
	}
}

func TestRootKeyPagingPreservesExpandedRowsAndAdvancesPastEmptyRoots(t *testing.T) {
	type calls struct{ keys, rows int }
	newPagedEngine := func(observed *calls) *Engine {
		t.Helper()
		queryRows := func(_ context.Context, _ string, _ int, binds map[string]any, visit func(map[string]any) error) error {
			if afterValue, ok := binds[compiler.RootPageAfterKeyBind]; ok {
				observed.keys++
				after, _ := afterValue.(string)
				pageSize, _ := binds[compiler.RootPageSizeBind].(int)
				emitted := 0
				for _, key := range []string{"a", "b", "c"} {
					if key <= after || emitted == pageSize {
						continue
					}
					if err := visit(map[string]any{"_key": key}); err != nil {
						return err
					}
					emitted++
				}
				return nil
			}
			keys, ok := binds[compiler.RootPageKeysBind].([]string)
			if !ok {
				return fmt.Errorf("unexpected unpaged query")
			}
			observed.rows++
			for _, key := range keys {
				rowCount := 0
				switch key {
				case "a":
					rowCount = 30
				case "c":
					rowCount = 1
				}
				for index := 0; index < rowCount; index++ {
					if err := visit(map[string]any{"_key": key, "id": fmt.Sprintf("%s-%02d", key, index)}); err != nil {
						return err
					}
				}
			}
			return nil
		}
		engine, err := New(Config{Registry: invalidRecipeRegistry{}, QueryRows: queryRows, ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" }, RootPageRows: 2})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}

	t.Run("complete stream", func(t *testing.T) {
		observed := &calls{}
		engine := newPagedEngine(observed)
		resolved, err := engine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
		if err != nil {
			t.Fatal(err)
		}
		streams, err := engine.Streams(context.Background(), resolved)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		result, err := streams[0].Stream(context.Background(), func(row map[string]any) error {
			ids = append(ids, row["id"].(string))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.RowCount != 31 || len(ids) != 31 || ids[0] != "a-00" || ids[30] != "c-00" {
			t.Fatalf("paged rows result=%#v ids=%#v", result, ids)
		}
		if observed.keys != 2 || observed.rows != 2 {
			t.Fatalf("query calls = %#v, want two key and two row pages", observed)
		}
	})

	t.Run("preview output limit", func(t *testing.T) {
		observed := &calls{}
		engine := newPagedEngine(observed)
		resolved, err := engine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		summary, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 25}, func(map[string]any) error {
			count++
			return nil
		})
		if err != nil || count != 25 || summary.RowCount != 25 {
			t.Fatalf("preview count=%d summary=%#v err=%v", count, summary, err)
		}
		if observed.keys != 1 || observed.rows != 1 {
			t.Fatalf("preview calls = %#v, want one bounded page", observed)
		}
	})
}

func TestPreviewOutputNormalizesVisitorAndDynamicSchemaErrors(t *testing.T) {
	driftEngine := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{
			"id":                          "p1",
			"value":                       "v",
			"__loom_dynamic_runtime_keys": map[string]any{"identifier": []string{"unexpected"}},
		})
	})
	resolved, err := driftEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{"value"}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = driftEngine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil })
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDynamicSchemaDrift) {
		t.Fatalf("dynamic drift error = %v, want DYNAMIC_SCHEMA_DRIFT", err)
	}

	visitorErr := errors.New("visitor failed")
	visitorEngine := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"id": "p1"})
	})
	resolved, err = visitorEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = visitorEngine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return visitorErr })
	userErr, ok = dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInternalError) || !errors.Is(err, visitorErr) {
		t.Fatalf("visitor error = %v, want typed internal error preserving cause", err)
	}
}

func TestNormalizePreviewErrorKeepsUnknownBackendFailuresRetryable(t *testing.T) {
	cause := errors.New("database connection refused")
	err := normalizePreviewError(cause, true)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
		t.Fatalf("normalized error = %#v, want retryable BACKEND_UNAVAILABLE", userErr)
	}
	if !errors.Is(err, cause) {
		t.Fatal("normalized backend error did not preserve the cause")
	}
}

func TestValidatePreviewPlanRequiresCanonicalPhysicalClass(t *testing.T) {
	valid := compiler.CompiledQuery{PlanMode: previewPlanMode, PlanProfile: previewPlanProfile, Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}}
	if err := validatePreviewPlan(valid, 2, ir.PhysicalEngineAQL); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []compiler.CompiledQuery{
		{PlanMode: "logical", PlanProfile: previewPlanProfile, Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}},
		{PlanMode: previewPlanMode, PlanProfile: "other", Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}},
		{PlanMode: previewPlanMode, PlanProfile: previewPlanProfile, Limit: 2},
		{PlanMode: previewPlanMode, PlanProfile: previewPlanProfile, Limit: 3, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}},
	} {
		if err := validatePreviewPlan(invalid, 2, ir.PhysicalEngineAQL); err == nil {
			t.Fatalf("invalid plan %#v was admitted", invalid)
		} else if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodePlanTooExpensive) {
			t.Fatalf("invalid plan error = %v, want PLAN_TOO_EXPENSIVE", err)
		}
	}
	clickHouse := compiler.CompiledQuery{PlanMode: "clickhouse", PlanProfile: "pinned_table_combine", Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "combine-fingerprint"}}
	if err := validatePreviewPlan(clickHouse, 2, ir.PhysicalEngineClickHouse); err != nil {
		t.Fatalf("valid pinned ClickHouse preview plan: %v", err)
	}
	if err := validatePreviewPlan(clickHouse, 2, ir.PhysicalEngineAQL); err == nil {
		t.Fatal("ClickHouse preview plan was admitted as AQL")
	}
}

func TestPreviewLimitsHaveStableDefaultAndMaximum(t *testing.T) {
	if got, err := normalizePreviewLimit(0); err != nil || got != DefaultPreviewLimit {
		t.Fatalf("default preview limit = %d, err=%v; want %d", got, err, DefaultPreviewLimit)
	}
	if got, err := normalizePreviewLimit(MaxPreviewLimit); err != nil || got != MaxPreviewLimit {
		t.Fatalf("maximum preview limit = %d, err=%v; want %d", got, err, MaxPreviewLimit)
	}
	for _, limit := range []int{-1, MaxPreviewLimit + 1} {
		if _, err := normalizePreviewLimit(limit); err == nil {
			t.Fatalf("limit %d was accepted", limit)
		}
	}
}

func TestCompileResolvedBundleSkipsCatalogResolverAndRetainsBundle(t *testing.T) {
	resolverCalls := 0
	e, err := New(Config{
		Registry: invalidRecipeRegistry{},
		ResolveBundle: func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
			resolverCalls++
			return recipe.Bundle{}, errors.New("catalog resolver must not be called")
		},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	bundle := testResolvedBundle([]string{"value"})
	resolved, err := e.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 {
		t.Fatalf("catalog resolver calls = %d, want 0", resolverCalls)
	}
	if resolved.Bundle.Name != bundle.Name || len(resolved.Bundle.Outputs) != 1 {
		t.Fatalf("resolved bundle was not retained: %#v", resolved.Bundle)
	}
	if len(resolved.Compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(resolved.Compiled.Outputs))
	}
	if _, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 {
		t.Fatalf("catalog resolver calls during PreviewOutput = %d, want 0", resolverCalls)
	}
}

func TestResolveBundleUsesResolverButResolvedPreviewAndMaterializeDoNot(t *testing.T) {
	resolverCalls := 0
	e, err := New(Config{
		Registry: invalidRecipeRegistry{},
		ResolveBundle: func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (recipe.Bundle, error) {
			resolverCalls++
			return bundle, nil
		},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"id": "p1"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := testResolvedBundle([]string{})
	bindings := recipe.RuntimeBindings{Project: "P1", PreviewLimit: 1}
	normal, err := e.ResolveBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 {
		t.Fatalf("catalog resolver calls after ResolveBundle = %d, want 1", resolverCalls)
	}
	direct, err := e.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	normalStreams, err := e.Streams(context.Background(), normal)
	if err != nil {
		t.Fatal(err)
	}
	directStreams, err := e.Streams(context.Background(), direct)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalStreams) != len(directStreams) || normalStreams[0].query != directStreams[0].query {
		t.Fatalf("resolved output differs from catalog-resolved output")
	}
	if _, err := e.PreviewResolvedBundle(context.Background(), bundle, bindings); err != nil {
		t.Fatal(err)
	}
	if _, err := e.MaterializeResolvedBundle(context.Background(), bundle, bindings, nil); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 {
		t.Fatalf("catalog resolver calls after resolved operations = %d, want 1", resolverCalls)
	}
}

func TestCompileResolvedBundleRejectsUnresolvedDynamicDeclarations(t *testing.T) {
	e, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.CompileResolvedBundle(context.Background(), testResolvedBundle(nil), recipe.RuntimeBindings{Project: "P1"})
	if err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("error = %v, want unresolved dynamic declaration", err)
	}

	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatalf("explicit empty dynamic family rejected: %v", err)
	}
	if len(resolved.Compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(resolved.Compiled.Outputs))
	}
}

func TestAdaptiveRelatedPreviewPagesPreserveSparseRoots(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	var sizes []int
	var visited []string
	stream := OutputStream{rootPageRows: 4, initialRootPageRows: 1, page: &compiler.CompiledOutputPage{}}
	stream.stream = func(_ context.Context, _ string, _ int, binds map[string]any, visit func(map[string]any) error) error {
		if after, ok := binds[compiler.RootPageAfterKeyBind].(string); ok {
			size := binds[compiler.RootPageSizeBind].(int)
			sizes = append(sizes, size)
			count := 0
			for _, key := range keys {
				if key <= after || count >= size {
					continue
				}
				if err := visit(map[string]any{"_key": key}); err != nil {
					return err
				}
				count++
			}
			return nil
		}
		for _, key := range binds[compiler.RootPageKeysBind].([]string) {
			visited = append(visited, key)
			if key == "g" || key == "h" {
				if err := visit(map[string]any{"id": key}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var rows []string
	if err := stream.streamRaw(context.Background(), func(row map[string]any) error { rows = append(rows, row["id"].(string)); return nil }); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(visited) != fmt.Sprint(keys) || fmt.Sprint(rows) != "[g h]" || fmt.Sprint(sizes) != "[1 2 4 1 1]" {
		t.Fatalf("visited=%v rows=%v page sizes=%v", visited, rows, sizes)
	}
}
