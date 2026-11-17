package compiler

import (
	"bytes"
	"encoding/json"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"reflect"
	"strings"
	"testing"
)

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
				trace, err := CompileCellTraceOutputWithPolicy(compiled, column, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
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
	returned, ok := sourceStageReshapeForTrace(ir.ClonePhysicalPlan(original))
	if ok || !reflect.DeepEqual(returned, original) {
		t.Fatal("filtered Pivot replay was flattened or its stage population changed")
	}
}
