package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type oracleReshape struct {
	Kind         string
	GroupedPivot *oraclePivot
	Unpivot      *oracleUnpivot
}

const (
	oracleReshapePivot   = "GROUPED_PIVOT"
	oracleReshapeUnpivot = "UNPIVOT"
)

type oraclePivot struct {
	ConstructionID              string
	GroupKeys                   []string
	CategoryColumn, ValueColumn string
	Categories                  []oraclePivotCategory
	DuplicatePolicy             recipe.PivotDuplicatePolicy
	MissingCellPolicy           recipe.PivotMissingCellPolicy
	UnlistedCategoryPolicy      recipe.PivotUnlistedCategoryPolicy
}

type oraclePivotCategory struct {
	Key           recipe.TableScalar
	Output, Label string
}

type oracleUnpivot struct {
	ConstructionID                               string
	Inputs                                       []oracleUnpivotInput
	KeyOutput, KeyLabel, ValueOutput, ValueLabel string
	NullRowPolicy                                recipe.UnpivotNullRowPolicy
}

type oracleUnpivotInput struct {
	Column string
	Key    recipe.TableScalar
}

func bindOracleCurrentConstruction(output recipe.Output, shape *oracleReshape) recipe.Output {
	if shape == nil {
		return output
	}
	construction := &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{}}
	byName := map[string]recipe.StageColumn{}
	for i := range output.Fields {
		field := &output.Fields[i]
		field.ColumnID = field.Name
		column := recipe.StageColumn{ID: field.Name, Name: field.Name, Label: field.Label}
		construction.SourceColumns = append(construction.SourceColumns, column)
		byName[column.Name] = column
	}
	step := recipe.ConstructionStep{Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}}
	switch shape.Kind {
	case oracleReshapePivot:
		p := shape.GroupedPivot
		step.ID = "pivot"
		pivot := &recipe.ConstructionPivot{ConstructionID: p.ConstructionID, GroupKeyIDs: append([]string{}, p.GroupKeys...), CategoryColumnID: p.CategoryColumn, ValueColumnID: p.ValueColumn, DuplicatePolicy: p.DuplicatePolicy, MissingCellPolicy: p.MissingCellPolicy, UnlistedCategoryPolicy: p.UnlistedCategoryPolicy}
		for _, name := range p.GroupKeys {
			column := byName[name]
			if column.ID == "" {
				column = recipe.StageColumn{ID: name, Name: name}
			}
			step.Outputs = append(step.Outputs, column)
		}
		for _, category := range p.Categories {
			pivot.Categories = append(pivot.Categories, recipe.ConstructionPivotCategory{Key: category.Key, OutputColumnID: category.Output})
			step.Outputs = append(step.Outputs, recipe.StageColumn{ID: category.Output, Name: category.Output, Label: category.Label})
		}
		step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: pivot}
	case oracleReshapeUnpivot:
		u := shape.Unpivot
		step.ID = "unpivot"
		unpivot := &recipe.ConstructionUnpivot{ConstructionID: u.ConstructionID, KeyOutputColumnID: u.KeyOutput, ValueOutputColumnID: u.ValueOutput, NullRowPolicy: u.NullRowPolicy}
		selected := map[string]bool{}
		for _, input := range u.Inputs {
			unpivot.Inputs = append(unpivot.Inputs, recipe.ConstructionUnpivotInput{ColumnID: input.Column, Key: input.Key})
			selected[input.Column] = true
		}
		for _, column := range construction.SourceColumns {
			if !selected[column.ID] {
				step.Outputs = append(step.Outputs, column)
			}
		}
		step.Outputs = append(step.Outputs, recipe.StageColumn{ID: u.KeyOutput, Name: u.KeyOutput, Label: u.KeyLabel}, recipe.StageColumn{ID: u.ValueOutput, Name: u.ValueOutput, Label: u.ValueLabel})
		step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: unpivot}
	default:
		step.ID = "invalid-shape"
	}
	construction.Steps = append(construction.Steps, step)
	output.Construction = construction
	return output
}

