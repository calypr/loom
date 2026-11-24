package celltrace_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
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
