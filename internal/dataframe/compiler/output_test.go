package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestGroupedPivotPreviewWindowRunsAfterReshapedRows(t *testing.T) {
	reshape := &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "preview_pivot", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []oraclePivotCategory{
				{Key: recipeTableInteger(0), Output: "zero", Label: "Zero"},
				{Key: recipeTableInteger(1), Output: "one", Label: "One"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	}
	plan := compileTableReshapePhysicalPlan(t, reshape)
	pivot := findGroupedPivot(t, plan)
	windowed, err := WithGenericPhysicalExecutionWindow(plan, 5)
	if err != nil {
		t.Fatal(err)
	}
	if windowed.StageSequence == nil || windowed.StageSequence.PreviewLimitBindKey != genericPhysicalExecutionLimitBind || windowed.StageSequence.FinalRowIdentity != "__loom_row_id" {
		t.Fatal("missing current stage output-row window")
	}

	rendered, err := aql.RenderPhysicalPlan(windowed)
	if err != nil {
		t.Fatal(err)
	}
	collectIndex := strings.Index(rendered.Query, "COLLECT ")
	shapeOutputIndex := strings.Index(rendered.Query, "LET "+pivot.OutputRowVariable+" = ")
	sortText := "SORT __loom_construction_final_row.__loom_row_id ASC"
	sortTextIndex := strings.Index(rendered.Query, sortText)
	limitTextIndex := strings.Index(rendered.Query, "LIMIT @limit")
	if !(collectIndex >= 0 && collectIndex < limitTextIndex && collectIndex < shapeOutputIndex && shapeOutputIndex < sortTextIndex && sortTextIndex < strings.LastIndex(rendered.Query, "LIMIT @limit")) {
		t.Fatalf("pivot query does not aggregate all source rows before deterministic preview window: %s", rendered.Query)
	}
	if strings.Count(rendered.Query, "LIMIT @limit") != 2 {
		t.Fatalf("pivot query has a misplaced or duplicate source-row limit: %s", rendered.Query)
	}
}

func TestUnpivotPreviewWindowBoundsDeterministicReshapedRows(t *testing.T) {
	reshape := &oracleReshape{
		Kind: oracleReshapeUnpivot,
		Unpivot: &oracleUnpivot{
			ConstructionID: "preview_unpivot", Inputs: []oracleUnpivotInput{
				{Column: "value", Key: recipeTableString("first")},
				{Column: "value2", Key: recipeTableString("second")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	}
	plan := compileTableReshapePhysicalPlan(t, reshape)
	unpivot := findUnpivot(t, plan)
	windowed, err := WithGenericPhysicalExecutionWindow(plan, 7)
	if err != nil {
		t.Fatal(err)
	}
	if windowed.StageSequence == nil || windowed.StageSequence.PreviewLimitBindKey != genericPhysicalExecutionLimitBind || windowed.StageSequence.FinalRowIdentity != "__loom_row_id" {
		t.Fatal("missing current stage output-row window")
	}

	rendered, err := aql.RenderPhysicalPlan(windowed)
	if err != nil {
		t.Fatal(err)
	}
	loopIndex := strings.Index(rendered.Query, "FOR "+unpivot.SlotVariable+" IN [")
	sortTextIndex := strings.Index(rendered.Query, "SORT __loom_construction_final_row.__loom_row_id ASC")
	limitIndexInQuery := strings.Index(rendered.Query, "LIMIT @limit")
	if !(loopIndex >= 0 && loopIndex < sortTextIndex && sortTextIndex < limitIndexInQuery) {
		t.Fatalf("unpivot query does not limit deterministic expanded rows: %s", rendered.Query)
	}
	if strings.Count(rendered.Query, "LIMIT @limit") != 1 {
		t.Fatalf("unpivot query must contain exactly one output-row limit: %s", rendered.Query)
	}
}

func compileTableReshapePhysicalPlan(t *testing.T, reshape *oracleReshape) ir.PhysicalPlan {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "reshape-window", TranslationVersion: "test",
		Outputs: []recipe.Output{bindOracleCurrentConstruction(recipe.Output{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{
				{Name: "group", Expr: recipe.Expression{Select: "gender"}},
				{Name: "category", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "value", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "value2", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "keep", Expr: recipe.Expression{Select: "id"}},
			},
		}, reshape)},
	}
	plan, err := recipeplan.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(compiled.Outputs[0].Plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return optimized
}

func operationIndex(plan ir.PhysicalPlan, kind ir.PhysicalOperationKind) int {
	for index, operation := range plan.Operations {
		if operation.Kind == kind {
			return index
		}
	}
	return -1
}

func findGroupedPivot(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalGroupedPivot {
	t.Helper()
	if plan.StageSequence != nil {
		for i := range plan.StageSequence.Stages {
			if value := plan.StageSequence.Stages[i].GroupedPivot; value != nil {
				return value
			}
		}
	}
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalGroupedPivotOp {
			return plan.Operations[index].GroupedPivot
		}
	}
	t.Fatal("physical plan has no grouped pivot")
	return nil
}

func findUnpivot(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalUnpivot {
	t.Helper()
	if plan.StageSequence != nil {
		for i := range plan.StageSequence.Stages {
			if value := plan.StageSequence.Stages[i].Unpivot; value != nil {
				return value
			}
		}
	}
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalUnpivotOp {
			return plan.Operations[index].Unpivot
		}
	}
	t.Fatal("physical plan has no unpivot")
	return nil
}

func recipeTableString(value string) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarString, String: &value}
}

func recipeTableInteger(value int64) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &value}
}