func bindOracleCurrentDerived(output recipe.Output) recipe.Output {
	if output.Construction == nil || len(output.DerivedColumns) == 0 {
		return output
	}
	construction := *output.Construction
	construction.Steps = append([]recipe.ConstructionStep{}, construction.Steps...)
	for _, column := range output.DerivedColumns {
		previous := construction.Steps[len(construction.Steps)-1]
		outputs := append(append([]recipe.StageColumn{}, previous.Outputs...), recipe.StageColumn{ID: column.Name, Name: column.Name, Label: column.Label})
		operand := func(input recipe.DerivedOperand) recipe.ConstructionOperand {
			return recipe.ConstructionOperand{Kind: input.Kind, ColumnID: input.Column, Literal: input.Literal}
		}
		construction.Steps = append(construction.Steps, recipe.ConstructionStep{ID: column.ConstructionID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: previous.ID}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{ConstructionID: column.ConstructionID, OutputColumnID: column.Name, Operation: column.Operation, Left: operand(column.Left), Right: operand(column.Right), MissingInputPolicy: column.MissingInputPolicy, DivisionByZeroPolicy: column.DivisionByZeroPolicy}}, Outputs: outputs})
	}
	output.Construction = &construction
	output.DerivedColumns = nil
	return output
}

func TestS04CurrentConstructionFixtureCompilation(t *testing.T) {
	output := reshapeOracleOutput("sum", &oracleReshape{Kind: oracleReshapePivot, GroupedPivot: &oraclePivot{ConstructionID: "s04-pivot-sum", GroupKeys: []string{"group_text", "group_number"}, CategoryColumn: "string_category", ValueColumn: "numeric_value", Categories: []oraclePivotCategory{{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"}, {Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"}}, DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence}})
	output.DerivedColumns = []recipe.DerivedColumn{{ConstructionID: "s04-derived-after-pivot", Name: "alpha_plus_zero", Operation: recipe.DerivedAdd, Left: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "alpha"}, Right: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "zero"}, MissingInputPolicy: recipe.MissingInputPropagateNull}}
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 || compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStagePivotOp || compiled.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageDeriveOp {
		t.Fatal("S04 fixture bypassed current Pivot/Derive stages")
	}
	for _, stage := range compiled.Plan.StageSequence.Stages {
		found := false
		for _, column := range stage.OutputColumns {
			if column.Name == "__loom_reshape_unlisted_count" && column.Internal {
				found = true
			}
		}
		if !found {
			t.Fatalf("stage %s dropped compiler-owned Pivot exclusion count", stage.ID)
		}
	}
	foundCount := false
	for _, column := range compiled.Plan.StageSequence.FinalColumns {
		if column.Name == "__loom_reshape_unlisted_count" && column.Internal {
			foundCount = true
		}
	}
	if !foundCount {
		t.Fatal("final output dropped Pivot exclusion count")
	}
	if _, err := celltrace.CompileCellTraceOutputWithPolicy(compiled, "alpha_plus_zero", 0, 100, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 100, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
		t.Fatal(err)
	}
	assertReshapeOracleUnpivotRejectsIncompatibleInputs(t, "project", "generation")
	for _, policy := range []recipe.UnpivotNullRowPolicy{recipe.UnpivotNullPreserve, recipe.UnpivotNullDrop} {
		compiled, _, err := compileReshapeOracle(reshapeOracleUnpivotOutput(policy, "s04-unpivot"), "project", "generation", 100)
		if err != nil {
			t.Fatal(err)
		}
		if compiled.Plan.StageSequence == nil || compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageUnpivotOp {
			t.Fatal("S04 fixture bypassed current Unpivot stage")
		}
		if outputColumn(compiled.OutputSchema, "amount") != "decimal" {
			t.Fatal("S04 integer/decimal unpivot widening changed")
		}
	}
}

func currentNullMissingExclusionOutput() recipe.Output {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)
	output.Name = "current_null_missing_exclusions"
	output.Construction.Steps[0].Operation.Pivot.Categories = []recipe.ConstructionPivotCategory{
		{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_value"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_value"},
	}
	output.Construction.Steps[0].Outputs = []recipe.StageColumn{
		{ID: "group_text", Name: "group_text"},
		{ID: "group_number", Name: "group_number"},
		{ID: "null_value", Name: "null_value"},
		{ID: "missing_value", Name: "missing_value"},
	}
	return output
}

