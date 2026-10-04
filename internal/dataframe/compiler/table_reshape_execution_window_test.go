package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestGroupedPivotPreviewWindowRunsAfterReshapedRows(t *testing.T) {
	reshape := &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "preview_pivot", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: recipeTableInteger(0), Output: "zero", Label: "Zero"},
				{Key: recipeTableInteger(1), Output: "one", Label: "One"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	}
	plan := compileTableReshapePhysicalPlan(t, reshape)
	pivot := findGroupedPivot(t, plan)
	windowed, err := withGenericPhysicalExecutionWindow(plan, 5)
	if err != nil {
		t.Fatal(err)
	}
	reshapeIndex := operationIndex(windowed, ir.PhysicalGroupedPivotOp)
	sortIndex := operationIndex(windowed, ir.PhysicalSortOp)
	limitIndex := operationIndex(windowed, ir.PhysicalLimitOp)
	returnIndex := operationIndex(windowed, ir.PhysicalReturnOp)
	if !(reshapeIndex < sortIndex && sortIndex < limitIndex && limitIndex < returnIndex) {
		t.Fatalf("pivot window order reshape=%d sort=%d limit=%d return=%d", reshapeIndex, sortIndex, limitIndex, returnIndex)
	}
	if got := windowed.Operations[sortIndex].Sort.Keys; len(got) != 1 || got[0].Variable != pivot.OutputRowVariable || len(got[0].Path) != 1 || got[0].Path[0] != "__loom_row_id" {
		t.Fatalf("pivot sort key = %#v, want reshaped stable row identity", got)
	}

	rendered, err := aql.RenderPhysicalPlan(windowed)
	if err != nil {
		t.Fatal(err)
	}
	collectIndex := strings.Index(rendered.Query, "COLLECT ")
	shapeOutputIndex := strings.Index(rendered.Query, "LET "+pivot.OutputRowVariable+" = ")
	sortText := "SORT " + pivot.OutputRowVariable + ".__loom_row_id ASC"
	sortTextIndex := strings.Index(rendered.Query, sortText)
	limitTextIndex := strings.Index(rendered.Query, "LIMIT @limit")
	if !(collectIndex >= 0 && collectIndex < shapeOutputIndex && shapeOutputIndex < sortTextIndex && sortTextIndex < limitTextIndex) {
		t.Fatalf("pivot query does not aggregate all source rows before deterministic preview window: %s", rendered.Query)
	}
	if strings.Count(rendered.Query, "LIMIT @limit") != 1 {
		t.Fatalf("pivot query has a misplaced or duplicate source-row limit: %s", rendered.Query)
	}
}

func TestUnpivotPreviewWindowBoundsDeterministicReshapedRows(t *testing.T) {
	reshape := &recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			ConstructionID: "preview_unpivot", Inputs: []recipe.UnpivotInput{
				{Column: "value", Key: recipeTableString("first")},
				{Column: "value2", Key: recipeTableString("second")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	}
	plan := compileTableReshapePhysicalPlan(t, reshape)
	unpivot := findUnpivot(t, plan)
	windowed, err := withGenericPhysicalExecutionWindow(plan, 7)
	if err != nil {
		t.Fatal(err)
	}
	reshapeIndex := operationIndex(windowed, ir.PhysicalUnpivotOp)
	sortIndex := operationIndex(windowed, ir.PhysicalSortOp)
	limitIndex := operationIndex(windowed, ir.PhysicalLimitOp)
	returnIndex := operationIndex(windowed, ir.PhysicalReturnOp)
	if !(reshapeIndex < sortIndex && sortIndex < limitIndex && limitIndex < returnIndex) {
		t.Fatalf("unpivot window order reshape=%d sort=%d limit=%d return=%d", reshapeIndex, sortIndex, limitIndex, returnIndex)
	}
	if got := windowed.Operations[sortIndex].Sort.Keys; len(got) != 1 || got[0].Variable != unpivot.OutputRowVariable || len(got[0].Path) != 1 || got[0].Path[0] != "__loom_row_id" {
		t.Fatalf("unpivot sort key = %#v, want reshaped stable row identity", got)
	}
	rendered, err := aql.RenderPhysicalPlan(windowed)
	if err != nil {
		t.Fatal(err)
	}
	loopIndex := strings.Index(rendered.Query, "FOR "+unpivot.SlotVariable+" IN [")
	sortTextIndex := strings.Index(rendered.Query, "SORT "+unpivot.OutputRowVariable+".__loom_row_id ASC")
	limitIndexInQuery := strings.Index(rendered.Query, "LIMIT @limit")
	if !(loopIndex >= 0 && loopIndex < sortTextIndex && sortTextIndex < limitIndexInQuery) {
		t.Fatalf("unpivot query does not limit deterministic expanded rows: %s", rendered.Query)
	}
	if strings.Count(rendered.Query, "LIMIT @limit") != 1 {
		t.Fatalf("unpivot query must contain exactly one output-row limit: %s", rendered.Query)
	}
}

func compileTableReshapePhysicalPlan(t *testing.T, reshape *recipe.TableReshape) ir.PhysicalPlan {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "reshape-window", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient", TableReshape: reshape,
			Fields: []recipe.Field{
				{Name: "group", Expr: recipe.Expression{Select: "gender"}},
				{Name: "category", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "value", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "value2", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
				{Name: "keep", Expr: recipe.Expression{Select: "id"}},
			},
		}},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
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
	plan := compileTableReshapePhysicalPlan(t, &recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			ConstructionID: "limit_bind", Inputs: []recipe.UnpivotInput{{Column: "value", Key: recipeTableString("one")}},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullDrop,
		},
	})
	windowed, err := withGenericPhysicalExecutionWindow(plan, 11)
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
	windowed := withoutUnusedTerminalPivotPresenceCompanions(clonePhysicalPlan(original))

	if !hasPresenceOutput(original.Operations) || !hasStageColumn(original.StageSequence.SourceColumns, "category_present") {
		t.Fatal("pruning mutated the source plan and lost its category-presence companion")
	}
	if hasPresenceOutput(windowed.Operations) || hasStageColumn(windowed.StageSequence.SourceColumns, "category_present") ||
		hasStageColumn(windowed.StageSequence.Stages[0].InputColumns, "category_present") ||
		hasProjection(windowed.StageSequence.Stages[0].GroupedPivot.InputProjections, "category_present") {
		t.Fatal("ordinary terminal Pivot preview retained an unused category-presence companion")
	}

	missingCategory := clonePhysicalPlan(original)
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
	plan := compileTableReshapePhysicalPlan(t, &recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			ConstructionID: "no_source_limit", Inputs: []recipe.UnpivotInput{{Column: "value", Key: recipeTableString("one")}},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	})
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