func TestTableReshapeExecutionWindowBindsExpectedLimit(t *testing.T) {
	plan := compileTableReshapePhysicalPlan(t, &oracleReshape{
		Kind: oracleReshapeUnpivot,
		Unpivot: &oracleUnpivot{
			ConstructionID: "limit_bind", Inputs: []oracleUnpivotInput{{Column: "value", Key: recipeTableString("one")}},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullDrop,
		},
	})
	windowed, err := WithGenericPhysicalExecutionWindow(plan, 11)
	if err != nil {
		t.Fatal(err)
	}
	if got := windowed.BindVars[genericPhysicalExecutionLimitBind]; got != 11 {
		t.Fatalf("execution limit bind = %#v, want 11", got)
	}
	if err := windowed.Validate(); err != nil {
		t.Fatalf("windowed reshape plan invalid: %v", err)
	}
}

func TestWithoutUnusedTerminalPivotPresenceCompanionsOnlyPrunesClonedPreviewPlan(t *testing.T) {
	original := terminalPivotPresencePreviewPlan()
	windowed := withoutUnusedTerminalPivotPresenceCompanions(ir.ClonePhysicalPlan(original))

	if !hasPresenceOutput(original.Operations) || !hasStageColumn(original.StageSequence.SourceColumns, "category_present") {
		t.Fatal("pruning mutated the source plan and lost its category-presence companion")
	}
	if hasPresenceOutput(windowed.Operations) || hasStageColumn(windowed.StageSequence.SourceColumns, "category_present") ||
		hasStageColumn(windowed.StageSequence.Stages[0].InputColumns, "category_present") ||
		hasProjection(windowed.StageSequence.Stages[0].GroupedPivot.InputProjections, "category_present") {
		t.Fatal("ordinary terminal Pivot preview retained an unused category-presence companion")
	}

	missingCategory := ir.ClonePhysicalPlan(original)
	missingCategory.StageSequence.Stages[0].GroupedPivot.CategoryPresenceColumn = "category_present"
	missingCategory = withoutUnusedTerminalPivotPresenceCompanions(missingCategory)
	if !hasPresenceOutput(missingCategory.Operations) || !hasStageColumn(missingCategory.StageSequence.SourceColumns, "category_present") {
		t.Fatal("a Pivot consuming the MISSING-category presence contract was pruned")
	}
}

func terminalPivotPresencePreviewPlan() ir.PhysicalPlan {
	columns := []ir.PhysicalStageColumn{{Name: "group"}, {Name: "category"}, {Name: "category_present"}}
	projections := []ir.PhysicalProjection{
		{Name: "group"},
		{Name: "category"},
		{Name: "category_present", Hidden: true, PresenceOutput: true},
	}
	pivot := &ir.PhysicalGroupedPivot{InputProjections: append([]ir.PhysicalProjection(nil), projections...)}
	stage := ir.PhysicalConstructionStage{
		ID: "pivot", InputStageID: "source", Kind: ir.PhysicalStagePivotOp, RowIdentityColumn: "row_id",
		InputColumns: append([]ir.PhysicalStageColumn(nil), columns...),
		GroupedPivot: pivot,
	}
	return ir.PhysicalPlan{
		Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: append([]ir.PhysicalProjection(nil), projections...)}}},
		StageSequence: &ir.PhysicalStageSequence{
			SourceStageID: "source", SourceColumns: append([]ir.PhysicalStageColumn(nil), columns...),
			Stages: []ir.PhysicalConstructionStage{stage}, FinalStageID: "pivot", FinalRowIdentity: "row_id",
			PreviewLimitBindKey: "preview_limit", PreviewTerminalPivotWindow: true,
		},
	}
}