func TestCurrentPivotExclusionsPreserveSourceCategoryPresence(t *testing.T) {
	for _, test := range []struct {
		name      string
		output    recipe.Output
		fromInput bool
	}{
		{name: "literal categories", output: currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)},
		{name: "NULL and MISSING categories", output: currentNullMissingExclusionOutput(), fromInput: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, _, err := compileCurrentConstructionOracle(test.output, "private-project", "generation", 100)
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.Plan.Validate(); err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			original := compiled.Plan.StageSequence.Stages[0].GroupedPivot
			if original.CategoryPresenceFromInput != test.fromInput {
				t.Fatalf("category-presence path = %t, want %t", original.CategoryPresenceFromInput, test.fromInput)
			}
			source := compiled.Plan.Operations[len(compiled.Plan.Operations)-1].Return.Projections
			var originalCategory *ir.PhysicalProjection
			for i := range source {
				if source[i].Name == original.CategoryColumn {
					originalCategory = &source[i]
				}
			}
			if originalCategory == nil {
				t.Fatal("source category projection is missing")
			}
			replayed, err := sourceStagePivotForExclusions(ir.ClonePhysicalPlan(compiled.Plan))
			if err != nil {
				t.Fatal(err)
			}
			if err := replayed.Validate(); test.fromInput && err != nil {
				t.Fatalf("replayed exclusion input invalid: %v", err)
			}
			var pivot *ir.PhysicalGroupedPivot
			for _, operation := range replayed.Operations {
				if operation.GroupedPivot != nil {
					pivot = operation.GroupedPivot
				}
			}
			if pivot == nil {
				t.Fatal("replayed Pivot is missing")
			}
			if test.fromInput && (len(pivot.Categories) != 2 || pivot.Categories[0].MatchKind != ir.PhysicalPivotCategoryNullMatch || pivot.Categories[1].MatchKind != ir.PhysicalPivotCategoryMissingMatch) {
				t.Fatal("replay changed the typed NULL/MISSING partition")
			}
			if pivot.CategoryPresenceColumn != original.CategoryPresenceColumn || pivot.CategoryPresenceFromInput != original.CategoryPresenceFromInput {
				t.Fatal("replay changed the compiler-owned presence companion")
			}
			categoryFound, companionFound := false, false
			for _, projection := range pivot.InputProjections {
				if projection.Name == pivot.CategoryPresenceColumn {
					companionFound = true
				}
				if projection.Name != pivot.CategoryColumn {
					continue
				}
				categoryFound = true
				if test.fromInput {
					if originalCategory.Presence == nil {
						t.Fatal("fixture did not exercise duplicate source-presence metadata")
					}
					if projection.Presence != nil {
						t.Fatal("category projection duplicates presence already supplied by the companion")
					}
				} else if !reflect.DeepEqual(projection.Presence, originalCategory.Presence) {
					t.Fatal("literal category replay changed ordinary source-presence metadata")
				}
			}
			if !categoryFound || test.fromInput && !companionFound {
				t.Fatal("source category or presence companion was dropped")
			}
			query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if query.Query == "" || query.BindVars["table_shape_exclusion_fetch_limit"] != 11 {
				t.Fatal("exclusion query did not retain bounded paging")
			}
			after, err := json.Marshal(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("exclusion replay mutated the current source plan")
			}
		})
	}
}

func TestCurrentConstructionEvidencePrototype(t *testing.T) {
	for _, test := range []struct {
		name    string
		output  recipe.Output
		columns []string
	}{
		{"pivot", currentOraclePivotOutput(recipe.PivotUnlistedCategoryError), []string{"alpha", "group_text"}},
		{"unpivot", currentOracleUnpivotOutput(recipe.UnpivotNullPreserve), []string{"amount", "measure", "group_text"}},
		{"pivot_derive", currentOracleDerivedPivotOutput(false), []string{"alpha_plus_zero"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, _, err := compileCurrentConstructionOracle(test.output, "project", "generation", 100)
			if err != nil {
				t.Fatal(err)
			}
			before, marshalErr := json.Marshal(compiled.Plan)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for _, column := range test.columns {
				trace, err := celltrace.CompileCellTraceOutputWithPolicy(compiled, column, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Fatalf("trace %s: %v", column, err)
				}
				if strings.Contains(trace.Query, "CONSTRUCTION_TRACE_OPERATION_UNSUPPORTED") {
					t.Fatalf("trace %s still omits physical shape lineage", column)
				}
				if column == "measure" {
					generated := strings.Contains(trace.Query, ir.PhysicalCellTraceGeneratedKeyOmission)
					for _, value := range trace.BindVars {
						if text, ok := value.(string); ok && text == ir.PhysicalCellTraceGeneratedKeyOmission {
							generated = true
						}
					}
					if !generated {
						t.Fatal("generated Unpivot key lost its explicit no-source omission")
					}
				}
				if trace.ExplicitIdentityColumn == "" || (column != "measure" && !strings.Contains(trace.Query, "resourceId")) {
					t.Fatalf("trace %s lacks stable identity or source contributor evidence", column)
				}
			}
			after, marshalErr := json.Marshal(compiled.Plan)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("trace compilation mutated the canonical output plan")
			}
		})
	}
}

