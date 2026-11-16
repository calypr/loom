package aql

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestPruneUnusedSourcePivotProjectionsKeepsSemanticAndHiddenInputs(t *testing.T) {
	plan, sequence, stages := sourcePivotProjectionFixture()
	pruneUnusedSourcePivotProjections(&plan, sequence, stages)

	want := []string{"group", "category", "value", "row_value", "contributors", "category_present", "runtime_metadata"}
	if got := projectionNames(plan.Operations[len(plan.Operations)-1].Return.Projections); !reflect.DeepEqual(got, want) {
		t.Fatalf("source RETURN projections = %v, want %v", got, want)
	}
	if got := projectionNames(stages[0].GroupedPivot.InputProjections); !reflect.DeepEqual(got, want) {
		t.Fatalf("Pivot input projections = %v, want %v", got, want)
	}
	if stages[0].GroupedPivot.CategoryPresenceColumn != "category_present" || !stages[0].GroupedPivot.CategoryPresenceFromInput {
		t.Fatalf("Pivot presence sidecar was lost: %+v", stages[0].GroupedPivot)
	}
	if stages[0].GroupedPivot.RowValues[0].InputColumn != "row_value" || stages[0].GroupedPivot.RootContributorInputColumn != "contributors" {
		t.Fatalf("Pivot row values or contributor input was lost: %+v", stages[0].GroupedPivot)
	}

	for _, kind := range []ir.PhysicalOperationKind{
		ir.PhysicalRootScanOp, ir.PhysicalFilterOp, ir.PhysicalSortOp, ir.PhysicalLimitOp, ir.PhysicalReturnOp,
	} {
		if !hasOperation(plan.Operations, kind) {
			t.Errorf("source operation %q was removed", kind)
		}
	}
	if !hasDerivedLet(plan.Operations, "filter_input") {
		t.Fatal("producer referenced by the source filter was pruned")
	}
	if hasDerivedLet(plan.Operations, "unused_source_value") {
		t.Fatal("unreferenced source producer was retained")
	}
}

func TestPruneUnusedSourcePivotProjectionsDropsOnlyUnusedDirectColumns(t *testing.T) {
	basePlan, baseSequence, baseStages := sourcePivotProjectionFixture()
	widePlan, wideSequence, wideStages := sourcePivotProjectionFixture()
	widePlan.Operations[len(widePlan.Operations)-1].Return.Projections = append(
		widePlan.Operations[len(widePlan.Operations)-1].Return.Projections,
		directSourceProjection("unused_wide_column", false),
	)
	wideStages[0].GroupedPivot.InputProjections = append(
		wideStages[0].GroupedPivot.InputProjections,
		directStageProjection("unused_wide_column", false),
	)

	pruneUnusedSourcePivotProjections(&basePlan, baseSequence, baseStages)
	pruneUnusedSourcePivotProjections(&widePlan, wideSequence, wideStages)
	baseReturn := basePlan.Operations[len(basePlan.Operations)-1].Return.Projections
	wideReturn := widePlan.Operations[len(widePlan.Operations)-1].Return.Projections
	if !reflect.DeepEqual(baseReturn, wideReturn) {
		t.Fatalf("adding unused direct columns changed the retained source projection: base=%+v wide=%+v", baseReturn, wideReturn)
	}
	if !reflect.DeepEqual(baseStages[0].GroupedPivot, wideStages[0].GroupedPivot) {
		t.Fatalf("adding unused direct columns changed the Pivot inputs or outputs:\nbase=%+v\nwide=%+v", baseStages[0].GroupedPivot, wideStages[0].GroupedPivot)
	}
}