func hasPresenceOutput(operations []ir.PhysicalOperation) bool {
	for _, operation := range operations {
		if operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "category_present" && projection.PresenceOutput {
				return true
			}
		}
	}
	return false
}

func hasStageColumn(columns []ir.PhysicalStageColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return true
		}
	}
	return false
}

func hasProjection(projections []ir.PhysicalProjection, name string) bool {
	for _, projection := range projections {
		if projection.Name == name {
			return true
		}
	}
	return false
}

func TestPhysicalValidationRejectsSourceLimitBeforeReshape(t *testing.T) {
	plan := compileTableReshapePhysicalPlan(t, &oracleReshape{
		Kind: oracleReshapeUnpivot,
		Unpivot: &oracleUnpivot{
			ConstructionID: "no_source_limit", Inputs: []oracleUnpivotInput{{Column: "value", Key: recipeTableString("one")}},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	})

	unpivot := findUnpivot(t, plan)
	terminal := operationIndex(plan, ir.PhysicalReturnOp)
	unpivot.InputProjections = plan.Operations[terminal].Return.Projections
	plan.StageSequence = nil
	plan.Operations = append(plan.Operations[:terminal], ir.PhysicalOperation{Kind: ir.PhysicalUnpivotOp, Unpivot: unpivot}, ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "amount", Value: ir.PhysicalValue{Variable: unpivot.OutputRowVariable, Path: []string{"amount"}}}}}})
	if err := plan.Validate(); err != nil {
		t.Fatalf("terminal physical fixture invalid before fault: %v", err)
	}
	plan.BindVars[genericPhysicalExecutionLimitBind] = 2
	reshapeIndex := operationIndex(plan, ir.PhysicalUnpivotOp)
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[reshapeIndex+1:], plan.Operations[reshapeIndex:])
	plan.Operations[reshapeIndex] = ir.PhysicalOperation{
		Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "LIMIT cannot precede a terminal table reshape") {
		t.Fatalf("Validate() = %v, want source LIMIT rejection", err)
	}
}

func TestExpandedWindowSortsStableIdentityBeforeUnrelatedSets(t *testing.T) {
	output := compileWindowExpansionOutput(t)
	window, err := WithGenericPhysicalExecutionWindow(output.Plan, 25)
	if err != nil {
		t.Fatal(err)
	}
	unnestIndex, sortIndex, limitIndex, siblingSetIndex := -1, -1, -1, -1
	var unnest *ir.PhysicalUnnest
	for index, operation := range window.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			unnestIndex, unnest = index, operation.Unnest
		}
		if operation.Kind == ir.PhysicalSortOp {
			sortIndex = index
		}
		if operation.Kind == ir.PhysicalLimitOp {
			limitIndex = index
		}
		if operation.Kind == ir.PhysicalSetOp && operation.Source.SemanticNode == "focus" {
			siblingSetIndex = index
		}
	}
	if unnest == nil || unnestIndex >= sortIndex || sortIndex+1 != limitIndex || limitIndex >= siblingSetIndex {
		t.Fatalf("expanded window positions: unnest=%d sort=%d limit=%d sibling-set=%d", unnestIndex, sortIndex, limitIndex, siblingSetIndex)
	}
	sort := window.Operations[sortIndex].Sort
	wantKeys := ir.PhysicalUnnestSortKeys(*unnest)
	if len(sort.Keys) != len(wantKeys) {
		t.Fatalf("sort key count = %d, want %d: %#v", len(sort.Keys), len(wantKeys), sort.Keys)
	}
	for index := range wantKeys {
		if !reflect.DeepEqual(sort.Keys[index], wantKeys[index]) {
			t.Fatalf("sort key %d = %#v, want %#v", index, sort.Keys[index], wantKeys[index])
		}
	}
	rendered, err := aql.RenderPhysicalPlan(window)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	rootRoute := strings.Index(query, "FOR __loom_expansion_edge_1 IN @@expansion_route_1_edge_collection")
	terminalRoute := strings.Index(query, "FOR __loom_expansion_edge_2 IN @@expansion_route_2_edge_collection")
	windowSort := strings.Index(query, "SORT root._key ASC, __loom_expansion_edge_1._key ASC, __loom_expansion_edge_2._key ASC, __loom_expansion_node_2._key ASC, __loom_has_expanded_item ASC, position ASC")
	siblingSet := strings.Index(query, "LET child_set_1 =")
	if rootRoute < 0 || terminalRoute <= rootRoute || windowSort <= terminalRoute || siblingSet <= windowSort {
		t.Fatalf("query did not keep owner route, stable expanded window, and unrelated set ordered:\n%s", query)
	}
	if strings.Contains(query[:windowSort], "LET child_set_1 =") || strings.Count(query, "FILTER ASSERT(LENGTH(__loom_physical_unnest_source_0) > 0") != 1 {
		t.Fatalf("selected route was rematerialized or expansion boundary was duplicated:\n%s", query)
	}
}