func TestCurrentConstructionExclusionPrototype(t *testing.T) {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryExcludeWithEvidence)
	compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := CompileTableShapeExclusionsWithPolicy(compiled, 2, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"FILTER NOT (", "TYPENAME(", "LIMIT @table_shape_exclusion_offset, @table_shape_exclusion_fetch_limit"} {
		if !strings.Contains(evidence.Query, needle) {
			t.Fatalf("exclusion terminal lost %q", needle)
		}
	}
	if evidence.BindVars["table_shape_exclusion_offset"] != 2 || evidence.BindVars["table_shape_exclusion_fetch_limit"] != 11 {
		t.Fatalf("exclusion paging changed: %#v", evidence.BindVars)
	}
}

func currentOracleDerivedPivotOutput(withFilter bool) recipe.Output {
	output := currentOraclePivotOutput(recipe.PivotUnlistedCategoryError)
	previous := output.Construction.Steps[0]
	outputs := append(append([]recipe.StageColumn{}, previous.Outputs...), recipe.StageColumn{ID: "alpha_plus_zero", Name: "alpha_plus_zero"})
	derive := recipe.ConstructionStep{ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{ConstructionID: "s04-derived-after-pivot", OutputColumnID: "alpha_plus_zero", Operation: recipe.DerivedAdd, Left: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha"}, Right: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "zero"}, MissingInputPolicy: recipe.MissingInputPropagateNull}}, Outputs: outputs}
	output.Construction.Steps = append(output.Construction.Steps, derive)
	if withFilter {
		zero := 0.0
		output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "alpha_plus_zero", Operator: recipe.FilterGreaterEq, Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &zero}}}}, Outputs: outputs})
	}
	return output
}

func TestCurrentConstructionExclusionPrototypeRefusesPostPivotFilter(t *testing.T) {
	output := currentOracleDerivedPivotOutput(true)
	output.Construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy = recipe.PivotUnlistedCategoryExcludeWithEvidence
	compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err == nil || err.Error() != string(TableShapeExclusionUnsupported) {
		t.Fatalf("post-Pivot filter must not be silently discarded: %v", err)
	}
}

func TestCurrentConstructionTracePrototypeRetainsFilteredStageFallback(t *testing.T) {
	compiled, _, err := compileCurrentConstructionOracle(currentOracleDerivedPivotOutput(true), "project", "generation", 100)
	if err != nil {
		t.Fatal(err)
	}
	original := ir.ClonePhysicalPlan(compiled.Plan)
	returned, ok := celltrace.SourceStageReshapeForTrace(ir.ClonePhysicalPlan(original))
	if ok || !reflect.DeepEqual(returned, original) {
		t.Fatal("filtered Pivot replay was flattened or its stage population changed")
	}
}