func TestPruneUnusedSourcePivotProjectionsFallsBackForDiagnosticsAndComputedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ir.PhysicalPlan, *ir.PhysicalStageSequence, []ir.PhysicalConstructionStage)
	}{
		{"coded correlation", func(_ *ir.PhysicalPlan, _ *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage) {
			stages[0].GroupedPivot.CodedCorrelation = &ir.PhysicalCorrelation{}
		}},
		{"computed presence", func(_ *ir.PhysicalPlan, _ *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage) {
			stages[0].GroupedPivot.CategoryPresence = &ir.PhysicalProjectionPresence{Source: ir.PhysicalValue{Variable: "source_row"}, Paths: [][]string{{"category"}}}
		}},
		{"cell trace", func(_ *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, _ []ir.PhysicalConstructionStage) {
			sequence.CellTraceReturn = &ir.PhysicalCellTraceReturn{}
		}},
		{"row lineage", func(_ *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, _ []ir.PhysicalConstructionStage) {
			sequence.RowLineageReturn = &ir.PhysicalRowLineageReturn{}
		}},
		{"population mapping", func(_ *ir.PhysicalPlan, sequence *ir.PhysicalStageSequence, _ []ir.PhysicalConstructionStage) {
			sequence.PopulationMappingReturn = &ir.PhysicalStagePopulationMappingReturn{}
		}},
		{"computed input", func(_ *ir.PhysicalPlan, _ *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage) {
			stages[0].GroupedPivot.InputProjections[0].Expression = &ir.PhysicalExpression{}
		}},
		{"presence expression", func(_ *ir.PhysicalPlan, _ *ir.PhysicalStageSequence, stages []ir.PhysicalConstructionStage) {
			stages[0].GroupedPivot.InputProjections[0].Presence = &ir.PhysicalProjectionPresence{Source: ir.PhysicalValue{Variable: "source_row"}, Paths: [][]string{{"group"}}}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, sequence, stages := sourcePivotProjectionFixture()
			test.mutate(&plan, sequence, stages)
			beforeSource := projectionNames(plan.Operations[len(plan.Operations)-1].Return.Projections)
			beforeInputs := projectionNames(stages[0].GroupedPivot.InputProjections)
			beforeOperations := len(plan.Operations)
			pruneUnusedSourcePivotProjections(&plan, sequence, stages)
			if len(plan.Operations) != beforeOperations || !reflect.DeepEqual(
				projectionNames(plan.Operations[len(plan.Operations)-1].Return.Projections), beforeSource,
			) {
				t.Fatal("fallback modified source operations")
			}
			if !reflect.DeepEqual(projectionNames(stages[0].GroupedPivot.InputProjections), beforeInputs) {
				t.Fatal("fallback modified GroupedPivot inputs")
			}
		})
	}
}

func TestGroupedPivotListedPredicateFactorsTypedValuesAndKeepsPresenceCases(t *testing.T) {
	pivot := ir.PhysicalGroupedPivot{
		Categories: []ir.PhysicalGroupedPivotCategory{
			{Output: "null_value", MatchKind: ir.PhysicalPivotCategoryNullMatch},
			{Output: "missing_value", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
			{Output: "false_value", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "false_value", ValueKind: "BOOLEAN"},
			{Output: "zero_value", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "zero_value", ValueKind: "INTEGER"},
			{Output: "empty_text", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "empty_text", ValueKind: "STRING"},
		},
	}
	predicate, err := groupedPivotListedPredicate(pivot, "row", "category_name", "category_presence", "category_type")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"row[@category_presence] == true AND row[@category_name] == null",
		"NOT (row[@category_presence] == true)",
		"row[@category_presence] == true AND TYPENAME(row[@category_name]) == @category_type AND row[@category_name] IN [@false_value, @zero_value, @empty_text]",
	} {
		if !strings.Contains(predicate, fragment) {
			t.Errorf("listed predicate %q omitted %q", predicate, fragment)
		}
	}
	if got := strings.Count(predicate, "TYPENAME("); got != 1 {
		t.Fatalf("typed category guard repeated %d times in factored predicate: %s", got, predicate)
	}
	notPredicate := "NOT " + predicate
	if !strings.HasPrefix(notPredicate, "NOT ((") || !strings.HasSuffix(notPredicate, "))") {
		t.Fatalf("unlisted check must negate the complete parenthesized predicate: %s", notPredicate)
	}
}