func compileWindowExpansionOutput(t *testing.T) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "expanded-window", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "ExpandedDocuments", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
			TraversalColumnNaming: recipe.TraversalColumnNamingAlias,
			Fields:                []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Expand:                &recipe.Expansion{OwnerOccurrenceID: "document-owner", From: recipe.Expression{Select: "document.identifier[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionError},
			Identity:              &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
			Traversals: []recipe.Traversal{
				{
					Name: "subject_Patient", OccurrenceID: "specimen-owner", Alias: "specimen", ToResourceType: "Specimen",
					Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "specimen.id"}}},
					Traversals: []recipe.Traversal{{
						Name: "subject_Specimen", OccurrenceID: "document-owner", Alias: "document", ToResourceType: "DocumentReference",
						Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "document.id"}}},
					}},
				},
				{
					Name: "focus_Patient", OccurrenceID: "focus-owner", Alias: "focus", ToResourceType: "Observation",
					Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "focus.id"}}},
				},
			},
		}},
	}
	plan, err := recipeplan.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}

func TestCanonicalExecutionPhysicalPlanPreservesValidRelatedCellTrace(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionRelatedSourceCellTraceRecipeOutput())
	physical := ir.ClonePhysicalPlan(output.Plan)
	var err error
	physical, err = WithGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		t.Fatal(err)
	}
	lineage, err := celltrace.ConstructionCellTraceLineage(output, *physical.StageSequence, "observation_status")
	if err != nil {
		t.Fatal(err)
	}
	physical.BindVars["cell_trace_offset"] = 0
	physical.BindVars["cell_trace_limit"] = 10
	physical.BindVars["cell_trace_fetch_limit"] = 11
	physical.StageSequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{
		Construction: lineage, OffsetBindKey: "cell_trace_offset", LimitBindKey: "cell_trace_limit", FetchLimitBindKey: "cell_trace_fetch_limit",
	}
	if err := physical.Validate(); err != nil {
		t.Fatalf("compiled related-source trace fixture is invalid: %v", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		t.Fatalf("compiled related-source trace scope is invalid: %v", err)
	}
	if lineage.RelatedSource == nil || len(lineage.RelatedSource.Subplan.Operations) == 0 {
		t.Fatal("fixture lacks its valid related-source subplan")
	}
	wantSource := lineage.RelatedSource.Subplan.Operations[0].Source
	if wantSource == (ir.PhysicalSource{}) {
		t.Fatal("fixture related-source operation has no nonempty Source metadata")
	}
	canonical := ir.CanonicalExecutionPhysicalPlan(physical)
	gotSource := canonical.StageSequence.CellTraceReturn.Construction.RelatedSource.Subplan.Operations[0].Source
	if gotSource != wantSource {
		t.Fatalf("canonicalization changed valid related trace source: got=%+v want=%+v", gotSource, wantSource)
	}
	if physical.StageSequence.CellTraceReturn.Construction.RelatedSource.Subplan.Operations[0].Source != wantSource {
		t.Fatal("canonicalization mutated the validated source plan")
	}
	data, err := ir.MarshalLegacyPhysical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got, want := hex.EncodeToString(sum[:]), "6f20d9b42c0ed14ccd2bdc1dbbc95cc4ddcf59145180de1f570a8a57d768ffbe"; got != want {
		t.Fatalf("valid construction related trace canonical fingerprint = %s, want unchanged %s", got, want)
	}
}