func TestCompileTableShapeExclusionsUsesCanonicalGroupedPivotInputs(t *testing.T) {
	output := reshapeOracleOutput("Observations", &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "exclusion-test", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []oraclePivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.DerivedColumns = []recipe.DerivedColumn{{
		ConstructionID: "derived-after-pivot", Name: "after", Label: "After", Operation: recipe.DerivedAdd,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "known"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "known"},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}}
	compiledOutput, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	originalPlan, err := json.Marshal(compiledOutput.Plan)
	if err != nil {
		t.Fatal(err)
	}
	query, err := CompileTableShapeExclusionsWithPolicy(compiledOutput, 7, 12, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	afterPlan, err := json.Marshal(compiledOutput.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterPlan, originalPlan) {
		t.Fatal("exclusion compilation mutated the canonical published plan")
	}
	for _, want := range []string{
		"FILTER NOT (", "TYPENAME(", "GROUPED_PIVOT",
		"LIMIT @table_shape_exclusion_offset, @table_shape_exclusion_fetch_limit",
		"__loom_physical_exclusion_output_row_id", "@__loom_physical_exclusion_category_column",
	} {
		if !strings.Contains(query.Query, want) {
			t.Fatalf("exclusion query missing %q:\n%s", want, query.Query)
		}
	}
	if strings.Contains(query.Query, "derived-after-pivot") || strings.Contains(query.Query, "LET after =") {
		t.Fatalf("diagnostic terminal evaluated work after the grouped pivot:\n%s", query.Query)
	}
	if query.Offset != 7 || query.Limit != 12 || query.BindVars["table_shape_exclusion_offset"] != 7 || query.BindVars["table_shape_exclusion_fetch_limit"] != 13 {
		t.Fatalf("unexpected bounded page metadata: %#v %#v", query, query.BindVars)
	}
	if !containsBindValue(query.BindVars, ir.PhysicalCellTraceSourceDocumentField) || !containsBindValue(query.BindVars, "string_category") || !containsBindValue(query.BindVars, "known") || !containsBindValue(query.BindVars, ir.PhysicalTableShapeExclusionCategoryPresentField) || !containsBindValue(query.BindVars, ir.PhysicalTableShapeExclusionReasonUnlistedCategory) {
		t.Fatalf("query does not bind exact source document and canonical category identity: %#v", query.BindVars)
	}
	if !strings.Contains(query.Query, "HAS(") || !containsBindValue(query.BindVars, "__loom_exclusion_category_present") {
		t.Fatalf("exclusion diagnostic lost source presence for ordinary value categories: %s", query.Query)
	}
	if strings.Contains(query.Query, `"known"`) {
		t.Fatalf("category value was interpolated rather than retained as a typed canonical bind:\n%s", query.Query)
	}
}

func TestCompileTableShapeExclusionsRefusesUnsupportedOrUnconfiguredOutputs(t *testing.T) {
	unsupported := lowerOutputForExclusionTest(t, bindOracleCurrentConstruction(recipe.Output{
		Name: "Unpivot", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
	}, &oracleReshape{Kind: oracleReshapeUnpivot, Unpivot: &oracleUnpivot{
		ConstructionID: "u", Inputs: []oracleUnpivotInput{{Column: "id", Key: reshapeOracleString("id")}},
		KeyOutput: "key", KeyLabel: "Key", ValueOutput: "value", ValueLabel: "Value", NullRowPolicy: recipe.UnpivotNullPreserve,
	}}))
	_, err := CompileTableShapeExclusionsWithPolicy(unsupported, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionUnsupported)

	noPolicy := lowerOutputForExclusionTest(t, bindOracleCurrentConstruction(recipe.Output{
		Name: "NoPolicy", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "category", Expr: recipe.Expression{Select: "root.status"}}, {Name: "value", Expr: recipe.Expression{Select: "root.valueInteger"}}},
	}, &oracleReshape{Kind: oracleReshapePivot, GroupedPivot: &oraclePivot{
		ConstructionID: "p", GroupKeys: []string{"id"}, CategoryColumn: "category", ValueColumn: "value",
		Categories:      []oraclePivotCategory{{Key: reshapeOracleString("known"), Output: "known", Label: "Known"}},
		DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
		UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
	}}))
	_, err = CompileTableShapeExclusionsWithPolicy(noPolicy, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionNoPolicy)

	_, err = CompileTableShapeExclusionsWithPolicy(noPolicy, -1, 10, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionInvalidOffset)
	_, err = CompileTableShapeExclusionsWithPolicy(noPolicy, 0, MaxTableShapeExclusionLimit+1, ir.DefaultPhysicalOptimizationPolicy())
	assertExclusionRefusal(t, err, TableShapeExclusionInvalidLimit)
}