func TestGroupedPivotListedPredicateQueryGrowthIsBoundedByOneTypedGuard(t *testing.T) {
	categories := make([]ir.PhysicalGroupedPivotCategory, 0, 24)
	for index := range 24 {
		categories = append(categories, ir.PhysicalGroupedPivotCategory{
			Output: "value_" + itoa(index), MatchKind: ir.PhysicalPivotCategoryValueMatch,
			ValueBindKey: "value_" + itoa(index), ValueKind: "STRING",
		})
	}
	predicate, err := groupedPivotListedPredicate(ir.PhysicalGroupedPivot{Categories: categories}, "row", "column", "", "type")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(predicate, "TYPENAME(") != 1 || strings.Count(predicate, " IN [") != 1 {
		t.Fatalf("24-category predicate repeated shared guards instead of using one typed membership list: %s", predicate)
	}
	if got := strings.Count(predicate, " == @value_"); got != 0 {
		t.Fatalf("24-category predicate retained per-value equality disjunctions: count=%d", got)
	}
	notPredicate := "NOT " + predicate
	if !strings.HasPrefix(notPredicate, "NOT ((") || !strings.HasSuffix(notPredicate, "))") {
		t.Fatalf("NOT control does not wrap the full listed predicate: %s", notPredicate)
	}
}

func sourcePivotProjectionFixture() (ir.PhysicalPlan, *ir.PhysicalStageSequence, []ir.PhysicalConstructionStage) {
	projectionNames := []string{"group", "category", "value", "row_value", "contributors", "category_present", "runtime_metadata", "unused_source_column"}
	projections := make([]ir.PhysicalProjection, 0, len(projectionNames))
	inputs := make([]ir.PhysicalProjection, 0, len(projectionNames))
	for _, name := range projectionNames {
		hidden := name == "category_present" || name == "runtime_metadata"
		projections = append(projections, directSourceProjection(name, hidden))
		inputs = append(inputs, directStageProjection(name, hidden))
	}
	plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "filter_input", Operator: "IDENTITY", Inputs: []ir.PhysicalValue{{Variable: "root", Path: []string{"active"}}}}},
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "unused_source_value", Operator: "IDENTITY", Inputs: []ir.PhysicalValue{{Variable: "root", Path: []string{"unused"}}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "filter_input"}, Right: &ir.PhysicalValue{BindKey: "active_value"}}}},
		{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"_key"}}}}},
		{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "source_limit"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
	}}
	pivot := ir.PhysicalGroupedPivot{
		InputRowVariable: "source_row", InputProjections: inputs,
		GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group", Variable: "group_value", Kind: "STRING"}},
		CategoryColumn: "category", CategoryPresenceColumn: "category_present", CategoryPresenceFromInput: true,
		ValueColumn: "value", RowValues: []ir.PhysicalStageRowValue{{InputColumn: "row_value", Output: "row_value_out"}},
		RootContributorInputColumn: "contributors", Categories: []ir.PhysicalGroupedPivotCategory{{
			Output: "selected", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "selected_category", ValueKind: "STRING",
		}},
	}
	stages := []ir.PhysicalConstructionStage{{
		ID: "pivot", InputStageID: "source", Kind: ir.PhysicalStagePivotOp,
		InputRowVariable: "source_row", GroupedPivot: &pivot,
	}}
	sequence := &ir.PhysicalStageSequence{SourceStageID: "source", Stages: stages, OutputAuthResourcePathBindKey: "auth_path"}
	return plan, sequence, stages
}

func directSourceProjection(name string, hidden bool) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Hidden: hidden, Value: ir.PhysicalValue{Variable: "root", Path: []string{name}}}
}

func directStageProjection(name string, hidden bool) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Hidden: hidden, Value: ir.PhysicalValue{Variable: "source_row", Path: []string{name}}}
}

func projectionNames(projections []ir.PhysicalProjection) []string {
	names := make([]string, 0, len(projections))
	for _, projection := range projections {
		names = append(names, projection.Name)
	}
	return names
}

func hasOperation(operations []ir.PhysicalOperation, kind ir.PhysicalOperationKind) bool {
	for _, operation := range operations {
		if operation.Kind == kind {
			return true
		}
	}
	return false
}

func hasDerivedLet(operations []ir.PhysicalOperation, variable string) bool {
	for _, operation := range operations {
		if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet != nil && operation.DerivedLet.Variable == variable {
			return true
		}
	}
	return false
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