func lowerOutputForExclusionTest(t *testing.T, output recipe.Output) lower.CompiledRecipeOutput {
	t.Helper()
	compiled, _, err := compileReshapeOracle(output, "project", "generation", 10)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func assertExclusionRefusal(t *testing.T, err error, want TableShapeExclusionRefusalCode) {
	t.Helper()
	var refusal *TableShapeExclusionRefusal
	if !errors.As(err, &refusal) || refusal.Code != want {
		t.Fatalf("refusal = %#v, want %s", err, want)
	}
}

func TestCurrentPivotNullMissingExclusionPartitionAgainstArango(t *testing.T) {
	endpoint, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if endpoint == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must name the supplied local Arango fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := store.OpenExisting(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_null_missing_exclusion_" + uuid.NewString()
	const generation = "generation-current-null-missing-exclusion"

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.QueryRows(cleanupCtx, `FOR d IN Observation FILTER d.project == @project AND d.dataset_generation == @generation REMOVE d IN Observation RETURN {_key: OLD._key}`, 10, map[string]any{"project": project, "generation": generation}, func(map[string]any) error { return nil }); err != nil {
			t.Errorf("remove owned exclusion fixture: %v", err)
		}
	}()
	documents := make([]json.RawMessage, 0, 3)
	for _, row := range []struct {
		id    string
		code  map[string]any
		value int
	}{
		{id: "null", code: map[string]any{"text": nil}, value: 10},
		{id: "missing", code: map[string]any{}, value: 20},
		{id: "other", code: map[string]any{"text": "other"}, value: 30},
	} {
		payload := map[string]any{"resourceType": "Observation", "id": row.id, "status": "final", "valueInteger": 1, "code": row.code, "valueQuantity": map[string]any{"value": row.value}}
		raw, err := json.Marshal(map[string]any{"_key": project + "_" + row.id, "id": row.id, "project": project, "project_id": project, "dataset_generation": generation, "resourceType": "Observation", "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, raw)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatal(err)
	}
	compiled, preview, err := compileCurrentConstructionOracle(currentNullMissingExclusionOutput(), project, generation, 10)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(previewRows) != 1 {
		t.Fatalf("preview rows = %#v, want one final group", previewRows)
	}
	row := previewRows[0]
	for name, expected := range map[string]any{"group_text": "final", "group_number": float64(1), "null_value": float64(10), "missing_value": float64(20), "__loom_reshape_unlisted_count": float64(1)} {
		if row[name] != expected {
			t.Fatalf("preview %s = %#v, want %#v", name, row[name], expected)
		}
	}
	const rowID = `["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",1]]`
	if row["__loom_row_id"] != rowID {
		t.Fatalf("preview identity = %#v, want %s", row["__loom_row_id"], rowID)
	}
	query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	exclusions := []map[string]any{}
	if err := client.QueryRows(ctx, query.Query, 10, query.BindVars, func(value map[string]any) error { exclusions = append(exclusions, value); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(exclusions) != 1 {
		t.Fatalf("exclusions = %#v, want only the unlisted other source", exclusions)
	}
	excluded := exclusions[0]
	if excluded[query.ResourceTypeField] != "Observation" || excluded[query.ResourceIDField] != "other" {
		t.Fatalf("excluded identity = %#v, want Observation/other", excluded)
	}
	assertExclusionLiteral(t, excluded, "other", true, "STRING", rowID)
	after, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preview/exclusion compilation mutated the source plan")
	}
}

func TestCurrentPivotExclusionsBindCategoryTypeOnlyForValueMatches(t *testing.T) {
	for _, test := range []struct {
		name        string
		withLiteral bool
	}{
		{name: "NULL and MISSING only"},
		{name: "NULL MISSING and literal", withLiteral: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := currentNullMissingExclusionOutput()
			if test.withLiteral {
				known := "known"
				pivot := output.Construction.Steps[0].Operation.Pivot
				pivot.Categories = append(pivot.Categories, recipe.ConstructionPivotCategory{
					Key:            recipe.TableScalar{Kind: recipe.TableScalarString, String: &known},
					OutputColumnID: "literal_value",
				})
				output.Construction.Steps[0].Outputs = append(output.Construction.Steps[0].Outputs,
					recipe.StageColumn{ID: "literal_value", Name: "literal_value"})
			}
			compiled, _, err := compileCurrentConstructionOracle(output, "project", "generation", 10)
			if err != nil {
				t.Fatal(err)
			}
			query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			const typeBind = "__loom_physical_exclusion_category_type"
			value, bound := query.BindVars[typeBind]
			referenced := strings.Contains(query.Query, "== @"+typeBind+" AND ")
			if bound != test.withLiteral || referenced != test.withLiteral {
				t.Fatalf("category type bind: supplied=%t referenced=%t; literal category=%t", bound, referenced, test.withLiteral)
			}
			if bound && value != "string" {
				t.Fatalf("literal category type bind=%#v, want string", value)
			}
		})
	}
}
